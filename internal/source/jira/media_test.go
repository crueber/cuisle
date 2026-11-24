package jira

// WP-T4 tests for Adapter.FetchMedia (jira-sync.md §2.14, ADR 0015). The wp19 harness cannot set a Location header
// or see request headers, so this file has its own recording httptest server.
//
// SPEC-NOTE (original): FetchMedia(att, false) sends GET /rest/api/3/attachment/content/{att.ID} WITH Authorization and
// does not auto-follow the redirect. On 303 (also 301/302/307/308) it reads Location, captures the media file id from
// `…/file/<uuid>/binary…` (uuid = 8-4-4-4-12 hex) into MediaBlob.MediaFileID, then GETs the Location EXACTLY as given
// (path and query, it is pre-signed) WITHOUT an Authorization header, even when the redirect stays on the same host
// (Go only strips the header across hosts, so a same-host redirect is the discriminating case). A 200 with a body on the
// first request is accepted as is (MediaFileID empty). A Location without a recognisable uuid still downloads
// (MediaFileID empty, no error).
// SPEC-NOTE (blob): MediaBlob.Body streams the bytes (the caller closes it), Mime is the response Content-Type without
// parameters (falling back to att.Mime when the response has none), Bytes is Content-Length or -1 when the response
// is chunked.
// SPEC-NOTE (thumbnail): FetchMedia(att, true) sends GET /rest/api/3/attachment/thumbnail/{att.ID}?width=320&height=200&
// fallbackToDefault=false with Authorization and streams the answer (200 expected); MediaFileID stays empty.
// SPEC-NOTE (errors): attachment endpoint 404 → media_unavailable; a pre-signed media URL answering 404 or 403 (expired
// or deleted) → media_unavailable; 401 → auth_invalid; 403 on the Jira endpoint → forbidden; 5xx → source_http after the
// client's retries; no connection → offline; a cancelled context → cancelled. No error ever carries the token, and a
// failed fetch returns a zero MediaBlob (Body nil) so there is nothing to close.

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
)

const f12MediaUUID = "3f1c9a52-7d4e-4b6a-9c2e-0a1b2c3d4e51"

type f12MediaReq struct {
	Method, Path, RawQuery, Auth string
}

type f12MediaServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []f12MediaReq
}

func (s *f12MediaServer) log() []f12MediaReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]f12MediaReq(nil), s.reqs...)
}

// f12NewMediaServer routes by path; handler gets the request after it was logged.
func f12NewMediaServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *f12MediaServer {
	t.Helper()
	s := &f12MediaServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, f12MediaReq{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")})
		s.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

var f12PNG = []byte("\x89PNG\r\n\x1a\n-not-a-real-image-but-bytes-are-bytes-")

func f12Att() model.Attachment {
	return model.Attachment{ID: "20001", Name: "unit-toggle-v3.png", Mime: "image/png", Bytes: int64(len(f12PNG))}
}

func f12BasicAuth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("me@acme.example:tok-123"))
}

func f12ReadBlob(t *testing.T, b interface{ Read([]byte) (int, error) }) []byte {
	t.Helper()
	data, err := io.ReadAll(b)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return data
}

func f12Fetch(t *testing.T, srv *f12MediaServer, att model.Attachment, thumb bool) (data []byte, mime string, nbytes int64, mediaID string, err error) {
	t.Helper()
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	blob, err := a.FetchMedia(context.Background(), att, thumb)
	if err != nil {
		if blob.Body != nil {
			t.Errorf("a failed FetchMedia returned a non-nil Body")
			blob.Body.Close()
		}
		return nil, "", 0, "", err
	}
	if blob.Body == nil {
		t.Fatal("MediaBlob.Body is nil without an error")
	}
	defer blob.Body.Close()
	return f12ReadBlob(t, blob.Body), blob.Mime, blob.Bytes, blob.MediaFileID, nil
}

// redirecting serves the attachment endpoint as a 303 to the pre-signed media URL on the same server.
func f12Redirecting(mediaPath string, contentType string, body []byte, location func(base string) string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/attachment/content/20001":
			w.Header().Set("Location", location("http://"+r.Host))
			w.WriteHeader(http.StatusSeeOther)
		case r.URL.Path == mediaPath:
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}
}

