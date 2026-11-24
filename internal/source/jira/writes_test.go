package jira

// WP-T5 tests for writes.go: Transitions, Transition, Assign, SetFlag, AddComment (jira-sync.md §2.15 and §3.1,
// ADR 0015, F12-freeze decision 8). Two harnesses:
//   - the fake Jira (jiratest) behind a recording http.RoundTripper, for end-to-end behaviour and exact request bodies;
//   - a tiny httptest mock, where a test needs a specific status, header, error body, transition list or a dropped
//     connection.
//
// Every adapter gets a client whose policy has a very high rate and a no-op Sleep and a fixed clock (wt5Now), so an
// implementation that retries a write fails fast with "N requests, want 1" instead of sleeping. Helpers and types are
// prefixed wt5.
//
// SPEC-NOTE (Capabilities): Capabilities().Writes is {Transition, Assign, Comment: true} always, and Flag is true
// exactly when the adapter knows the Flagged field id (source.Options.Meta "flaggedFieldId"). Without it Flag is false
// and SetFlag returns source.ErrUnsupported (source.IsUnsupported) without sending any request; the other writes work.
// SPEC-NOTE (Transitions): one GET /rest/api/3/issue/{sourceID}/transitions with query expand=transitions.fields.
// Result order = server order; isAvailable == false entries are dropped. Fields: ID, Name, ToStatusID, ToStatusName from
// transitions[].{id,name,to.id,to.name}. NeedsScreen = hasScreen && any field with required && !hasDefaultValue; so
// an optional-only screen, a required field WITH a default, and required fields on a transition with hasScreen == false
// are all NeedsScreen == false. The lookup is not retried-pinned (it is a read); 401 -> auth_invalid, 404 -> not_found.
// SPEC-NOTE (Transition): the adapter has no cache, so it learns NeedsScreen with one GET of the transitions list.
// A transition that NeedsScreen is never posted: error code transition_needs_screen and zero non-GET requests, whether
// or not Transitions() was called before on the same Adapter. An allowed transition is POSTed exactly once with the
// body {"transition":{"id":"<id>"}} (no other keys) after at most one lookup GET. The tests do not pin the error
// code for a transition id that is absent from the list.
// SPEC-NOTE (400 discrimination): Jira's first message is `errorMessages[0]`, else the first value of `errors{}`
// (keys sorted), which is what the client's HTTPError.Messages already holds. On POST transitions, a 400 whose body has
// a non-empty `errors{}` (a field-level complaint: a required screen field the list did not announce) maps to
// transition_needs_screen; a 400 with only errorMessages (a workflow validator) maps to write_rejected with that
// message verbatim. On every other write a 400 is write_rejected with the first message verbatim.
// SPEC-NOTE (other statuses): 401 auth_invalid, 403 permission_denied, 404 not_found, 409 conflict, 429 rate_limited,
// 500/502/503/504 source_http. Messages are non-empty and never contain the token or a raw response body.
// SPEC-NOTE (RetryInMs): a 429 carries RetryInMs = Retry-After seconds x 1000 exactly (7 -> 7000, no jitter); with no
// Retry-After the X-RateLimit-Reset (RFC 3339) minus the client's now; with neither, 5000 (jira-sync §3).
// SPEC-NOTE (transport): a connection that fails before any response (server down) -> offline; a connection dropped
// after the request reached the server -> timeout. Both: exactly one write request reaches the server (never retried).
// SPEC-NOTE (AddComment): the request is POST /rest/api/3/issue/{id}/comment with a JSON object whose only key is
// "body"; its value is JSON-equal to adf.FromPlainText(NewComment).Raw (no `visibility` anywhere in the request).
// DEPENDENCY: TestAddComment_AgainstFake_BodyIsAdfFromPlainText fails (by design) while adf.FromPlainText is the WP-T1
// stub with an empty Raw, so WP-T5 cannot go fully green before WP-T1 is merged. The returned comment id is the string
// `id` of the 201 response.
// SPEC-NOTE (flag/assign wire): Assign(id) sends PUT .../assignee {"accountId":"<id>"}; Assign("") sends
// {"accountId":null} (key present, JSON null). SetFlag(true) sends PUT /rest/api/3/issue/{sourceID}
// {"fields":{"<flaggedFieldId>":[{"value":"Impediment"}]}}; SetFlag(false) sends {"fields":{"<flaggedFieldId>":null}}.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/adf"
	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
)

var wt5Now = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

const (
	wt5Account = "me@acme.example"
	wt5Flagged = "customfield_10021"
)

// ── recording transport ──────────────────────────────────────────────────────────────────────────

type wt5Req struct {
	Method, Path, RawQuery, Body string
	Header                       http.Header
}

type wt5Rec struct {
	next http.RoundTripper
	mu   sync.Mutex
	reqs []wt5Req
}

func (r *wt5Rec) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	r.mu.Lock()
	r.reqs = append(r.reqs, wt5Req{Method: req.Method, Path: req.URL.Path, RawQuery: req.URL.RawQuery, Body: string(body), Header: req.Header.Clone()})
	r.mu.Unlock()
	out := req.Clone(req.Context())
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	return r.next.RoundTrip(out)
}

func (r *wt5Rec) all() []wt5Req {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]wt5Req(nil), r.reqs...)
}

// writes returns the requests that are not GETs.
func (r *wt5Rec) writes() []wt5Req {
	var out []wt5Req
	for _, q := range r.all() {
		if q.Method != http.MethodGet {
			out = append(out, q)
		}
	}
	return out
}

