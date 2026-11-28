// Package jira is the Jira Cloud source adapter (jira-sync.md §1–§3). It is the only package that speaks Jira.
//
// FROZEN CONTRACT: the types and signatures in this file. File ownership:
//
//	WP-06 client*.go policy*.go errors*.go   (HTTP client, request policy, error mapping, timestamp decoding)
//	WP-18 endpoints_ref*.go                  (Test, SearchSpaces, SearchBoards, BoardConfig, ActiveSprints,
//	                                          Reference incl. field discovery, SearchUsers, ValidateFilter)
//	WP-19 plan*.go scan*.go hydrate*.go dev*.go (Plan, Scan, Hydrate, DevActivity)
//
// Imports allowed: source, model, apperr, stdlib (never cache, store, sync).
package jira

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

// Kind is the registered source kind.
const Kind = "jira"

// Meta is persisted in connection.source_meta (JSON keys as written).
type Meta struct {
	SprintFieldID  string `json:"sprintFieldId,omitempty"`
	RankFieldID    string `json:"rankFieldId,omitempty"`
	FlaggedFieldID string `json:"flaggedFieldId,omitempty"`
	// DevSummaryFieldID is the Development summary custom field (schema custom type ending ":devsummarycf"), requested
	// in the scan so the dev fingerprint comes for free (jira-sync §2.11). "" = the site has none.
	DevSummaryFieldID string `json:"devSummaryFieldId,omitempty"`
	// DevFieldChecked: the field list was searched for DevSummaryFieldID at least once (so "" means absent, not unknown).
	DevFieldChecked bool      `json:"devSummaryFieldChecked,omitempty"`
	FieldsFetchedAt time.Time `json:"fieldsFetchedAt,omitzero"`
	DevActivity     string    `json:"devActivity,omitempty"` // "ok"|"unavailable"|"" (probe result of this Source)
}

// Adapter implements source.Source. One Adapter is opened per sync run or per Configure request burst.
type Adapter struct {
	conn      model.Connection
	c         *Client
	log       *slog.Logger
	now       func() time.Time
	mu        sync.Mutex
	meta      Meta
	devOff    bool // dev-status probe failed in this Adapter's lifetime
	devProven bool // a dev-status request succeeded in this Adapter's lifetime (guarded by mu)
	// devHints: per issue id, what the last Scan learned from the Development summary field (guarded by mu).
	devHints map[string]devHint
	// devFieldMu serialises the one-off discovery of the Development field id (parallel scans).
	devFieldMu sync.Mutex
	types      map[string]model.IssueType // issue types seen by Hydrate (guarded by mu); Reference returns them
}

var _ source.Source = (*Adapter)(nil)

// New is the source.Factory for Jira. It does no I/O.
func New(conn model.Connection, secret string, opts source.Options) (source.Source, error) {
	if err := checkBaseURL(conn.BaseURL); err != nil {
		return nil, err
	}
	a := &Adapter{conn: conn, log: opts.Logger, now: opts.Now}
	if a.log == nil {
		a.log = slog.New(slog.DiscardHandler)
	}
	if a.now == nil {
		a.now = time.Now
	}
	a.meta = metaFromMap(opts.Meta)
	if opts.Full {
		a.meta.FieldsFetchedAt = time.Time{} // jira-sync §2.2: fields are re-discovered on every full sync
	}
	a.c = NewClient(conn.BaseURL, conn.Account, secret, ClientOptions{
		HTTP: opts.HTTP, Logger: a.log, UserAgent: opts.UserAgent, Policy: DefaultPolicy(), Now: a.now,
	})
	return a, nil
}

// checkBaseURL refuses plain http unless the host is loopback (the fake Jira), so credentials never cross the network
// in clear text.
func checkBaseURL(baseURL string) error {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return apperr.Validationf("baseUrl", "Enter a valid site address.")
	}
	if strings.EqualFold(u.Scheme, "http") {
		switch strings.ToLower(u.Hostname()) {
		case "127.0.0.1", "::1", "localhost":
		default:
			return apperr.Validationf("baseUrl", "Use an https:// site address.")
		}
	}
	return nil
}

// IssueURL is the source.URLFunc for Jira: "<base>/browse/<key>".
func IssueURL(baseURL, key string) string { return strings.TrimRight(baseURL, "/") + "/browse/" + key }

func (a *Adapter) Kind() string               { return Kind }
func (a *Adapter) IssueURL(key string) string { return IssueURL(a.conn.BaseURL, key) }

// Capabilities per jira-sync.md header table; HasDevActivity is false once the probe failed.
func (a *Adapter) Capabilities() source.Capabilities {
	a.mu.Lock()
	defer a.mu.Unlock()
	return source.Capabilities{
		HasSprints: true, HasBacklogRank: true, HasFlagging: true, HasDevActivity: !a.devOff,
		HasExtraFilter: true, HasAssigneeHistory: true, FilterLanguage: "JQL",
		AuthFields: []string{"baseUrl", "account", "secret"},
		// ADR 0015: the hooks live in WP-owned files (content.go: WP-T4, writes.go: WP-T5) so the frozen contract
		// needn't change when those WPs land. Both default to "off".
		Content: contentCapability(),
		Writes:  writesCapabilities(a.meta),
	}
}

