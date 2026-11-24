package jira

// Attachment downloads (jira-sync.md §2.14, ADR 0015). Requests go through c.http, so the connection's limiter takes a
// Media-lane permit for each of them (WithMedia).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/source/jira/limiter"
)

// mediaUUIDRe captures the media file id from a pre-signed media URL: …/file/<uuid>/binary…
var mediaUUIDRe = regexp.MustCompile(`/file/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})/binary`)

// thumbQuery is the provider-made thumbnail request (jira-sync §2.14): a 320×200 box, no fallback to a default image.
const thumbQuery = "width=320&height=200&fallbackToDefault=false"

// FetchMedia streams an attachment original (thumb=false) or the provider thumbnail (thumb=true). The original is read
// without following the redirect: the 303 Location names the pre-signed media URL, whose uuid is returned as
// MediaFileID and which is then fetched without Authorization. The caller closes Body.
func (a *Adapter) FetchMedia(ctx context.Context, att model.Attachment, thumb bool) (source.MediaBlob, error) {
	ctx = WithMedia(ctx)
	hc := a.mediaClient()
	if thumb {
		target := a.c.base + "/rest/api/3/attachment/thumbnail/" + url.PathEscape(att.ID) + "?" + thumbQuery
		resp, err := a.mediaGet(ctx, hc, target, true)
		if err != nil {
			return source.MediaBlob{}, MapError(stripURL(err))
		}
		if resp.StatusCode/100 != 2 {
			return source.MediaBlob{}, a.mediaFailure(resp, "/rest/api/3/attachment/thumbnail/", false)
		}
		return mediaBlob(resp, att.Mime, ""), nil
	}

	target := a.c.base + "/rest/api/3/attachment/content/" + url.PathEscape(att.ID)
	resp, err := a.mediaGet(ctx, hc, target, true)
	if err != nil {
		return source.MediaBlob{}, MapError(stripURL(err))
	}
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		loc, lerr := resp.Location()
		if lerr != nil {
			_ = drainClose(resp)
			return source.MediaBlob{}, apperr.New(apperr.MediaUnavailable, mediaUnavailableMsg)
		}
		mediaID := ""
		if m := mediaUUIDRe.FindStringSubmatch(loc.Path); m != nil {
			mediaID = m[1]
		}
		_ = drainClose(resp)
		mresp, err := a.mediaGet(ctx, hc, loc.String(), false)
		if err != nil {
			return source.MediaBlob{}, MapError(stripURL(err))
		}
		if mresp.StatusCode/100 != 2 {
			return source.MediaBlob{}, a.mediaFailure(mresp, "/rest/api/3/attachment/content/", true)
		}
		return mediaBlob(mresp, att.Mime, mediaID), nil
	}
	if resp.StatusCode/100 == 2 {
		return mediaBlob(resp, att.Mime, ""), nil
	}
	return source.MediaBlob{}, a.mediaFailure(resp, "/rest/api/3/attachment/content/", false)
}

const mediaUnavailableMsg = "This attachment is no longer available in Jira."

// mediaClient is a copy of the connection's http.Client that surfaces redirects instead of following them. It shares
// the transport, so the limiter still applies.
func (a *Adapter) mediaClient() *http.Client {
	hc := *a.c.http
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &hc
}

// mediaGet sends one GET. Jira-authenticated requests carry the Basic credentials; the pre-signed URL never does.
// A 5xx is retried within the client's budget (jira-sync §3).
func (a *Adapter) mediaGet(ctx context.Context, hc *http.Client, target string, auth bool) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, fmt.Errorf("build media request: %w", err)
		}
		req.Header.Set("Accept", "*/*")
		if auth {
			req.Header.Set("Authorization", a.c.auth)
			req.Header.Set("User-Agent", a.c.ua)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		if !retryable5xx(resp.StatusCode) || attempt >= a.c.pol.opts.MaxRetries5xx {
			return resp, nil
		}
		_ = drainClose(resp)
		if serr := a.c.pol.sleep(ctx, a.c.pol.Backoff(attempt+1)); serr != nil {
			return nil, serr
		}
	}
}

// mediaFailure closes a non-2xx answer and maps it: 404 (and 403 on the pre-signed URL, expired or deleted) is
// media_unavailable; everything else goes through the usual HTTP mapping. A 429 closes the connection's gate.
func (a *Adapter) mediaFailure(resp *http.Response, path string, presigned bool) error {
	defer func() { _ = drainClose(resp) }()
	status := resp.StatusCode
	if status == http.StatusNotFound || (presigned && status == http.StatusForbidden) {
		return apperr.New(apperr.MediaUnavailable, mediaUnavailableMsg)
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	now := time.Now()
	if status == http.StatusTooManyRequests {
		if ra := limiter.RetryAfter(resp.Header, now); ra <= a.c.pol.opts.MaxGate {
			a.c.Limiter().CloseGate(now.Add(ra))
		}
	}
	return MapError(newHTTPError(http.MethodGet, path, status, resp.Header, data, now))
}

// mediaBlob wraps a 2xx answer. Mime is the response type without parameters (falling back to the attachment's); Bytes
// is -1 when the response is chunked.
func mediaBlob(resp *http.Response, fallbackMime, mediaID string) source.MediaBlob {
	mime := strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])
	if mime == "" {
		mime = fallbackMime
	}
	return source.MediaBlob{Body: resp.Body, Mime: mime, Bytes: resp.ContentLength, MediaFileID: mediaID}
}

// drainClose discards a small remainder and closes the body, returning the permit to the limiter.
func drainClose(resp *http.Response) error {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.Body.Close()
}

// stripURL removes the request URL from a transport error, because a pre-signed media URL carries its signature in
// the query. The inner error is mapped as before (cancelled, timeout, offline).
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