func TestFetchMedia_Original_FollowsTheRedirectWithoutAuthorizationAndCapturesTheMediaId(t *testing.T) {
	defer wp19Guard(t)
	media := "/media/file/" + f12MediaUUID + "/binary"
	srv := f12NewMediaServer(t, f12Redirecting(media, "image/png", f12PNG, func(base string) string {
		return base + media + "?token=abc.def&client=cl-1&collection=c1"
	}))
	data, mime, nbytes, mediaID, err := f12Fetch(t, srv, f12Att(), false)
	if err != nil {
		t.Fatalf("FetchMedia: %v", err)
	}
	if string(data) != string(f12PNG) {
		t.Errorf("body = %q, want the media bytes", data)
	}
	if mime != "image/png" || nbytes != int64(len(f12PNG)) {
		t.Errorf("Mime/Bytes = %q/%d, want image/png/%d", mime, nbytes, len(f12PNG))
	}
	if mediaID != f12MediaUUID {
		t.Errorf("MediaFileID = %q, want %q (from the redirect URL)", mediaID, f12MediaUUID)
	}
	log := srv.log()
	if len(log) != 2 {
		t.Fatalf("requests = %+v, want exactly the attachment request and the media request", log)
	}
	if log[0].Method != "GET" || log[0].Path != "/rest/api/3/attachment/content/20001" || log[0].Auth != f12BasicAuth() {
		t.Errorf("request 1 = %+v, want GET attachment/content/20001 with the Basic Authorization", log[0])
	}
	if log[1].Method != "GET" || log[1].Path != media || log[1].RawQuery != "token=abc.def&client=cl-1&collection=c1" {
		t.Errorf("request 2 = %+v, want the Location verbatim (path and query)", log[1])
	}
	if log[1].Auth != "" {
		t.Errorf("request 2 carries Authorization %q: the pre-signed media URL must be fetched without it", log[1].Auth)
	}
}

func TestFetchMedia_Original_ChunkedResponseReportsUnknownSize(t *testing.T) {
	defer wp19Guard(t)
	media := "/media/file/" + f12MediaUUID + "/binary"
	srv := f12NewMediaServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/attachment/content/20001":
			w.Header().Set("Location", "http://"+r.Host+media)
			w.WriteHeader(http.StatusSeeOther)
		case media:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(f12PNG[:10])
			w.(http.Flusher).Flush() // chunked: no Content-Length
			_, _ = w.Write(f12PNG[10:])
		}
	})
	data, _, nbytes, _, err := f12Fetch(t, srv, f12Att(), false)
	if err != nil {
		t.Fatalf("FetchMedia: %v", err)
	}
	if string(data) != string(f12PNG) || nbytes != -1 {
		t.Errorf("body %d bytes, Bytes = %d, want the whole body and -1 (unknown)", len(data), nbytes)
	}
}

func TestFetchMedia_Original_MimeParametersAreDropped(t *testing.T) {
	defer wp19Guard(t)
	media := "/media/file/" + f12MediaUUID + "/binary"
	srv := f12NewMediaServer(t, f12Redirecting(media, "image/png; charset=binary", f12PNG, func(base string) string { return base + media }))
	_, mime, _, _, err := f12Fetch(t, srv, f12Att(), false)
	if err != nil {
		t.Fatalf("FetchMedia: %v", err)
	}
	if mime != "image/png" {
		t.Errorf("Mime = %q, want image/png", mime)
	}
}

func TestFetchMedia_Original_NoRedirectIsAcceptedWithoutAMediaId(t *testing.T) {
	defer wp19Guard(t)
	srv := f12NewMediaServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/attachment/content/20001" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", fmt.Sprint(len(f12PNG)))
		_, _ = w.Write(f12PNG)
	})
	data, mime, nbytes, mediaID, err := f12Fetch(t, srv, f12Att(), false)
	if err != nil {
		t.Fatalf("FetchMedia: %v", err)
	}
	if string(data) != string(f12PNG) || mime != "image/png" || nbytes != int64(len(f12PNG)) {
		t.Errorf("body %d bytes, Mime %q, Bytes %d", len(data), mime, nbytes)
	}
	if mediaID != "" {
		t.Errorf("MediaFileID = %q, want empty (no redirect, nothing learned)", mediaID)
	}
	if n := len(srv.log()); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

func TestFetchMedia_Original_LocationWithoutAUuidStillDownloads(t *testing.T) {
	defer wp19Guard(t)
	media := "/some/other/path"
	srv := f12NewMediaServer(t, f12Redirecting(media, "image/png", f12PNG, func(base string) string { return base + media }))
	data, _, _, mediaID, err := f12Fetch(t, srv, f12Att(), false)
	if err != nil {
		t.Fatalf("FetchMedia: %v", err)
	}
	if string(data) != string(f12PNG) || mediaID != "" {
		t.Errorf("body %d bytes, MediaFileID %q, want the bytes and an empty id", len(data), mediaID)
	}
}