func (r *wt5Rec) gets() []wt5Req {
	var out []wt5Req
	for _, q := range r.all() {
		if q.Method == http.MethodGet {
			out = append(out, q)
		}
	}
	return out
}

func wt5Meta(withFlagged bool) map[string]any {
	m := map[string]any{
		"sprintFieldId":   "customfield_10020",
		"rankFieldId":     "customfield_10019",
		"fieldsFetchedAt": wt5Now.Format(time.RFC3339Nano),
	}
	if withFlagged {
		m["flaggedFieldId"] = wt5Flagged
	}
	return m
}

func newWT5Adapter(t *testing.T, base string, meta map[string]any) (*Adapter, *wt5Rec) {
	t.Helper()
	rec := &wt5Rec{next: &http.Transport{DisableKeepAlives: true}}
	hc := &http.Client{Transport: rec, Timeout: 10 * time.Second}
	now := func() time.Time { return wt5Now }
	src, err := New(model.Connection{ID: 1, Source: Kind, BaseURL: base, Account: wt5Account}, jiratest.Token,
		source.Options{HTTP: hc, Now: now, Meta: meta})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a, ok := src.(*Adapter)
	if !ok {
		t.Fatalf("New returned %T, want *Adapter", src)
	}
	pol := NewPolicy(PolicyOptions{
		RatePerSecond: 1e6,
		Sleep:         func(<-chan struct{}, time.Duration) bool { return true },
		Jitter:        func() float64 { return 0.5 },
	})
	a.c = NewClient(base, wt5Account, jiratest.Token, ClientOptions{HTTP: hc, Policy: pol, Now: now})
	return a, rec
}

func wt5Fake(t *testing.T, opts jiratest.Options) (*jiratest.Server, *Adapter, *wt5Rec) {
	t.Helper()
	fx, err := jiratest.ParseFixture(jiratest.DefaultFixture)
	if err != nil {
		t.Fatalf("ParseFixture: %v", err)
	}
	opts.Now = wt5Now
	opts.Loc = time.UTC
	srv := jiratest.NewServer(t, fx, opts)
	a, rec := newWT5Adapter(t, srv.URL, wt5Meta(true))
	return srv, a, rec
}

type wt5Issue struct {
	ID, Key, StatusID, Assignee string
	Flagged                     bool
	Changes, Comments           int
}

func wt5Snap(srv *jiratest.Server, key string) wt5Issue {
	var out wt5Issue
	srv.Mutate(func(ds *jiratest.Dataset) {
		for _, is := range ds.Issues {
			if is.Key == key {
				out = wt5Issue{ID: is.ID, Key: is.Key, StatusID: is.StatusID, Assignee: is.Assignee, Flagged: is.Flagged,
					Changes: len(is.Changelog), Comments: len(is.Comments)}
			}
		}
	})
	return out
}

// wt5Pick returns an unflagged, assigned board issue in the named status.
func wt5Pick(t *testing.T, srv *jiratest.Server, statusName string) wt5Issue {
	t.Helper()
	var key string
	srv.Mutate(func(ds *jiratest.Dataset) {
		want := ""
		for _, st := range ds.Statuses {
			if st.Name == statusName {
				want = st.ID
			}
		}
		for _, is := range ds.Issues {
			if is.StatusID == want && is.Assignee != "" && !is.Flagged {
				key = is.Key
				return
			}
		}
	})
	if key == "" {
		t.Fatalf("no unflagged assigned issue in %q", statusName)
	}
	return wt5Snap(srv, key)
}

func wt5LastChange(srv *jiratest.Server, key string) jiratest.DSChange {
	var c jiratest.DSChange
	srv.Mutate(func(ds *jiratest.Dataset) {
		for _, is := range ds.Issues {
			if is.Key == key && len(is.Changelog) > 0 {
				c = is.Changelog[len(is.Changelog)-1]
			}
		}
	})
	return c
}

func wt5StatusID(srv *jiratest.Server, name string) string {
	id := ""
	srv.Mutate(func(ds *jiratest.Dataset) {
		for _, st := range ds.Statuses {
			if st.Name == name {
				id = st.ID
			}
		}
	})
	return id
}

func wt5JSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("not JSON: %v (%q)", err, s)
	}
	return v
}

func wt5Code(t *testing.T, err error, want apperr.Code) *apperr.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want code %s", want)
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %T %v, want *apperr.Error with code %s", err, err, want)
	}
	if ae.Code != want {
		t.Fatalf("code = %s (%q), want %s", ae.Code, ae.Message, want)
	}
	if strings.TrimSpace(ae.Message) == "" {
		t.Errorf("error %s has an empty Message", want)
	}
	return ae
}

func wt5Guard(t *testing.T) {
	if r := recover(); r != nil {
		t.Fatalf("panicked: %v", r)
	}
}

// ── mock server ──────────────────────────────────────────────────────────────────────────────────

const wt5StdTransitions = `{"transitions":[
 {"id":"11","name":"Start progress","to":{"id":"3","name":"In Progress"},"hasScreen":false,"isAvailable":true,"fields":{}},
 {"id":"21","name":"Done","to":{"id":"5","name":"Done"},"hasScreen":true,"isAvailable":true,"fields":{"resolution":{"required":true,"hasDefaultValue":false}}}
]}`

