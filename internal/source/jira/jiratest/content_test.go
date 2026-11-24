package jiratest

// WP-T4 tests for the fake Jira's content and media endpoints (jira-sync.md §6 "Content and writes (ADR 0015)",
// §2.8 `*all` projection, §2.10 comments, §2.14 attachments and media). Black-box HTTP only, over the real
// DefaultFixture, because the content fields of Dataset do not exist yet (WP-T4 adds them); the writes half of the
// ADR 0015 paragraph (transitions, assignee, flag, add comment) belongs to WP-T5 and is not tested here.
//
// The content itself is test data: testdata/content.json (written with these tests), loaded by the fake over the
// generated fixture. Its shape is pinned by TestContentFixture_ShapeAndCoverage (strict decode), so WP-T4 implements
// the loader against exactly these keys. Media files are testdata/media/*; sizes and bytes served come from disk.
//
// SPEC-NOTE (overlay, not fixture.json): content lives in testdata/content.json, NOT in fixture.json, because
// cmd/fixturegen regenerates fixture.json from design/reference/sample-data.json and its drift test
// (TestConvert_DefaultFixtureMatchesConvertedSample) compares the whole file. The fake applies the overlay by issue
// key for any fixture (the tiny test fixtures have no matching keys and get generated content only).
// SPEC-NOTE (generated content): every issue WITHOUT an overlay entry still carries a non-empty ADF `description`
// (type "doc", version 1, at least one block), `attachment: []`, `issuelinks: []`, `subtasks: []`, `watches`, and every
// comment it has carries an ADF body (type "doc") with no visibility and updated == created.
// SPEC-NOTE (*all semantics): a projection of ["*all"] returns every field the fake knows including `comment`;
// ["*all","-comment","-worklog"] drops those two keys; an explicit list (the pre-F12 hydrate) still returns only the
// listed fields. GET /issue/{id}?fields=… parses the same list comma-separated.
// SPEC-NOTE (redirect): GET /attachment/content/{id} answers 303 with a Location on /media/file/<uuid>/binary of the
// same server (the uuid is the overlay's mediaFileId). The media URL is pre-signed: it answers WITHOUT any
// Authorization header. Unknown attachment id → 404; unknown media uuid → 404; the content endpoint itself needs auth.
// SPEC-NOTE (thumbnail): GET /attachment/thumbnail/{id} serves a decodable image that fits inside the requested
// width×height (default 320×200); an attachment with no thumbnail (the PDF) answers 404 when fallbackToDefault=false.
// SPEC-NOTE (untested gap): the fault modes Options.Media / Writes / RefreshFails do not exist in the frozen Options
// yet; tests for them arrive with WP-T4/T5 once the option names exist.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// ── overlay file (pinned shape) ─────────────────────────────────────────────────────────────────

type f12Overlay struct {
	Doc       string                     `json:"_doc"`
	FieldDefs []json.RawMessage          `json:"fieldDefs"`
	Issues    map[string]f12OverlayIssue `json:"issues"`
}

type f12OverlayIssue struct {
	Fields         map[string]json.RawMessage `json:"fields"`
	DateFields     map[string]int             `json:"dateFields"`
	DateTimeFields map[string]int             `json:"dateTimeFields"`
	Attachments    []f12OverlayAtt            `json:"attachments"`
	Comments       []f12OverlayComment        `json:"comments"`
	Links          []f12OverlayLink           `json:"links"`
	Subtasks       []string                   `json:"subtasks"`
}

type f12OverlayAtt struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	MimeType    string `json:"mimeType"`
	Author      string `json:"author"`
	Day         int    `json:"day"`
	File        string `json:"file"`
	MediaFileID string `json:"mediaFileId"`
}

type f12OverlayComment struct {
	Body       json.RawMessage `json:"body"`
	Visibility *struct {
		Type       string `json:"type"`
		Value      string `json:"value"`
		Identifier string `json:"identifier"`
	} `json:"visibility"`
	EditedDay *int `json:"editedDay"`
}

type f12OverlayLink struct {
	ID   string `json:"id"`
	Type struct {
		Name    string `json:"name"`
		Inward  string `json:"inward"`
		Outward string `json:"outward"`
	} `json:"type"`
	Direction string `json:"direction"`
	Key       string `json:"key"`
}

func f12LoadOverlay(t *testing.T) *f12Overlay {
	t.Helper()
	b, err := os.ReadFile("testdata/content.json")
	if err != nil {
		t.Fatalf("read testdata/content.json: %v", err)
	}
	var ov f12Overlay
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ov); err != nil {
		t.Fatalf("testdata/content.json does not match the pinned shape: %v", err)
	}
	return &ov
}

// f12Server starts the fake over the real DefaultFixture with a fixed clock.
func f12Server(t *testing.T, opts Options) *Server {
	t.Helper()
	fx, err := ParseFixture(DefaultFixture)
	if err != nil {
		t.Fatalf("ParseFixture(DefaultFixture): %v", err)
	}
	opts.Now = testNow
	opts.Loc = nyc
	return NewServer(t, fx, opts)
}

