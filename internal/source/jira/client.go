package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/source/jira/limiter"
)

const maxResponseBytes = 16 << 20

// errResponseTooLarge is returned when a body exceeds maxResponseBytes. It is never retried.
var errResponseTooLarge = apperr.New(apperr.SourceHTTP, "Jira returned more data than expected.")

// NewClient builds a Client. token is held in memory only.
func NewClient(baseURL, account, token string, opts ClientOptions) *Client {
	c := &Client{
		base: strings.TrimRight(baseURL, "/"),
		auth: "Basic " + base64.StdEncoding.EncodeToString([]byte(account+":"+token)),
		http: opts.HTTP,
		ua:   opts.UserAgent,
		log:  opts.Logger,
		pol:  opts.Policy,
		now:  opts.Now,
	}
	if c.http == nil {
		c.http = defaultHTTPClient()
	} else if c.http.CheckRedirect == nil {
		hc := *c.http // never forward credentials across hosts, whatever client the caller gave us
		hc.CheckRedirect = refuseUnsafeRedirect
		c.http = &hc
	}
	if c.ua == "" {
		c.ua = "Cuisle/dev (" + runtime.GOOS + ")"
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.pol == nil {
		c.pol = DefaultPolicy()
	}
	if c.now == nil {
		c.now = time.Now
	}
	c.limit(account)
	return c
}

func defaultHTTPClient() *http.Client {
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        8,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: refuseUnsafeRedirect}
}

// refuseUnsafeRedirect stops redirects that change host or downgrade https to http, so the Authorization header is
// never sent anywhere else. Same-host redirects follow Go's default limit of 10.
func refuseUnsafeRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	first := via[0].URL
	if req.URL.Host != first.Host || (first.Scheme == "https" && req.URL.Scheme != "https") {
		return errors.New("refusing redirect to a different host or scheme")
	}
	return nil
}

// GetJSON performs GET base+path?q under the policy and decodes a 2xx JSON body into out (nil = discard).
// Non-2xx → *HTTPError (after policy retries); transport errors are returned as-is (map with MapError).
func (c *Client) GetJSON(ctx context.Context, path string, q url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, q, nil, out)
}

// PostJSON performs POST base+path?q with body encoded as JSON, decoding a 2xx body into out.
func (c *Client) PostJSON(ctx context.Context, path string, q url.Values, body any, out any) error {
	return c.do(ctx, http.MethodPost, path, q, body, out)
}

// Counts returns requests sent and 429 responses seen by this client.
func (c *Client) Counts() (requests, throttled int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reqs, c.thrott
}

// do runs one logical request: attempts until success, a non-retryable outcome, or the retry budgets run out.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, payload any, out any) error {
	var body []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode %s body: %w", path, err)
		}
		body = b
	}
	target := c.base + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	write := limiter.IsWrite(ctx) // ADR 0015: a write is attempted once and never retried
	var retries429, retries5xx int
	for {
		sent := c.now()
		status, hdr, data, err := c.attempt(ctx, method, path, target, body)
		if err != nil {
			var ge *limiter.GateError
			if errors.As(err, &ge) { // the limiter refused a write while the 429 gate is closed: nothing was sent
				return &HTTPError{Status: http.StatusTooManyRequests, Method: method, Path: path, RetryAfter: ge.RetryIn}
			}
			if write || ctx.Err() != nil || unreachable(err) || errors.Is(err, errResponseTooLarge) || retries5xx >= c.pol.opts.MaxRetries5xx {
				return err
			}
			retries5xx++
			c.log.Debug("jira transport failure, retrying", "method", method, "path", path, "retry", retries5xx)
			if serr := c.pol.sleep(ctx, c.pol.Backoff(retries5xx)); serr != nil {
				return serr
			}
			continue
		}
		if status/100 == 2 {
			if out != nil {
				if err := json.Unmarshal(data, out); err != nil {
					return fmt.Errorf("decode %s response: %w", path, err)
				}
			}
			return nil
		}
		he := newHTTPError(method, path, status, hdr, data, c.now())
		switch {
		case status == http.StatusTooManyRequests:
			// The limiter (Transport → Observe) has already halved the connection's rate; the gate is ours to close
			// (ManualGate) once the budgets below say this Retry-After is honoured. The retry then waits in Acquire.
			now := c.now()
			// ra comes from the same clock (c.now) as he.RetryAfter; the gate end is an absolute time on the limiter's real
			// clock, so only the duration crosses over.
			ra := limiter.RetryAfter(hdr, now)
			if write {
				// A write is never retried, but the next request of the connection still has to wait this 429 out.
				if ra <= c.pol.opts.MaxGate {
					c.Limiter().CloseGate(time.Now().Add(c.pol.wait429(ra)))
				}
				return he
			}
			if retries429 >= c.pol.opts.Max429Retries {
				return he
			}
			retries429++
			if ra > c.pol.opts.MaxGate {
				return he // one Retry-After beyond the whole budget: give up without closing the gate
			}
			wait := c.pol.wait429(ra)
			if !c.reserveGate(sent, now, wait) {
				return he
			}
			c.Limiter().CloseGate(time.Now().Add(wait))
		case retryable5xx(status):
			if write || retries5xx >= c.pol.opts.MaxRetries5xx {
				return he
			}
			retries5xx++
			wait := c.pol.Backoff(retries5xx)
			if hdr.Get("Retry-After") != "" {
				wait = min(limiter.RetryAfter(hdr, c.now()), c.pol.opts.MaxGate)
			}
			if serr := c.pol.sleep(ctx, wait); serr != nil {
				return serr
			}
		default:
			return he
		}
	}
}