// wt5MatrixTransitions covers the NeedsScreen rule: id, expected NeedsScreen (or dropped).
const wt5MatrixTransitions = `{"transitions":[
 {"id":"11","name":"Start progress","to":{"id":"3","name":"In Progress"},"hasScreen":false,"isAvailable":true,"fields":{}},
 {"id":"21","name":"Done","to":{"id":"5","name":"Done"},"hasScreen":true,"isAvailable":true,"fields":{"resolution":{"required":true,"hasDefaultValue":false}}},
 {"id":"31","name":"Send to review","to":{"id":"4","name":"Code Review"},"hasScreen":true,"isAvailable":true,"fields":{"fixVersions":{"required":false,"hasDefaultValue":false},"comment":{"required":false}}},
 {"id":"41","name":"QA with default","to":{"id":"6","name":"QA"},"hasScreen":true,"isAvailable":true,"fields":{"customfield_10090":{"required":true,"hasDefaultValue":true}}},
 {"id":"51","name":"Mixed screen","to":{"id":"7","name":"Blocked"},"hasScreen":true,"isAvailable":true,"fields":{"a":{"required":true,"hasDefaultValue":true},"b":{"required":true,"hasDefaultValue":false}}},
 {"id":"61","name":"Hidden by conditions","to":{"id":"8","name":"Archived"},"hasScreen":false,"isAvailable":false,"fields":{}},
 {"id":"71","name":"Fields without screen","to":{"id":"9","name":"Parked"},"hasScreen":false,"isAvailable":true,"fields":{"x":{"required":true,"hasDefaultValue":false}}},
 {"id":"81","name":"Reopen","to":{"id":"1","name":"To Do"},"hasScreen":false,"isAvailable":true}
]}`

var wt5MatrixWant = []model.Transition{
	{ID: "11", Name: "Start progress", ToStatusID: "3", ToStatusName: "In Progress", NeedsScreen: false},
	{ID: "21", Name: "Done", ToStatusID: "5", ToStatusName: "Done", NeedsScreen: true},
	{ID: "31", Name: "Send to review", ToStatusID: "4", ToStatusName: "Code Review", NeedsScreen: false},
	{ID: "41", Name: "QA with default", ToStatusID: "6", ToStatusName: "QA", NeedsScreen: false},
	{ID: "51", Name: "Mixed screen", ToStatusID: "7", ToStatusName: "Blocked", NeedsScreen: true},
	{ID: "71", Name: "Fields without screen", ToStatusID: "9", ToStatusName: "Parked", NeedsScreen: false},
	{ID: "81", Name: "Reopen", ToStatusID: "1", ToStatusName: "To Do", NeedsScreen: false},
}

type wt5MockCfg struct {
	Status      int               // answer to every write; 0 = 204 (201 {"id":"20001"} for a comment POST)
	Header      map[string]string // headers on the write answer
	Body        string            // body of the write answer
	Drop        bool              // read the write request, then close the connection without answering
	Transitions string            // GET transitions body; "" = wt5StdTransitions
	GetStatus   int               // non-zero: GET transitions answers this status with an error body
}

type wt5Mock struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []wt5Req
}