func f12SameJSON(got any, want json.RawMessage) bool {
	var w any
	if err := json.Unmarshal(want, &w); err != nil {
		return false
	}
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(w)
	return bytes.Equal(gb, wb) // both sides marshal maps with sorted keys
}

func f12MediaFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "media", name))
	if err != nil {
		t.Fatalf("read media file %s: %v", name, err)
	}
	return b
}

// f12Walk visits every JSON object of v.
func f12Walk(v any, fn func(m map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		fn(x)
		for _, c := range x {
			f12Walk(c, fn)
		}
	case []any:
		for _, c := range x {
			f12Walk(c, fn)
		}
	}
}

func f12Decode(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

func f12IsADFDoc(v any) bool {
	return jstr(jget(v, "type")) == "doc" && jnum(jget(v, "version")) == 1 && len(jarr(jget(v, "content"))) > 0
}

// noRedirect is a client that surfaces 3xx responses.
var f12NoRedirect = &http.Client{
	Timeout:       10 * time.Second,
	Transport:     &http.Transport{DisableKeepAlives: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func f12Do(t *testing.T, method, rawURL, auth string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := f12NoRedirect.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// ── the fixture file itself ─────────────────────────────────────────────────────────────────────

func TestContentFixture_ShapeAndCoverage(t *testing.T) {
	defer guard(t)
	ov := f12LoadOverlay(t)
	mob, ok := ov.Issues["MOB-1421"]
	if !ok {
		t.Fatal("content.json has no MOB-1421")
	}
	fx, err := ParseFixture(DefaultFixture)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]FxIssue{}
	for _, i := range fx.Issues {
		keys[i.Key] = i
	}
	fi, ok := keys["MOB-1421"]
	if !ok {
		t.Fatal("DefaultFixture has no MOB-1421")
	}
	if len(mob.Comments) != len(fi.Comments) {
		t.Errorf("overlay comments = %d, fixture comments of MOB-1421 = %d: they are aligned by index and must match", len(mob.Comments), len(fi.Comments))
	}
	for _, l := range mob.Links {
		if _, ok := keys[l.Key]; !ok {
			t.Errorf("link target %s is not a fixture issue", l.Key)
		}
	}
	for _, k := range mob.Subtasks {
		if _, ok := keys[k]; !ok {
			t.Errorf("subtask %s is not a fixture issue", k)
		}
	}
	images, pdfs := 0, 0
	for _, a := range mob.Attachments {
		if _, err := os.Stat(filepath.Join("testdata", "media", a.File)); err != nil {
			t.Errorf("attachment %s: media file %s missing: %v", a.ID, a.File, err)
		}
		switch {
		case strings.HasPrefix(a.MimeType, "image/"):
			images++
		case a.MimeType == "application/pdf":
			pdfs++
		}
	}
	if images != 3 || pdfs != 1 {
		t.Errorf("attachments: %d images + %d pdf, want 3 + 1", images, pdfs)
	}

	// every ADF node and mark of ux-spec §14.4, plus one unknown node
	desc := f12Decode(t, mob.Fields["description"])
	nodes, marks, panels := map[string]bool{}, map[string]bool{}, map[string]bool{}
	f12Walk(desc, func(m map[string]any) {
		if ty := jstr(m["type"]); ty != "" {
			nodes[ty] = true
			if ty == "panel" {
				panels[jstr(jget(m, "attrs", "panelType"))] = true
			}
		}
	})
	for _, n := range []string{"heading", "paragraph", "text", "hardBreak", "codeBlock", "blockquote", "rule", "bulletList",
		"orderedList", "listItem", "taskList", "taskItem", "decisionList", "decisionItem", "panel", "table", "tableRow",
		"tableHeader", "tableCell", "expand", "nestedExpand", "status", "mention", "date", "emoji", "inlineCard",
		"blockCard", "mediaSingle", "media", "mediaGroup", "mediaInline", "bodiedExtension"} {
		if !nodes[n] {
			t.Errorf("MOB-1421 description lacks ADF node %q", n)
		}
	}
	for _, p := range []string{"info", "note", "success", "warning", "error"} {
		if !panels[p] {
			t.Errorf("MOB-1421 description lacks panelType %q", p)
		}
	}
	var walkMarks func(v any)
	walkMarks = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, mk := range jarr(x["marks"]) {
				marks[jstr(jget(mk, "type"))] = true
			}
			for _, c := range x {
				walkMarks(c)
			}
		case []any:
			for _, c := range x {
				walkMarks(c)
			}
		}
	}
	walkMarks(desc)
	for _, m := range []string{"strong", "em", "strike", "underline", "subsup", "textColor", "link", "code"} {
		if !marks[m] {
			t.Errorf("MOB-1421 description lacks mark %q", m)
		}
	}
	// one unknown app field and several empty ones
	var unknown, empty int
	for _, raw := range ov.FieldDefs {
		var d struct {
			ID     string `json:"id"`
			Custom bool   `json:"custom"`
			Schema struct {
				Custom string `json:"custom"`
				Type   string `json:"type"`
			} `json:"schema"`
		}
		_ = json.Unmarshal(raw, &d)
		if strings.HasPrefix(d.Schema.Custom, "com.tempoplugin") {
			unknown++
		}
		if v, present := mob.Fields[d.ID]; d.Custom && present {
			s := strings.TrimSpace(string(v))
			if s == "null" || s == "[]" || s == `""` {
				empty++
			}
		}
	}
	if unknown != 1 || empty < 3 {
		t.Errorf("fieldDefs: %d unknown app field(s), %d empty value(s), want 1 and ≥ 3", unknown, empty)
	}
}

// ── GET /rest/api/3/field ───────────────────────────────────────────────────────────────────────

func TestField_ServesTheContentRegistryAndKeepsTheBuiltins(t *testing.T) {
	defer guard(t)
	ov := f12LoadOverlay(t)
	s := f12Server(t, Options{})
	code, v := get(t, s, "/rest/api/3/field")
	mustOK(t, code, v, "field")
	byID := map[string]any{}
	for _, f := range jarr(v) {
		byID[jstr(jget(f, "id"))] = f
	}
	for _, id := range []string{SprintFieldID, RankFieldID, FlaggedFieldID} {
		if byID[id] == nil {
			t.Errorf("built-in field %s missing", id)
		}
	}
	if len(ov.FieldDefs) == 0 {
		t.Fatal("content.json has no fieldDefs")
	}
	for _, raw := range ov.FieldDefs {
		want := f12Decode(t, raw)
		id := jstr(jget(want, "id"))
		got := byID[id]
		if got == nil {
			t.Errorf("field %s from content.json is not served by GET /field", id)
			continue
		}
		if jstr(jget(got, "name")) != jstr(jget(want, "name")) || jget(got, "custom") != jget(want, "custom") {
			t.Errorf("field %s: name/custom = %v/%v, want %v/%v", id, jget(got, "name"), jget(got, "custom"), jget(want, "name"), jget(want, "custom"))
		}
		gs, _ := json.Marshal(jget(got, "schema"))
		ws, _ := json.Marshal(jget(want, "schema"))
		if !bytes.Equal(gs, ws) {
			t.Errorf("field %s: schema = %s, want %s", id, gs, ws)
		}
	}
}

// ── GET /rest/api/3/issue/{id} ──────────────────────────────────────────────────────────────────

const f12AllNoComments = "*all,-comment,-worklog"

func TestIssueGet_MOB1421CarriesEveryContentFieldVerbatim(t *testing.T) {
	defer guard(t)
	ov := f12LoadOverlay(t)
	mob := ov.Issues["MOB-1421"]
	s := f12Server(t, Options{})
	id := issueID(t, s, "MOB-1421")
	code, v := get(t, s, "/rest/api/3/issue/"+id+"?fields="+url.QueryEscape(f12AllNoComments))
	mustOK(t, code, v, "GET issue")
	if jstr(jget(v, "id")) != id || jstr(jget(v, "key")) != "MOB-1421" {
		t.Fatalf("id/key = %v/%v, want %s/MOB-1421", jget(v, "id"), jget(v, "key"), id)
	}
	fields := jget(v, "fields")
	for _, base := range []string{"summary", "issuetype", "status", "assignee", "created", "updated", SprintFieldID, RankFieldID, FlaggedFieldID} {
		if !hasKey(fields, base) {
			t.Errorf("fields lack %q under *all", base)
		}
	}
	if hasKey(fields, "comment") || hasKey(fields, "worklog") {
		t.Errorf("fields carry comment/worklog although they were excluded: %v", f12Keys(fields))
	}
	materialised := map[string]bool{}
	for k := range mob.DateFields {
		materialised[k] = true
	}
	for k := range mob.DateTimeFields {
		materialised[k] = true
	}
	for k, raw := range mob.Fields {
		if materialised[k] {
			continue
		}
		got, present := jget(fields, k), hasKey(fields, k)
		if strings.TrimSpace(string(raw)) == "null" {
			// an empty field is served as null (or left out): both mean "not set" to the adapter
			if present && got != nil {
				t.Errorf("empty field %s = %v, want null or absent", k, got)
			}
			continue
		}
		if !present {
			t.Errorf("field %s missing from the issue", k)
			continue
		}
		if !f12SameJSON(got, raw) {
			gb, _ := json.Marshal(got)
			t.Errorf("field %s = %s, want verbatim %s", k, gb, raw)
		}
	}
	for k, day := range mob.DateFields {
		if got, want := jstr(jget(fields, k)), dayStr(testNow, nyc, day); got != want {
			t.Errorf("date field %s = %q, want %q (day %+d)", k, got, want, day)
		}
	}
	for k, day := range mob.DateTimeFields {
		at := parseJiraTime(t, jget(fields, k)).In(nyc)
		if at.Format("2006-01-02") != dayStr(testNow, nyc, day) || at.Hour() != 9 || at.Minute() != 30 {
			t.Errorf("datetime field %s = %v, want %s 09:30 local", k, at, dayStr(testNow, nyc, day))
		}
	}
	if jnum(jget(fields, "watches", "watchCount")) != 3 {
		t.Errorf("watches = %v, want watchCount 3", jget(fields, "watches"))
	}
}

func f12Keys(v any) []string {
	m, _ := v.(map[string]any)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestIssueGet_AcceptsAKeyAndHonoursAnExplicitFieldList(t *testing.T) {
	defer guard(t)
	s := f12Server(t, Options{})
	code, v := get(t, s, "/rest/api/3/issue/MOB-1421?fields=summary,description")
	mustOK(t, code, v, "GET issue by key")
	if got := f12Keys(jget(v, "fields")); strings.Join(got, ",") != "description,summary" {
		t.Errorf("explicit field list returned %v, want only description and summary", got)
	}
	code, v = get(t, s, "/rest/api/3/issue/MOB-1421?fields=*all")
	mustOK(t, code, v, "GET issue *all")
	if !hasKey(jget(v, "fields"), "comment") {
		t.Errorf("*all without -comment must include the comment field: %v", f12Keys(jget(v, "fields")))
	}
}

func TestIssueGet_UnknownIssueIs404AndAuthIsRequired(t *testing.T) {
	defer guard(t)
	s := f12Server(t, Options{})
	if code, _ := get(t, s, "/rest/api/3/issue/99999999?fields=*all"); code != http.StatusNotFound {
		t.Errorf("unknown issue status = %d, want 404", code)
	}
	id := issueID(t, s, "MOB-1421")
	for _, p := range []string{
		"/rest/api/3/issue/" + id,
		"/rest/api/3/attachment/content/20001",
		"/rest/api/3/attachment/thumbnail/20001",
	} {
		// precondition: the endpoint exists (otherwise "401" would only be the auth wall in front of a 404)
		if resp, _ := f12Do(t, http.MethodGet, s.URL+p, basicAuth(Token)); resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusSeeOther {
			t.Errorf("GET %s with credentials = %d, want 200 or 303", p, resp.StatusCode)
		}
		if code, _, _ := rawCall(t, s, http.MethodGet, p, nil, ""); code != http.StatusUnauthorized {
			t.Errorf("GET %s without credentials = %d, want 401", p, code)
		}
	}
}

func TestIssueGet_MOB1421AttachmentsLinksAndSubtasks(t *testing.T) {
	defer guard(t)
	ov := f12LoadOverlay(t)
	mob := ov.Issues["MOB-1421"]
	s := f12Server(t, Options{})
	id := issueID(t, s, "MOB-1421")
	code, v := get(t, s, "/rest/api/3/issue/"+id+"?fields="+url.QueryEscape(f12AllNoComments))
	mustOK(t, code, v, "GET issue")
	fields := jget(v, "fields")

	atts := jarr(jget(fields, "attachment"))
	if len(atts) != len(mob.Attachments) || len(atts) == 0 {
		t.Fatalf("attachments = %d, want %d", len(atts), len(mob.Attachments))
	}
	for i, w := range mob.Attachments {
		a := atts[i]
		if jstr(jget(a, "id")) != w.ID || jstr(jget(a, "filename")) != w.Filename || jstr(jget(a, "mimeType")) != w.MimeType {
			t.Errorf("attachment[%d] = %v, want %s %s %s", i, a, w.ID, w.Filename, w.MimeType)
		}
		if got, want := jnum(jget(a, "size")), len(f12MediaFile(t, w.File)); got != want {
			t.Errorf("attachment %s size = %d, want %d (the file on disk)", w.ID, got, want)
		}
		if jstr(jget(a, "author", "accountId")) != w.Author {
			t.Errorf("attachment %s author = %v, want %s", w.ID, jget(a, "author"), w.Author)
		}
		if got := parseJiraTime(t, jget(a, "created")).In(nyc).Format("2006-01-02"); got != dayStr(testNow, nyc, w.Day) {
			t.Errorf("attachment %s created = %s, want day %+d", w.ID, got, w.Day)
		}
		if c := jstr(jget(a, "content")); !strings.HasSuffix(c, "/rest/api/3/attachment/content/"+w.ID) || !strings.HasPrefix(c, s.URL) {
			t.Errorf("attachment %s content URL = %q, want %s/rest/api/3/attachment/content/%s", w.ID, c, s.URL, w.ID)
		}
		th := jstr(jget(a, "thumbnail"))
		if strings.HasPrefix(w.MimeType, "image/") && !strings.HasSuffix(th, "/rest/api/3/attachment/thumbnail/"+w.ID) {
			t.Errorf("image attachment %s thumbnail URL = %q", w.ID, th)
		}
		if w.MimeType == "application/pdf" && th != "" {
			t.Errorf("pdf attachment %s has a thumbnail URL %q, want none", w.ID, th)
		}
	}

	links := jarr(jget(fields, "issuelinks"))
	if len(links) != len(mob.Links) || len(links) == 0 {
		t.Fatalf("issuelinks = %d, want %d", len(links), len(mob.Links))
	}
	for i, w := range mob.Links {
		l := links[i]
		if jstr(jget(l, "id")) != w.ID || jstr(jget(l, "type", "name")) != w.Type.Name ||
			jstr(jget(l, "type", "inward")) != w.Type.Inward || jstr(jget(l, "type", "outward")) != w.Type.Outward {
			t.Errorf("issuelinks[%d] = %v, want id %s type %+v", i, l, w.ID, w.Type)
			continue
		}
		side, other := "inwardIssue", "outwardIssue"
		if w.Direction == "outward" {
			side, other = other, side
		}
		if hasKey(l, other) || !hasKey(l, side) {
			t.Errorf("issuelinks[%d] (%s) has %v, want only %s", i, w.Direction, f12Keys(l), side)
			continue
		}
		f12AssertRef(t, s, "issuelinks["+jstr(i)+"]."+side, jget(l, side), w.Key)
	}

	subs := jarr(jget(fields, "subtasks"))
	if len(subs) != len(mob.Subtasks) || len(subs) == 0 {
		t.Fatalf("subtasks = %d, want %d", len(subs), len(mob.Subtasks))
	}
	for i, key := range mob.Subtasks {
		f12AssertRef(t, s, "subtasks["+jstr(i)+"]", subs[i], key)
	}
}

// f12AssertRef checks an issue reference {id,key,fields:{summary,status:{id},issuetype:{id}}} against the dataset.
func f12AssertRef(t *testing.T, s *Server, what string, ref any, key string) {
	t.Helper()
	var want DSIssue
	withDS(s, func(ds *Dataset) {
		if is := issueByKey(ds, key); is != nil {
			want = *is
		}
	})
	if want.ID == "" {
		t.Fatalf("%s: %s is not in the dataset", what, key)
	}
	if jstr(jget(ref, "id")) != want.ID || jstr(jget(ref, "key")) != key ||
		jstr(jget(ref, "fields", "summary")) != want.Summary ||
		jstr(jget(ref, "fields", "status", "id")) != want.StatusID ||
		jstr(jget(ref, "fields", "issuetype", "id")) != want.TypeID {
		t.Errorf("%s = %v, want id %s key %s summary %q status %s type %s", what, ref, want.ID, key, want.Summary, want.StatusID, want.TypeID)
	}
}

func TestIssueGet_IssuesWithoutOverlayStillCarryContentKeys(t *testing.T) {
	defer guard(t)
	s := f12Server(t, Options{})
	code, v := get(t, s, "/rest/api/3/issue/MOB-1390?fields="+url.QueryEscape(f12AllNoComments))
	mustOK(t, code, v, "GET MOB-1390")
	fields := jget(v, "fields")
	if d := jget(fields, "description"); !f12IsADFDoc(d) {
		t.Errorf("MOB-1390 description = %v, want a non-empty ADF doc (every fixture issue carries one)", d)
	}
	for _, k := range []string{"attachment", "issuelinks", "subtasks"} {
		arr, ok := jget(fields, k).([]any)
		if !ok || len(arr) != 0 {
			t.Errorf("MOB-1390 %s = %v, want an empty array", k, jget(fields, k))
		}
	}
	if !hasKey(fields, "watches") {
		t.Errorf("MOB-1390 lacks watches: %v", f12Keys(fields))
	}
}

// ── search: the *all projection ─────────────────────────────────────────────────────────────────

func TestSearch_AllProjectionSemantics(t *testing.T) {
	defer guard(t)
	s := f12Server(t, Options{})
	id := issueID(t, s, "MOB-1421")
	jql := "id in (" + id + ")"
	one := func(fields []string) any {
		code, v := search(t, s, map[string]any{"jql": jql, "fields": fields, "maxResults": 50})
		mustOK(t, code, v, "search")
		if n := len(jarr(jget(v, "issues"))); n != 1 {
			t.Fatalf("fields %v: %d issues, want 1", fields, n)
		}
		return jget(v, "issues", 0, "fields")
	}
	f := one([]string{"*all", "-comment", "-worklog"})
	if !f12IsADFDoc(jget(f, "description")) || hasKey(f, "comment") || hasKey(f, "worklog") {
		t.Errorf("*all,-comment,-worklog → keys %v (description ADF, no comment/worklog expected)", f12Keys(f))
	}
	for _, k := range []string{SprintFieldID, RankFieldID, FlaggedFieldID, "customfield_10100", "attachment", "issuelinks", "subtasks", "labels", "fixVersions", "timetracking"} {
		if !hasKey(f, k) {
			t.Errorf("*all,-comment,-worklog lacks %q", k)
		}
	}
	if f := one([]string{"*all"}); !hasKey(f, "comment") || !hasKey(f, "description") {
		t.Errorf("*all → keys %v, want comment and description", f12Keys(f))
	}
	if f := one([]string{"summary", "updated"}); strings.Join(f12Keys(f), ",") != "summary,updated" {
		t.Errorf("explicit [summary updated] → keys %v, want only those", f12Keys(f))
	}
}

func TestSearch_EveryFixtureIssueCarriesAnAdfDescription(t *testing.T) {
	defer guard(t)
	s := f12Server(t, Options{})
	var ids []string
	withDS(s, func(ds *Dataset) {
		for _, is := range ds.Issues {
			ids = append(ids, is.ID)
		}
	})
	if len(ids) < 100 {
		t.Fatalf("dataset has %d issues, want the full fixture", len(ids))
	}
	seen := 0
	for start := 0; start < len(ids); start += 50 {
		end := min(start+50, len(ids))
		code, v := search(t, s, map[string]any{"jql": "id in (" + strings.Join(ids[start:end], ",") + ")",
			"fields": []string{"*all", "-comment", "-worklog"}, "maxResults": 50})
		mustOK(t, code, v, "search")
		for _, is := range jarr(jget(v, "issues")) {
			seen++
			if d := jget(is, "fields", "description"); !f12IsADFDoc(d) {
				t.Errorf("%s description = %v, want a non-empty ADF doc", jstr(jget(is, "key")), d)
			}
		}
	}
	if seen != len(ids) {
		t.Errorf("search returned %d issues, want %d", seen, len(ids))
	}
}

// ── comments: bodies, visibility, edited, paging ────────────────────────────────────────────────

func TestComments_MOB1421BodiesVisibilityAndEdited(t *testing.T) {
	defer guard(t)
	ov := f12LoadOverlay(t)
	mob := ov.Issues["MOB-1421"]
	s := f12Server(t, Options{})
	id := issueID(t, s, "MOB-1421")
	code, v := get(t, s, "/rest/api/3/issue/"+id+"/comment?orderBy=created&maxResults=100&startAt=0")
	mustOK(t, code, v, "comments")
	cs := jarr(jget(v, "comments"))
	if len(cs) != len(mob.Comments) || len(cs) == 0 || jnum(jget(v, "total")) != len(cs) {
		t.Fatalf("comments = %d (total %v), want %d", len(cs), jget(v, "total"), len(mob.Comments))
	}
	var prev time.Time
	for i, w := range mob.Comments {
		c := cs[i]
		created, updated := parseJiraTime(t, jget(c, "created")), parseJiraTime(t, jget(c, "updated"))
		if i > 0 && created.Before(prev) {
			t.Errorf("comment %d is older than comment %d: orderBy=created is oldest first", i, i-1)
		}
		prev = created
		if !f12SameJSON(jget(c, "body"), w.Body) {
			t.Errorf("comment %d body differs from content.json", i)
		}
		if jstr(jget(c, "id")) == "" || jstr(jget(c, "author", "accountId")) == "" || jstr(jget(c, "author", "accountType")) == "" {
			t.Errorf("comment %d lacks id/author.accountId/author.accountType: %v", i, c)
		}
		vis := jget(c, "visibility")
		if w.Visibility == nil {
			if vis != nil {
				t.Errorf("comment %d visibility = %v, want none", i, vis)
			}
		} else if jstr(jget(vis, "type")) != w.Visibility.Type || jstr(jget(vis, "value")) != w.Visibility.Value {
			t.Errorf("comment %d visibility = %v, want %s/%s", i, vis, w.Visibility.Type, w.Visibility.Value)
		}
		if w.EditedDay == nil {
			if !updated.Equal(created) {
				t.Errorf("comment %d updated = %v, want == created %v (not edited)", i, updated, created)
			}
		} else if !updated.After(created) || updated.In(nyc).Format("2006-01-02") != dayStr(testNow, nyc, *w.EditedDay) {
			t.Errorf("comment %d updated = %v, want after created on day %+d", i, updated, *w.EditedDay)
		}
	}
	// the day offsets of the comments are the fixture's: the overlay adds bodies, it does not move or add comments.
	fx, _ := ParseFixture(DefaultFixture)
	for _, fi := range fx.Issues {
		if fi.Key != "MOB-1421" {
			continue
		}
		days := append([]int(nil), fi.Comments...)
		sort.Ints(days)
		for i, d := range days {
			if got := parseJiraTime(t, jget(cs[i], "created")).In(nyc).Format("2006-01-02"); got != dayStr(testNow, nyc, d) {
				t.Errorf("comment %d created day = %s, want fixture day %+d (%s)", i, got, d, dayStr(testNow, nyc, d))
			}
		}
	}
}

func TestComments_IssuesWithoutOverlayGetGeneratedAdfBodies(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	id := issueID(t, s, "MOB-1")
	code, v := get(t, s, "/rest/api/3/issue/"+id+"/comment?orderBy=created&maxResults=100&startAt=0")
	mustOK(t, code, v, "comments")
	cs := jarr(jget(v, "comments"))
	if len(cs) != 3 {
		t.Fatalf("comments = %d, want 3", len(cs))
	}
	for i, c := range cs {
		if !f12IsADFDoc(jget(c, "body")) {
			t.Errorf("comment %d body = %v, want a non-empty ADF doc", i, jget(c, "body"))
		}
		if jget(c, "visibility") != nil {
			t.Errorf("comment %d has visibility %v, want none", i, jget(c, "visibility"))
		}
		if !parseJiraTime(t, jget(c, "updated")).Equal(parseJiraTime(t, jget(c, "created"))) {
			t.Errorf("comment %d updated != created", i)
		}
	}
}

func TestComments_MoreThanAHundredPageByStartAt(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	id := issueID(t, s, "MOB-2")
	s.Mutate(func(ds *Dataset) {
		is := issueByKey(ds, "MOB-2")
		for i := 0; i < 130; i++ {
			is.Comments = append(is.Comments, DSComment{
				ID: fmt.Sprintf("9%03d", i), At: ds.Now.Add(-time.Duration(130-i) * time.Minute), Author: "u-ann",
			})
		}
	})
	code, v := get(t, s, "/rest/api/3/issue/"+id+"/comment?orderBy=created&maxResults=100&startAt=0")
	mustOK(t, code, v, "page 1")
	if n := len(jarr(jget(v, "comments"))); n != 100 || jnum(jget(v, "total")) != 130 || jnum(jget(v, "startAt")) != 0 {
		t.Fatalf("page 1: %d comments, total %v, startAt %v, want 100 of 130 at 0", n, jget(v, "total"), jget(v, "startAt"))
	}
	code, v = get(t, s, "/rest/api/3/issue/"+id+"/comment?orderBy=created&maxResults=100&startAt=100")
	mustOK(t, code, v, "page 2")
	if n := len(jarr(jget(v, "comments"))); n != 30 || jnum(jget(v, "startAt")) != 100 {
		t.Fatalf("page 2: %d comments at %v, want 30 at 100", n, jget(v, "startAt"))
	}
	for i, c := range jarr(jget(v, "comments")) {
		if !f12IsADFDoc(jget(c, "body")) {
			t.Fatalf("page 2 comment %d has no ADF body", i)
		}
	}
}

func TestComments_PageSizeCapsOverlayCommentsToo(t *testing.T) {
	defer guard(t)
	s := f12Server(t, Options{PageSize: 3})
	id := issueID(t, s, "MOB-1421")
	code, v := get(t, s, "/rest/api/3/issue/"+id+"/comment?orderBy=created&maxResults=100&startAt=0")
	mustOK(t, code, v, "page 1")
	if n := len(jarr(jget(v, "comments"))); n != 3 || jnum(jget(v, "total")) != 4 {
		t.Fatalf("page 1: %d of %v, want 3 of 4", n, jget(v, "total"))
	}
	code, v = get(t, s, "/rest/api/3/issue/"+id+"/comment?orderBy=created&maxResults=100&startAt=3")
	mustOK(t, code, v, "page 2")
	cs := jarr(jget(v, "comments"))
	if len(cs) != 1 || jget(cs[0], "body") == nil {
		t.Fatalf("page 2: %v, want the last comment with its body", cs)
	}
}

// ── attachments and media ───────────────────────────────────────────────────────────────────────

var f12MediaPathRe = regexp.MustCompile(`^/media/file/([0-9a-f-]{36})/binary$`)

func TestAttachmentContent_303ToTheMediaFileOfThatAttachment(t *testing.T) {
	defer guard(t)
	ov := f12LoadOverlay(t)
	s := f12Server(t, Options{})
	for _, w := range ov.Issues["MOB-1421"].Attachments {
		w := w
		sub(t, w.Filename, func(t *testing.T) {
			resp, _ := f12Do(t, http.MethodGet, s.URL+"/rest/api/3/attachment/content/"+w.ID, basicAuth(Token))
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", resp.StatusCode)
			}
			loc, err := resp.Location()
			if err != nil {
				t.Fatalf("Location: %v", err)
			}
			if loc.Host != strings.TrimPrefix(s.URL, "http://") {
				t.Errorf("Location host = %q, want the fake itself (%s)", loc.Host, s.URL)
			}
			m := f12MediaPathRe.FindStringSubmatch(loc.Path)
			if m == nil {
				t.Fatalf("Location path = %q, want /media/file/<uuid>/binary", loc.Path)
			}
			if m[1] != w.MediaFileID {
				t.Errorf("media file id in Location = %s, want %s (content.json mediaFileId)", m[1], w.MediaFileID)
			}

			// the media URL is pre-signed: no Authorization header, bytes of the file on disk
			mresp, body := f12Do(t, http.MethodGet, loc.String(), "")
			if mresp.StatusCode != http.StatusOK {
				t.Fatalf("media status = %d, want 200 without Authorization", mresp.StatusCode)
			}
			if !bytes.Equal(body, f12MediaFile(t, w.File)) {
				t.Errorf("media body = %d bytes, want the %d bytes of %s", len(body), len(f12MediaFile(t, w.File)), w.File)
			}
			if ct := mresp.Header.Get("Content-Type"); !strings.HasPrefix(ct, w.MimeType) {
				t.Errorf("media Content-Type = %q, want %s", ct, w.MimeType)
			}
		})
	}
}

func TestAttachmentContent_UnknownIdAndUnknownMediaAre404(t *testing.T) {
	defer guard(t)
	s := f12Server(t, Options{})
	if resp, _ := f12Do(t, http.MethodGet, s.URL+"/rest/api/3/attachment/content/99999", basicAuth(Token)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown attachment content = %d, want 404", resp.StatusCode)
	}
	if resp, _ := f12Do(t, http.MethodGet, s.URL+"/rest/api/3/attachment/thumbnail/99999", basicAuth(Token)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown attachment thumbnail = %d, want 404", resp.StatusCode)
	}
	if resp, _ := f12Do(t, http.MethodGet, s.URL+"/media/file/00000000-0000-4000-8000-000000000000/binary", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown media uuid = %d, want 404", resp.StatusCode)
	}
}

func TestAttachmentThumbnail_ImagesFitTheRequestedBox_PdfHasNone(t *testing.T) {
	defer guard(t)
	ov := f12LoadOverlay(t)
	s := f12Server(t, Options{})
	for _, w := range ov.Issues["MOB-1421"].Attachments {
		w := w
		sub(t, w.Filename, func(t *testing.T) {
			resp, body := f12Do(t, http.MethodGet,
				s.URL+"/rest/api/3/attachment/thumbnail/"+w.ID+"?width=320&height=200&fallbackToDefault=false", basicAuth(Token))
			if w.MimeType == "application/pdf" {
				if resp.StatusCode != http.StatusNotFound {
					t.Fatalf("pdf thumbnail status = %d, want 404 (fallbackToDefault=false)", resp.StatusCode)
				}
				return
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
				t.Errorf("Content-Type = %q, want image/*", ct)
			}
			img, _, err := image.Decode(bytes.NewReader(body))
			if err != nil {
				t.Fatalf("thumbnail does not decode: %v", err)
			}
			if b := img.Bounds(); b.Dx() < 1 || b.Dy() < 1 || b.Dx() > 320 || b.Dy() > 200 {
				t.Errorf("thumbnail is %dx%d, want within 320x200", b.Dx(), b.Dy())
			}
		})
	}
}

func TestRequestLog_RecordsContentRedirectAndMediaFetch(t *testing.T) {
	defer guard(t)
	ov := f12LoadOverlay(t)
	att := ov.Issues["MOB-1421"].Attachments[0]
	s := f12Server(t, Options{})
	s.ResetRequests()
	resp, _ := f12Do(t, http.MethodGet, s.URL+"/rest/api/3/attachment/content/"+att.ID, basicAuth(Token))
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("no Location (status %d): %v", resp.StatusCode, err)
	}
	f12Do(t, http.MethodGet, loc.String(), "")
	log := waitLog(t, s, 2)
	if log[0].Method != "GET" || log[0].Path != "/rest/api/3/attachment/content/"+att.ID || log[0].Status != http.StatusSeeOther {
		t.Errorf("log[0] = %+v, want GET content 303", log[0])
	}
	if log[1].Method != "GET" || log[1].Path != "/media/file/"+att.MediaFileID+"/binary" || log[1].Status != http.StatusOK {
		t.Errorf("log[1] = %+v, want GET media 200", log[1])
	}
}

func TestIssueGet_IsLoggedSoNoNetworkAssertionsCanSeeIt(t *testing.T) {
	defer guard(t)
	s := f12Server(t, Options{})
	id := issueID(t, s, "MOB-1421")
	s.ResetRequests()
	code, v := get(t, s, "/rest/api/3/issue/"+id+"?fields=*all")
	mustOK(t, code, v, "GET issue")
	log := waitLog(t, s, 1)
	if log[0].Method != "GET" || log[0].Path != "/rest/api/3/issue/"+id || !strings.Contains(log[0].Query, "fields=") {
		t.Errorf("log[0] = %+v", log[0])
	}
}