// attempt sends one HTTP request and returns the status, headers and body. The limiter (jira-sync §3.1) and the policy
// slot are taken by the transport NewClient installed (see limit), so this path cannot reach the network without them.
func (c *Client) attempt(ctx context.Context, method, path, target string, body []byte) (int, http.Header, []byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	if source.IsInteractive(ctx) { // the core's neutral marker (source.WithInteractive) → the limiter's interactive lane
		ctx = WithInteractive(ctx)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rdr)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("build %s request: %w", path, err)
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.ua)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return 0, nil, nil, err
	}
	if len(data) > maxResponseBytes {
		c.log.Debug("jira response too large", "method", method, "path", path, "limit", maxResponseBytes)
		return 0, nil, nil, errResponseTooLarge
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		c.mu.Lock()
		c.thrott++
		c.mu.Unlock()
	}
	c.log.Debug("jira response", "method", method, "path", path, "status", resp.StatusCode)
	return resp.StatusCode, resp.Header, data, nil
}

// reserveGate accounts a 429 gate window [now, now+wait] against the client's MaxGate budget. A request sent before the
// previous window was closed was part of the same burst, so its window is merged (the union counts once); a request sent
// after it opens a new window and counts in full. It returns false, leaving the budget untouched, when the window would
// push the total over MaxGate.
func (c *Client) reserveGate(sent, now time.Time, wait time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	until := now.Add(wait)
	from := now
	if !c.gateClosedAt.IsZero() && !sent.After(c.gateClosedAt) && c.gateUntil.After(from) {
		from = c.gateUntil
	}
	var add time.Duration
	if until.After(from) {
		add = until.Sub(from)
	}
	if time.Duration(c.gateMs)*time.Millisecond+add > c.pol.opts.MaxGate {
		return false
	}
	c.gateMs += int64(add / time.Millisecond)
	if until.After(c.gateUntil) {
		c.gateUntil = until
	}
	c.gateClosedAt = now
	return true
}

func retryable5xx(status int) bool {
	switch status {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// newHTTPError builds an HTTPError. Only Jira's errorMessages and errors{} values are kept from the body.
func newHTTPError(method, path string, status int, hdr http.Header, body []byte, now time.Time) *HTTPError {
	he := &HTTPError{
		Status:      status,
		Method:      method,
		Path:        path,
		LoginReason: hdr.Get("X-Seraph-LoginReason"),
		NonJSON:     !json.Valid(body),
	}
	if !he.NonJSON {
		var b struct {
			ErrorMessages []string       `json:"errorMessages"`
			Errors        map[string]any `json:"errors"`
		}
		if json.Unmarshal(body, &b) == nil {
			he.Messages = append(he.Messages, b.ErrorMessages...)
			keys := make([]string, 0, len(b.Errors))
			for k := range b.Errors {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if s, ok := b.Errors[k].(string); ok && s != "" {
					he.Messages = append(he.Messages, s)
				}
			}
		}
	}
	if hdr.Get("Retry-After") != "" || hdr.Get("X-RateLimit-Reset") != "" || hdr.Get("RateLimit") != "" {
		he.RetryAfter = limiter.RetryAfter(hdr, now)
	}
	return he
}

// UnmarshalJSON implements the Time formats documented on the type.
func (t *Time) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		t.Time = time.Time{}
		return nil
	case strings.HasPrefix(s, `"`):
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		str = strings.TrimSpace(str)
		if str == "" {
			t.Time = time.Time{}
			return nil
		}
		v, err := parseJiraTime(str)
		if err != nil {
			return err
		}
		t.Time = v
		return nil
	case strings.HasPrefix(s, "-") || (s != "" && s[0] >= '0' && s[0] <= '9'):
		v, err := parseEpochMillis(s)
		if err != nil {
			return err
		}
		t.Time = v
		return nil
	}
	return fmt.Errorf("jira time: unsupported JSON value")
}

func parseJiraTime(s string) (time.Time, error) {
	if isEpochText(s) {
		return parseEpochMillis(s)
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999Z0700", "2006-01-02T15:04:05.999999999Z07:00"} {
		if v, err := time.Parse(layout, s); err == nil {
			return v.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("jira time: unrecognised timestamp")
}

func isEpochText(s string) bool {
	digits := strings.TrimPrefix(s, "-")
	if digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseEpochMillis(s string) (time.Time, error) {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("jira time: not epoch milliseconds")
	}
	return time.UnixMilli(ms).UTC(), nil
}