func newWT5Mock(t *testing.T, cfg wt5MockCfg) *wt5Mock {
	t.Helper()
	m := &wt5Mock{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.reqs = append(m.reqs, wt5Req{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Body: string(b), Header: r.Header.Clone()})
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			if !strings.HasSuffix(r.URL.Path, "/transitions") {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"errorMessages":["no route"]}`)
				return
			}
			if cfg.GetStatus != 0 {
				w.WriteHeader(cfg.GetStatus)
				_, _ = io.WriteString(w, `{"errorMessages":["lookup failed"]}`)
				return
			}
			body := cfg.Transitions
			if body == "" {
				body = wt5StdTransitions
			}
			_, _ = io.WriteString(w, body)
			return
		}
		if cfg.Drop {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		for k, v := range cfg.Header {
			w.Header().Set(k, v)
		}
		status, body := cfg.Status, cfg.Body
		if status == 0 {
			status = http.StatusNoContent
			if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comment") {
				status, body = http.StatusCreated, `{"id":"20001"}`
			}
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(m.Server.Close)
	return m
}

func (m *wt5Mock) writes() []wt5Req {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []wt5Req
	for _, q := range m.reqs {
		if q.Method != http.MethodGet {
			out = append(out, q)
		}
	}
	return out
}

// wt5MockAdapter builds an adapter (flagged field known) against a mock.
func wt5MockAdapter(t *testing.T, cfg wt5MockCfg) (*Adapter, *wt5Mock, *wt5Rec) {
	t.Helper()
	m := newWT5Mock(t, cfg)
	a, rec := newWT5Adapter(t, m.URL, wt5Meta(true))
	return a, m, rec
}

// ── the five actions, as data ────────────────────────────────────────────────────────────────────

type wt5Action struct {
	name string
	run  func(ctx context.Context, a *Adapter) error
}

func wt5Actions() []wt5Action {
	const id = "10001"
	return []wt5Action{
		{"assign", func(ctx context.Context, a *Adapter) error { return a.Assign(ctx, id, "u-bob") }},
		{"unassign", func(ctx context.Context, a *Adapter) error { return a.Assign(ctx, id, "") }},
		{"flag", func(ctx context.Context, a *Adapter) error { return a.SetFlag(ctx, id, true) }},
		{"unflag", func(ctx context.Context, a *Adapter) error { return a.SetFlag(ctx, id, false) }},
		{"comment", func(ctx context.Context, a *Adapter) error {
			_, err := a.AddComment(ctx, id, model.NewComment{Text: "hello"})
			return err
		}},
		{"transition", func(ctx context.Context, a *Adapter) error { return a.Transition(ctx, id, "11") }},
	}
}

// ── Capabilities ─────────────────────────────────────────────────────────────────────────────────

func TestCapabilities_Writes_AllTrueWhenFlaggedFieldKnown(t *testing.T) {
	defer wt5Guard(t)
	a, _ := newWT5Adapter(t, "http://127.0.0.1:1", wt5Meta(true))
	want := source.Writes{Transition: true, Assign: true, Flag: true, Comment: true}
	if got := a.Capabilities().Writes; got != want {
		t.Errorf("Writes = %+v, want %+v", got, want)
	}
}

func TestCapabilities_Writes_FlagFalseWithoutFlaggedField(t *testing.T) {
	defer wt5Guard(t)
	a, _ := newWT5Adapter(t, "http://127.0.0.1:1", wt5Meta(false))
	want := source.Writes{Transition: true, Assign: true, Flag: false, Comment: true}
	if got := a.Capabilities().Writes; got != want {
		t.Errorf("Writes = %+v, want %+v", got, want)
	}
}

func TestSetFlag_NoFlaggedField_UnsupportedWithoutRequest(t *testing.T) {
	defer wt5Guard(t)
	m := newWT5Mock(t, wt5MockCfg{})
	a, rec := newWT5Adapter(t, m.URL, wt5Meta(false))
	for _, flagged := range []bool{true, false} {
		err := a.SetFlag(context.Background(), "10001", flagged)
		if !source.IsUnsupported(err) {
			t.Errorf("SetFlag(%v) err = %v, want unsupported", flagged, err)
		}
	}
	if n := len(rec.all()); n != 0 {
		t.Errorf("%d requests sent, want 0: %+v", n, rec.all())
	}
	// The other writes are unaffected: Assign still goes out (this also keeps the test red while writes are stubbed).
	if err := a.Assign(context.Background(), "10001", "u-bob"); err != nil {
		t.Fatalf("Assign without a Flagged field: %v", err)
	}
	if n := len(rec.writes()); n != 1 {
		t.Errorf("%d writes after Assign, want 1", n)
	}
}

// ── Transitions (list) ───────────────────────────────────────────────────────────────────────────

func TestTransitions_RuleMatrix_NeedsScreenAndDroppedUnavailable(t *testing.T) {
	defer wt5Guard(t)
	a, m, rec := wt5MockAdapter(t, wt5MockCfg{Transitions: wt5MatrixTransitions})
	got, err := a.Transitions(context.Background(), "10001")
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	if !reflect.DeepEqual(got, wt5MatrixWant) {
		t.Errorf("Transitions:\n got %+v\nwant %+v", got, wt5MatrixWant)
	}
	gets := rec.gets()
	if len(gets) != 1 || gets[0].Path != "/rest/api/3/issue/10001/transitions" {
		t.Fatalf("requests = %+v, want exactly one GET /rest/api/3/issue/10001/transitions", rec.all())
	}
	if q := gets[0].RawQuery; q != "expand=transitions.fields" {
		t.Errorf("query = %q, want expand=transitions.fields", q)
	}
	if len(m.writes()) != 0 {
		t.Errorf("Transitions sent a write: %+v", m.writes())
	}
}

func TestTransitions_AgainstFake_NamesToStatusesAndScreens(t *testing.T) {
	defer wt5Guard(t)
	srv, a, rec := wt5Fake(t, jiratest.Options{})
	is := wt5Pick(t, srv, "In Progress")
	got, err := a.Transitions(context.Background(), is.ID)
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	byTo := map[string]model.Transition{}
	ids := map[string]bool{}
	for _, tr := range got {
		if tr.ID == "" || tr.Name == "" || ids[tr.ID] {
			t.Errorf("transition %+v: want a non-empty, unique id and a name", tr)
		}
		ids[tr.ID] = true
		byTo[tr.ToStatusName] = tr
		if tr.ToStatusID == is.StatusID {
			t.Errorf("transition %+v goes to the current status", tr)
		}
		if want := wt5StatusID(srv, tr.ToStatusName); want == "" || want != tr.ToStatusID {
			t.Errorf("transition %+v: ToStatusID does not match the dataset status %q (%q)", tr, tr.ToStatusName, want)
		}
	}
	var names []string
	for n := range byTo {
		names = append(names, n)
	}
	sort.Strings(names)
	if want := []string{"Code Review", "Done", "QA", "To Do"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("transition targets = %v, want %v", names, want)
	}
	for name, wantScreen := range map[string]bool{"Done": true, "Code Review": false, "QA": false, "To Do": false} {
		if byTo[name].NeedsScreen != wantScreen {
			t.Errorf("transition to %s: NeedsScreen = %v, want %v", name, byTo[name].NeedsScreen, wantScreen)
		}
	}
	if gets := rec.gets(); len(gets) != 1 || len(rec.writes()) != 0 {
		t.Errorf("requests = %+v, want one GET and no write", rec.all())
	}
}

func TestTransitions_Errors_MapLikeOtherCalls(t *testing.T) {
	defer wt5Guard(t)
	for status, code := range map[int]apperr.Code{401: apperr.AuthInvalid, 404: apperr.NotFound} {
		a, _, _ := wt5MockAdapter(t, wt5MockCfg{GetStatus: status})
		_, err := a.Transitions(context.Background(), "10001")
		wt5Code(t, err, code)
	}
}

// ── Transition (perform) ─────────────────────────────────────────────────────────────────────────

func TestTransition_RuleMatrix_PostsOnlyWhenNoScreenNeeded(t *testing.T) {
	defer wt5Guard(t)
	for _, want := range wt5MatrixWant {
		t.Run(want.Name, func(t *testing.T) {
			defer wt5Guard(t)
			a, m, rec := wt5MockAdapter(t, wt5MockCfg{Transitions: wt5MatrixTransitions})
			err := a.Transition(context.Background(), "10001", want.ID)
			if want.NeedsScreen {
				wt5Code(t, err, apperr.TransitionNeedsScreen)
				if w := m.writes(); len(w) != 0 {
					t.Errorf("a needs-screen transition was sent: %+v", w)
				}
				return
			}
			if err != nil {
				t.Fatalf("Transition: %v", err)
			}
			w := m.writes()
			if len(w) != 1 || w[0].Method != http.MethodPost || w[0].Path != "/rest/api/3/issue/10001/transitions" {
				t.Fatalf("writes = %+v, want exactly one POST /rest/api/3/issue/10001/transitions", w)
			}
			if got, wantBody := wt5JSON(t, w[0].Body), wt5JSON(t, fmt.Sprintf(`{"transition":{"id":%q}}`, want.ID)); !reflect.DeepEqual(got, wantBody) {
				t.Errorf("body = %s, want %v", w[0].Body, wantBody)
			}
			if n := len(rec.gets()); n > 1 {
				t.Errorf("%d lookup GETs, want at most 1", n)
			}
		})
	}
}

func TestTransition_AgainstFake_AppliesStatusChangeAndChangelog(t *testing.T) {
	defer wt5Guard(t)
	srv, a, rec := wt5Fake(t, jiratest.Options{})
	is := wt5Pick(t, srv, "In Progress")
	for _, to := range []string{"QA", "Code Review"} { // a plain one, then an optional-only screen
		before := wt5Snap(srv, is.Key)
		list, err := a.Transitions(context.Background(), is.ID)
		if err != nil {
			t.Fatalf("Transitions: %v", err)
		}
		var tr model.Transition
		for _, c := range list {
			if c.ToStatusName == to {
				tr = c
			}
		}
		if tr.ID == "" {
			t.Fatalf("no transition to %q in %+v", to, list)
		}
		if err := a.Transition(context.Background(), is.ID, tr.ID); err != nil {
			t.Fatalf("Transition to %s: %v", to, err)
		}
		after := wt5Snap(srv, is.Key)
		if after.StatusID != tr.ToStatusID {
			t.Errorf("status after = %q, want %s (%q)", after.StatusID, to, tr.ToStatusID)
		}
		if after.Changes != before.Changes+1 {
			t.Errorf("changelog length = %d, want %d", after.Changes, before.Changes+1)
		}
		if c := wt5LastChange(srv, is.Key); c.FieldID != "status" || c.From != before.StatusID || c.To != tr.ToStatusID {
			t.Errorf("last change = %+v, want status %s -> %s", c, before.StatusID, tr.ToStatusID)
		}
	}
	posts := 0
	for _, w := range rec.writes() {
		if w.Method == http.MethodPost && w.Path == "/rest/api/3/issue/"+is.ID+"/transitions" {
			posts++
		}
	}
	if posts != 2 {
		t.Errorf("%d transition POSTs, want exactly 2 (one per Transition call)", posts)
	}
}

func TestTransition_NeedsScreenAgainstFake_RejectedWithoutSending(t *testing.T) {
	defer wt5Guard(t)
	for _, listFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("transitions_listed_first=%v", listFirst), func(t *testing.T) {
			defer wt5Guard(t)
			srv, a, rec := wt5Fake(t, jiratest.Options{})
			is := wt5Pick(t, srv, "In Progress")
			list, err := a.Transitions(context.Background(), is.ID)
			if err != nil {
				t.Fatalf("Transitions: %v", err)
			}
			doneID := ""
			for _, tr := range list {
				if tr.ToStatusName == "Done" {
					doneID = tr.ID
					if !tr.NeedsScreen {
						t.Fatalf("setup: the fake's Done transition must need a screen: %+v", tr)
					}
				}
			}
			a2, rec2 := a, rec
			if !listFirst {
				a2, rec2 = newWT5Adapter(t, srv.URL, wt5Meta(true)) // a fresh Adapter that never listed
			}
			err = a2.Transition(context.Background(), is.ID, doneID)
			wt5Code(t, err, apperr.TransitionNeedsScreen)
			if w := rec2.writes(); len(w) != 0 {
				t.Errorf("writes = %+v, want none", w)
			}
			for _, r := range srv.Requests() {
				if r.Method != http.MethodGet {
					t.Errorf("the fake saw %s %s, want only GETs", r.Method, r.Path)
				}
			}
			if after := wt5Snap(srv, is.Key); after != is {
				t.Errorf("dataset changed: before %+v after %+v", is, after)
			}
		})
	}
}

// 400 discrimination on POST transitions (SPEC-NOTE 400 discrimination).
func TestTransition_PostFieldLevel400_IsNeedsScreen(t *testing.T) {
	defer wt5Guard(t)
	a, m, _ := wt5MockAdapter(t, wt5MockCfg{Status: 400, Body: `{"errorMessages":[],"errors":{"resolution":"Resolution is required."}}`})
	err := a.Transition(context.Background(), "10001", "11")
	wt5Code(t, err, apperr.TransitionNeedsScreen)
	if n := len(m.writes()); n != 1 {
		t.Errorf("%d POSTs, want 1", n)
	}
}

func TestTransition_PostValidator400_IsWriteRejectedVerbatim(t *testing.T) {
	defer wt5Guard(t)
	const msg = "Cannot resolve: 2 sub-tasks are still open."
	a, m, _ := wt5MockAdapter(t, wt5MockCfg{Status: 400, Body: `{"errorMessages":["` + msg + `"],"errors":{}}`})
	err := a.Transition(context.Background(), "10001", "11")
	ae := wt5Code(t, err, apperr.WriteRejected)
	if ae.Message != msg {
		t.Errorf("Message = %q, want Jira's message verbatim %q", ae.Message, msg)
	}
	if n := len(m.writes()); n != 1 {
		t.Errorf("%d POSTs, want 1", n)
	}
}

// ── Assign ───────────────────────────────────────────────────────────────────────────────────────

func TestAssign_AgainstFake_UserThenUnassign(t *testing.T) {
	defer wt5Guard(t)
	srv, a, rec := wt5Fake(t, jiratest.Options{})
	is := wt5Pick(t, srv, "In Progress")
	target := ""
	srv.Mutate(func(ds *jiratest.Dataset) {
		for _, u := range ds.Users {
			if u.AccountID != is.Assignee {
				target = u.AccountID
				return
			}
		}
	})
	if err := a.Assign(context.Background(), is.ID, target); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if got := wt5Snap(srv, is.Key); got.Assignee != target || got.Changes != is.Changes+1 {
		t.Errorf("after assign: %+v, want assignee %q and one new change", got, target)
	}
	if c := wt5LastChange(srv, is.Key); c.FieldID != "assignee" || c.From != is.Assignee || c.To != target {
		t.Errorf("last change = %+v", c)
	}
	if err := a.Assign(context.Background(), is.ID, ""); err != nil {
		t.Fatalf("Assign(unassign): %v", err)
	}
	if got := wt5Snap(srv, is.Key); got.Assignee != "" || got.Changes != is.Changes+2 {
		t.Errorf("after unassign: %+v, want no assignee and two new changes", got)
	}
	if c := wt5LastChange(srv, is.Key); c.FieldID != "assignee" || c.From != target || c.To != "" {
		t.Errorf("last change = %+v", c)
	}

	w := rec.writes()
	if len(w) != 2 {
		t.Fatalf("writes = %+v, want exactly two PUTs", w)
	}
	for i, wantBody := range []string{`{"accountId":"` + target + `"}`, `{"accountId":null}`} {
		if w[i].Method != http.MethodPut || w[i].Path != "/rest/api/3/issue/"+is.ID+"/assignee" {
			t.Errorf("write %d = %s %s, want PUT /rest/api/3/issue/%s/assignee", i, w[i].Method, w[i].Path, is.ID)
		}
		if got := wt5JSON(t, w[i].Body); !reflect.DeepEqual(got, wt5JSON(t, wantBody)) {
			t.Errorf("write %d body = %s, want %s", i, w[i].Body, wantBody)
		}
	}
}

func TestWrites_RequestCarriesAuthAndJSONContentType(t *testing.T) {
	defer wt5Guard(t)
	a, _, rec := wt5MockAdapter(t, wt5MockCfg{})
	if err := a.Assign(context.Background(), "10001", "u-bob"); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	w := rec.writes()
	if len(w) != 1 {
		t.Fatalf("writes = %+v", w)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(wt5Account+":"+jiratest.Token))
	if got := w[0].Header.Get("Authorization"); got != wantAuth {
		t.Errorf("Authorization = %q, want Basic auth of account:token", got)
	}
	if got := w[0].Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

// ── SetFlag ──────────────────────────────────────────────────────────────────────────────────────

func TestSetFlag_AgainstFake_FlagThenUnflag(t *testing.T) {
	defer wt5Guard(t)
	srv, a, rec := wt5Fake(t, jiratest.Options{})
	is := wt5Pick(t, srv, "In Progress")
	if err := a.SetFlag(context.Background(), is.ID, true); err != nil {
		t.Fatalf("SetFlag(true): %v", err)
	}
	if got := wt5Snap(srv, is.Key); !got.Flagged || got.Changes != is.Changes+1 {
		t.Errorf("after flag: %+v, want flagged with one new change", got)
	}
	if c := wt5LastChange(srv, is.Key); c.FieldID != jiratest.FlaggedFieldID || c.From != "" || c.To != "Impediment" {
		t.Errorf("last change = %+v, want Flagged \"\" -> Impediment", c)
	}
	if err := a.SetFlag(context.Background(), is.ID, false); err != nil {
		t.Fatalf("SetFlag(false): %v", err)
	}
	if got := wt5Snap(srv, is.Key); got.Flagged || got.Changes != is.Changes+2 {
		t.Errorf("after unflag: %+v, want unflagged with two new changes", got)
	}
	if c := wt5LastChange(srv, is.Key); c.FieldID != jiratest.FlaggedFieldID || c.From != "Impediment" || c.To != "" {
		t.Errorf("last change = %+v, want Flagged Impediment -> \"\"", c)
	}

	w := rec.writes()
	if len(w) != 2 {
		t.Fatalf("writes = %+v, want exactly two PUTs", w)
	}
	for i, wantBody := range []string{
		`{"fields":{"` + wt5Flagged + `":[{"value":"Impediment"}]}}`,
		`{"fields":{"` + wt5Flagged + `":null}}`,
	} {
		if w[i].Method != http.MethodPut || w[i].Path != "/rest/api/3/issue/"+is.ID {
			t.Errorf("write %d = %s %s, want PUT /rest/api/3/issue/%s", i, w[i].Method, w[i].Path, is.ID)
		}
		if got := wt5JSON(t, w[i].Body); !reflect.DeepEqual(got, wt5JSON(t, wantBody)) {
			t.Errorf("write %d body = %s, want %s", i, w[i].Body, wantBody)
		}
	}
}

// ── AddComment ───────────────────────────────────────────────────────────────────────────────────

func TestAddComment_AgainstFake_BodyIsAdfFromPlainText(t *testing.T) {
	defer wt5Guard(t)
	cases := []struct {
		name string
		c    model.NewComment
	}{
		{"plain text", model.NewComment{Text: "Looks good to me"}},
		{"paragraphs, line break and a mention", model.NewComment{
			Text:     "Thanks @Ann Archer\n\nSecond paragraph, line one\nline two",
			Mentions: []model.Mention{{Start: 7, End: 18, AccountID: "u-iris", Name: "Ann Archer"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer wt5Guard(t)
			srv, a, rec := wt5Fake(t, jiratest.Options{})
			is := wt5Pick(t, srv, "In Progress")
			id, err := a.AddComment(context.Background(), is.ID, tc.c)
			if err != nil {
				t.Fatalf("AddComment: %v", err)
			}
			var last jiratest.DSComment
			n := 0
			srv.Mutate(func(ds *jiratest.Dataset) {
				for _, i := range ds.Issues {
					if i.Key == is.Key {
						n = len(i.Comments)
						last = i.Comments[n-1]
					}
				}
			})
			if n != is.Comments+1 {
				t.Errorf("comment count = %d, want %d", n, is.Comments+1)
			}
			if id == "" || id != last.ID {
				t.Errorf("returned id = %q, want the new comment's id %q", id, last.ID)
			}

			w := rec.writes()
			if len(w) != 1 || w[0].Method != http.MethodPost || w[0].Path != "/rest/api/3/issue/"+is.ID+"/comment" {
				t.Fatalf("writes = %+v, want exactly one POST /rest/api/3/issue/%s/comment", w, is.ID)
			}
			var top map[string]json.RawMessage
			if err := json.Unmarshal([]byte(w[0].Body), &top); err != nil {
				t.Fatalf("request body is not a JSON object: %v (%q)", err, w[0].Body)
			}
			keys := make([]string, 0, len(top))
			for k := range top {
				keys = append(keys, k)
			}
			if !reflect.DeepEqual(keys, []string{"body"}) {
				t.Fatalf("request keys = %v, want only [body] (no visibility restriction is ever set)", keys)
			}
			want := adf.FromPlainText(tc.c)
			if want.IsEmpty() {
				t.Fatalf("adf.FromPlainText returned an empty document: WP-T1 (internal/adf) must be merged before this test can pass")
			}
			var wantV any
			if err := json.Unmarshal(want.Raw, &wantV); err != nil {
				t.Fatalf("FromPlainText Raw is not JSON: %v", err)
			}
			if got := wt5JSON(t, string(top["body"])); !reflect.DeepEqual(got, wantV) {
				t.Errorf("request body.body = %s\nwant adf.FromPlainText = %s", top["body"], want.Raw)
			}
		})
	}
}

// ── error mapping ────────────────────────────────────────────────────────────────────────────────

func TestWrites_StatusCodesMapAndAreNeverRetried(t *testing.T) {
	defer wt5Guard(t)
	cases := []struct {
		status int
		want   apperr.Code
	}{
		{401, apperr.AuthInvalid},
		{403, apperr.PermissionDenied},
		{404, apperr.NotFound},
		{409, apperr.Conflict},
		{429, apperr.RateLimited},
		{500, apperr.SourceHTTP},
		{502, apperr.SourceHTTP},
		{503, apperr.SourceHTTP},
		{504, apperr.SourceHTTP},
	}
	const rawBody = `{"errorMessages":["RAW-BODY-MARKER"]}`
	for _, tc := range cases {
		for _, act := range wt5Actions() {
			t.Run(fmt.Sprintf("%d/%s", tc.status, act.name), func(t *testing.T) {
				defer wt5Guard(t)
				a, m, _ := wt5MockAdapter(t, wt5MockCfg{Status: tc.status, Body: rawBody})
				err := act.run(context.Background(), a)
				ae := wt5Code(t, err, tc.want)
				for _, bad := range []string{"RAW-BODY-MARKER", jiratest.Token} {
					if strings.Contains(ae.Message, bad) || strings.Contains(ae.Error(), bad) {
						t.Errorf("user-facing text %q leaks %q", ae.Error(), bad)
					}
				}
				if n := len(m.writes()); n != 1 {
					t.Errorf("%d write requests reached the server, want exactly 1 (writes are never retried)", n)
				}
			})
		}
	}
}

func TestWrites_StatusCodesAgainstFakeFaults_OneRequestAndDatasetUntouched(t *testing.T) {
	defer wt5Guard(t)
	cases := []struct {
		code int
		want apperr.Code
	}{
		{400, apperr.WriteRejected}, {401, apperr.AuthInvalid}, {403, apperr.PermissionDenied}, {404, apperr.NotFound},
		{409, apperr.Conflict}, {429, apperr.RateLimited}, {500, apperr.SourceHTTP}, {502, apperr.SourceHTTP},
		{503, apperr.SourceHTTP}, {504, apperr.SourceHTTP},
	}
	acts := map[string]func(ctx context.Context, a *Adapter, id string) error{
		"assign": func(ctx context.Context, a *Adapter, id string) error { return a.Assign(ctx, id, "") },
		"flag":   func(ctx context.Context, a *Adapter, id string) error { return a.SetFlag(ctx, id, true) },
		"comment": func(ctx context.Context, a *Adapter, id string) error {
			_, err := a.AddComment(ctx, id, model.NewComment{Text: "x"})
			return err
		},
	}
	for _, tc := range cases {
		for name, run := range acts {
			t.Run(fmt.Sprintf("%d/%s", tc.code, name), func(t *testing.T) {
				defer wt5Guard(t)
				srv, a, _ := wt5Fake(t, jiratest.Options{Fail: []jiratest.FailStatus{{PathPrefix: "/rest/api/3/issue/", Code: tc.code}}})
				is := wt5Pick(t, srv, "In Progress")
				srv.ResetRequests()
				err := run(context.Background(), a, is.ID)
				ae := wt5Code(t, err, tc.want)
				if tc.code == 400 && ae.Message != "Injected failure." {
					t.Errorf("400 Message = %q, want the fake's errorMessages[0] verbatim (\"Injected failure.\")", ae.Message)
				}
				if reqs := srv.Requests(); len(reqs) != 1 {
					t.Errorf("fake saw %d requests, want exactly 1 (no retry): %+v", len(reqs), reqs)
				}
				if after := wt5Snap(srv, is.Key); after != is {
					t.Errorf("dataset changed by a failed write: %+v -> %+v", is, after)
				}
			})
		}
	}
}

func TestWrites_400_WriteRejectedCarriesJirasFirstMessageVerbatim(t *testing.T) {
	defer wt5Guard(t)
	cases := []struct{ name, body, want string }{
		{"errorMessages", `{"errorMessages":["Cannot assign: the user is on leave.","second"],"errors":{}}`, "Cannot assign: the user is on leave."},
		{"errors only", `{"errorMessages":[],"errors":{"customfield_10021":"Field cannot be set."}}`, "Field cannot be set."},
		{"both, messages first", `{"errorMessages":["General failure."],"errors":{"a":"Field a."}}`, "General failure."},
	}
	for _, tc := range cases {
		for _, act := range wt5Actions() {
			if act.name == "transition" {
				continue // POST transitions has its own 400 rule (field-level -> needs screen)
			}
			t.Run(tc.name+"/"+act.name, func(t *testing.T) {
				defer wt5Guard(t)
				a, m, _ := wt5MockAdapter(t, wt5MockCfg{Status: 400, Body: tc.body})
				err := act.run(context.Background(), a)
				ae := wt5Code(t, err, apperr.WriteRejected)
				if ae.Message != tc.want {
					t.Errorf("Message = %q, want %q", ae.Message, tc.want)
				}
				if n := len(m.writes()); n != 1 {
					t.Errorf("%d write requests, want 1", n)
				}
			})
		}
	}
}

func TestWrites_429_RetryInMsFromRetryAfter(t *testing.T) {
	defer wt5Guard(t)
	cases := []struct {
		name   string
		header map[string]string
		want   int64
	}{
		{"Retry-After seconds", map[string]string{"Retry-After": "7"}, 7000},
		{"Retry-After zero", map[string]string{"Retry-After": "0"}, 0},
		{"X-RateLimit-Reset when no Retry-After", map[string]string{"X-RateLimit-Reset": wt5Now.Add(12 * time.Second).Format(time.RFC3339)}, 12000},
		{"neither header: 5 s fallback", nil, 5000},
	}
	for _, tc := range cases {
		for _, act := range wt5Actions() {
			t.Run(tc.name+"/"+act.name, func(t *testing.T) {
				defer wt5Guard(t)
				a, m, _ := wt5MockAdapter(t, wt5MockCfg{Status: 429, Header: tc.header, Body: `{"errorMessages":["Rate limit exceeded."]}`})
				err := act.run(context.Background(), a)
				ae := wt5Code(t, err, apperr.RateLimited)
				if ae.RetryInMs != tc.want {
					t.Errorf("RetryInMs = %d, want %d", ae.RetryInMs, tc.want)
				}
				if n := len(m.writes()); n != 1 {
					t.Errorf("%d write requests, want exactly 1", n)
				}
			})
		}
	}
}

func TestWrites_429FromFakeThrottle_CarriesRetryAfterAndIsNotRetried(t *testing.T) {
	defer wt5Guard(t)
	srv, a, _ := wt5Fake(t, jiratest.Options{Throttle: &jiratest.Throttle{Every: 1, RetryAfter: 7 * time.Second}})
	is := wt5Pick(t, srv, "In Progress")
	srv.ResetRequests()
	err := a.Assign(context.Background(), is.ID, "")
	ae := wt5Code(t, err, apperr.RateLimited)
	if ae.RetryInMs != 7000 {
		t.Errorf("RetryInMs = %d, want 7000", ae.RetryInMs)
	}
	if reqs := srv.Requests(); len(reqs) != 1 {
		t.Errorf("fake saw %d requests, want exactly 1: %+v", len(reqs), reqs)
	}
	if after := wt5Snap(srv, is.Key); after != is {
		t.Errorf("dataset changed: %+v -> %+v", is, after)
	}
}

func TestWrites_ServerDown_Offline(t *testing.T) {
	defer wt5Guard(t)
	for _, act := range wt5Actions() {
		t.Run(act.name, func(t *testing.T) {
			defer wt5Guard(t)
			m := newWT5Mock(t, wt5MockCfg{})
			a, _ := newWT5Adapter(t, m.URL, wt5Meta(true))
			m.Server.Close()
			wt5Code(t, act.run(context.Background(), a), apperr.Offline)
		})
	}
}

func TestWrites_ConnectionDroppedAfterSend_TimeoutAndNotRetried(t *testing.T) {
	defer wt5Guard(t)
	for _, act := range wt5Actions() {
		t.Run(act.name, func(t *testing.T) {
			defer wt5Guard(t)
			a, m, _ := wt5MockAdapter(t, wt5MockCfg{Drop: true})
			wt5Code(t, act.run(context.Background(), a), apperr.Timeout)
			if n := len(m.writes()); n != 1 {
				t.Errorf("%d write requests reached the server, want exactly 1 (a possibly-landed write is never re-sent)", n)
			}
		})
	}
}