// Meta returns the field ids and dev probe state to persist.
func (a *Adapter) Meta() map[string]any {
	a.mu.Lock()
	m := a.meta
	if a.devOff {
		m.DevActivity = "unavailable"
	}
	a.mu.Unlock()
	return metaToMap(m)
}

// Requests reports the client's counters.
func (a *Adapter) Requests() (int, int) { return a.c.Counts() }

// ── Client (WP-06) ────────────────────────────────────────────────────────────

type ClientOptions struct {
	HTTP      *http.Client // nil = a client with 10 s connect / 30 s request timeouts, HTTP/2, keep-alive
	Logger    *slog.Logger
	UserAgent string
	Policy    *Policy
	Now       func() time.Time
}

// Client performs authenticated JSON requests under the request policy (jira-sync.md §1, §3).
type Client struct {
	base   string
	auth   string // "Basic base64(account:token)"; never logged
	http   *http.Client
	ua     string
	log    *slog.Logger
	pol    *Policy
	now    func() time.Time
	mu     sync.Mutex
	gateMs int64 // cumulative 429 gate time of this client (run) in ms (union of closed windows)
	// gateUntil is the end of the latest window counted in gateMs.
	gateUntil    time.Time
	gateClosedAt time.Time // when the latest window was closed; later-sent requests start a new window
	reqs         int
	thrott       int
}

// HTTPError is a non-2xx response after the policy gave up retrying.
type HTTPError struct {
	Status      int
	Method      string
	Path        string
	Messages    []string // errorMessages[] + errors{} values, when the body is Jira's error JSON
	RetryAfter  time.Duration
	LoginReason string // X-Seraph-LoginReason
	NonJSON     bool   // body was not JSON (dev-status degrade signal)
}

func (e *HTTPError) Error() string { return e.Method + " " + e.Path + ": HTTP " + strconv.Itoa(e.Status) }

// Time decodes Jira timestamps: ISO-8601 with offset ("2026-11-23T09:15:02.123+0000", "…Z", "…+00:00"), epoch
// milliseconds as a number or numeric string, and null/"" (zero time). Implemented in client.go (WP-06).
type Time struct{ time.Time }

// ── Policy (WP-06) ────────────────────────────────────────────────────────────

type PolicyOptions struct {
	Concurrency   int                                                 // 4
	RatePerSecond float64                                             // 10 (token bucket)
	Max429Retries int                                                 // 5 per request
	MaxGate       time.Duration                                       // 120 s cumulative per Client → rate_limited
	MaxRetries5xx int                                                 // 3
	BaseBackoff   time.Duration                                       // 500 ms (×2 per attempt, ×jitter 0.5–1.5)
	Sleep         func(ctxDone <-chan struct{}, d time.Duration) bool // nil = timer; false when ctx ended
	Jitter        func() float64                                      // nil = math/rand in [0,1)
}

// Policy is shared by every Client in the process (global semaphore, token bucket, 429 gate).
type Policy struct {
	opts PolicyOptions
	sem  chan struct{}
	mu   sync.Mutex
	gate time.Time // all workers wait until this instant
	// token bucket state
	tokens float64
	last   time.Time
}

var (
	policyMu      sync.Mutex
	defaultPolicy *Policy
)

// DefaultPolicy returns the process-wide policy with the jira-sync.md §1/§3 numbers.
func DefaultPolicy() *Policy {
	policyMu.Lock()
	defer policyMu.Unlock()
	if defaultPolicy == nil {
		defaultPolicy = NewPolicy(PolicyOptions{})
	}
	return defaultPolicy
}

// SetDefaultPolicy replaces the process-wide policy for Adapters created afterwards (tests: a fast policy without the
// 10/s bucket) and returns a function restoring the previous one.
func SetDefaultPolicy(p *Policy) (restore func()) {
	policyMu.Lock()
	defer policyMu.Unlock()
	prev := defaultPolicy
	defaultPolicy = p
	return func() {
		policyMu.Lock()
		defer policyMu.Unlock()
		defaultPolicy = prev
	}
}

func metaFromMap(m map[string]any) Meta {
	var out Meta
	if m == nil {
		return out
	}
	s := func(k string) string { v, _ := m[k].(string); return v }
	out.SprintFieldID, out.RankFieldID, out.FlaggedFieldID = s("sprintFieldId"), s("rankFieldId"), s("flaggedFieldId")
	out.DevActivity = s("devActivity")
	out.DevSummaryFieldID = s("devSummaryFieldId")
	out.DevFieldChecked, _ = m["devSummaryFieldChecked"].(bool)
	if v := s("fieldsFetchedAt"); v != "" {
		out.FieldsFetchedAt, _ = time.Parse(time.RFC3339Nano, v)
	}
	return out
}

func metaToMap(m Meta) map[string]any {
	out := map[string]any{}
	set := func(k, v string) {
		if v != "" {
			out[k] = v
		}
	}
	set("sprintFieldId", m.SprintFieldID)
	set("rankFieldId", m.RankFieldID)
	set("flaggedFieldId", m.FlaggedFieldID)
	set("devActivity", m.DevActivity)
	set("devSummaryFieldId", m.DevSummaryFieldID)
	if m.DevFieldChecked {
		out["devSummaryFieldChecked"] = true
	}
	if !m.FieldsFetchedAt.IsZero() {
		out["fieldsFetchedAt"] = m.FieldsFetchedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}
