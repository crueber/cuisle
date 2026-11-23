package app_test

// Cold-start contract (architecture.md section 5.1): GET /boot.js returns `window.__CUISLE_BOOT__ = {...};` with the
// last standup's view, built from the cache, so the first paint needs no IPC round trip.

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/app"
)

const bootPrefix = "window.__CUISLE_BOOT__ = "

// serveBoot runs GET /boot.js through the middleware with a next handler that fails the test if reached.
func serveBoot(t *testing.T, ta *testApp) *httptest.ResponseRecorder {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("GET /boot.js reached the next handler; the middleware must serve it")
	})
	h := app.BootMiddleware(ta.Svc)(next)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boot.js", nil))
	return rec
}

// bootJSON strips the assignment wrapper and returns the raw JSON object.
func bootJSON(t *testing.T, body string) string {
	t.Helper()
	if !strings.HasPrefix(body, bootPrefix) {
		t.Fatalf("body must start with %q, got %.60q", bootPrefix, body)
	}
	rest := strings.TrimSpace(strings.TrimPrefix(body, bootPrefix))
	if !strings.HasSuffix(rest, ";") {
		t.Fatalf("body must end with a semicolon, got ...%q", rest[max(0, len(rest)-30):])
	}
	return strings.TrimSuffix(rest, ";")
}

func TestBootMiddleware_ServesBootJSWithTheBootstrapJSON(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	want := ta.bootstrap()

	rec := serveBoot(t, ta)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	mt, _, err := mime.ParseMediaType(rec.Header().Get("Content-Type"))
	if err != nil || mt != "text/javascript" {
		t.Fatalf("Content-Type = %q (media type %q, err %v), want text/javascript", rec.Header().Get("Content-Type"), mt, err)
	}
	raw := bootJSON(t, rec.Body.String())
	var got app.Bootstrap
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("boot payload is not valid Bootstrap JSON: %v\n%.200s", err, raw)
	}
	if got.View == nil || got.View.StandupID != id {
		t.Fatalf("boot view = %+v, want the view of standup %d (the last/first configured standup)", got.View, id)
	}
	if len(got.View.Tickets) == 0 {
		t.Errorf("boot view has no tickets after a sync; the cached board must be in the boot payload")
	}
	if g, w := fmtJSON(got), fmtJSON(want); g != w {
		t.Errorf("boot.js payload differs from AppService.Bootstrap\n got: %.400s\nwant: %.400s", g, w)
	}
}

func TestBootMiddleware_BootsWithNullViewAndEmptyListsWhenNothingIsConfigured(t *testing.T) {
	ta := newTestApp(t)
	rec := serveBoot(t, ta)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(bootJSON(t, rec.Body.String())), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if string(m["view"]) != "null" {
		t.Errorf(`"view" = %s, want null`, m["view"])
	}
	for _, k := range []string{"standups", "sync"} {
		if string(m[k]) != "[]" {
			t.Errorf("%q = %s, want [] (lists are never null)", k, m[k])
		}
	}
	var conn app.Connection
	_ = json.Unmarshal(m["connection"], &conn)
	if conn.State != "missing" {
		t.Errorf("connection.state = %q, want missing", conn.State)
	}
}

func TestBootMiddleware_PassesOtherRequestsToNext(t *testing.T) {
	ta := newTestApp(t)
	for _, p := range []string{"/", "/index.html", "/assets/app.js", "/boot.json", "/boot.js/extra", "/sub/boot.js"} {
		t.Run(p, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusTeapot)
				_, _ = w.Write([]byte("next"))
			})
			rec := httptest.NewRecorder()
			app.BootMiddleware(ta.Svc)(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
			if !called || rec.Code != http.StatusTeapot || rec.Body.String() != "next" {
				t.Errorf("GET %s: next called=%v code=%d body=%q, want the next handler's response", p, called, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestBootMiddleware_BootJSCarriesNoSecrets(t *testing.T) {
	ta := newTestApp(t)
	ta.scrumStandup()
	body := serveBoot(t, ta).Body.String()
	for _, secret := range []string{fakeToken(), testAccountBasicAuth()} {
		if strings.Contains(body, secret) {
			t.Errorf("boot.js contains the secret %q", secret)
		}
	}
}