func TestFetchMedia_Thumbnail_UsesTheThumbnailEndpointAndDoesNotLearnAMediaId(t *testing.T) {
	defer wp19Guard(t)
	srv := f12NewMediaServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/attachment/thumbnail/20001" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", "6")
		_, _ = w.Write([]byte("thumb!"))
	})
	data, mime, nbytes, mediaID, err := f12Fetch(t, srv, f12Att(), true)
	if err != nil {
		t.Fatalf("FetchMedia(thumb): %v", err)
	}
	if string(data) != "thumb!" || mime != "image/jpeg" || nbytes != 6 || mediaID != "" {
		t.Errorf("data %q mime %q bytes %d mediaID %q", data, mime, nbytes, mediaID)
	}
	log := srv.log()
	if len(log) != 1 {
		t.Fatalf("requests = %+v, want 1", log)
	}
	if log[0].Method != "GET" || log[0].Path != "/rest/api/3/attachment/thumbnail/20001" || log[0].Auth != f12BasicAuth() {
		t.Errorf("request = %+v", log[0])
	}
	q, _ := http.NewRequest("GET", "http://x/?"+log[0].RawQuery, nil)
	for k, want := range map[string]string{"width": "320", "height": "200", "fallbackToDefault": "false"} {
		if got := q.URL.Query().Get(k); got != want {
			t.Errorf("thumbnail query %s = %q, want %q (raw %q)", k, got, want, log[0].RawQuery)
		}
	}
}

func TestFetchMedia_MapsErrors(t *testing.T) {
	defer wp19Guard(t)
	media := "/media/file/" + f12MediaUUID + "/binary"
	tests := []struct {
		name  string
		thumb bool
		h     func(w http.ResponseWriter, r *http.Request)
		want  apperr.Code
	}{
		{"original 404 on the attachment endpoint", false, func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"errorMessages":["gone"]}`, 404) }, apperr.MediaUnavailable},
		{"original 401", false, func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{}`, 401) }, apperr.AuthInvalid},
		{"original 403", false, func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{}`, 403) }, apperr.Forbidden},
		{"original 500", false, func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{}`, 500) }, apperr.SourceHTTP},
		{"redirect target 404", false, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == media {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Location", "http://"+r.Host+media)
			w.WriteHeader(http.StatusSeeOther)
		}, apperr.MediaUnavailable},
		{"redirect target 403 (expired signature)", false, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == media {
				http.Error(w, "expired", 403)
				return
			}
			w.Header().Set("Location", "http://"+r.Host+media)
			w.WriteHeader(http.StatusSeeOther)
		}, apperr.MediaUnavailable},
		{"thumbnail 404", true, func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{}`, 404) }, apperr.MediaUnavailable},
		{"thumbnail 401", true, func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{}`, 401) }, apperr.AuthInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp19Guard(t)
			srv := f12NewMediaServer(t, tc.h)
			_, _, _, _, err := f12Fetch(t, srv, f12Att(), tc.thumb)
			wp19AssertCode(t, err, tc.want)
			if s := fmt.Sprintf("%v %+v", err, err); strings.Contains(s, "tok-123") {
				t.Errorf("the error leaks the token: %s", s)
			}
		})
	}
}

func TestFetchMedia_OfflineAndCancelled(t *testing.T) {
	defer wp19Guard(t)
	srv := f12NewMediaServer(t, func(w http.ResponseWriter, r *http.Request) {})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.FetchMedia(ctx, f12Att(), false)
	wp19AssertCode(t, err, apperr.Cancelled)

	srv.Close() // nothing listens any more
	_, err = a.FetchMedia(context.Background(), f12Att(), false)
	wp19AssertCode(t, err, apperr.Offline)
}

func TestFetchMedia_BodyStreamsUntilTheCallerCloses(t *testing.T) {
	defer wp19Guard(t)
	big := make([]byte, 3<<20)
	for i := range big {
		big[i] = byte(i % 251)
	}
	media := "/media/file/" + f12MediaUUID + "/binary"
	srv := f12NewMediaServer(t, f12Redirecting(media, "image/png", big, func(base string) string { return base + media }))
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	blob, err := a.FetchMedia(context.Background(), f12Att(), false)
	if err != nil {
		t.Fatalf("FetchMedia: %v", err)
	}
	if blob.Body == nil {
		t.Fatal("Body is nil")
	}
	head := make([]byte, 16)
	if _, err := io.ReadFull(blob.Body, head); err != nil {
		t.Fatalf("read head: %v", err)
	}
	rest := f12ReadBlob(t, blob.Body)
	if len(head)+len(rest) != len(big) || string(head) != string(big[:16]) {
		t.Errorf("streamed %d bytes, want %d", len(head)+len(rest), len(big))
	}
	if err := blob.Body.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
