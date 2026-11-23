package jira

// WP-19 tests for dev.go (DevActivity, DevFingerprint): the best-effort dev-status integration of jira-sync.md §2.11
// and architecture.md R2. Harness and conventions: see scan_test.go.
//
// SPEC-NOTE: DevFingerprint(rc, rl, pc, pl, ps) = lower-case hex sha1 of the UTF-8 string
// "<rc>|<rl>|<pc>|<pl>|<ps>" (decimal counts, timestamps verbatim as Jira sent them). Two literal hashes are pinned
// besides the computed one.
// SPEC-NOTE: DevActivity reads summary.repository.overall.{count,lastUpdated} and summary.pullrequest.overall.{count,
// lastUpdated,state} (missing/null -> 0 / "" / ""), builds the fingerprint with exactly those values, and returns it in
// DevActivity.Fingerprint whether or not it changed. Changed == (fingerprint != prevFingerprint) so an empty
// prevFingerprint is always "changed". Unchanged -> exactly one request (the summary), Events empty.
// SPEC-NOTE: Detail calls happen only when changed: dataType=repository once for each key of
// summary.repository.byInstanceType and dataType=pullrequest once for each key of summary.pullrequest.byInstanceType
// (each data type only for the instance types listed under its own summary entry); applicationType carries the key
// verbatim, issueId is the sourceID. A summary without instance types makes no detail call.
// SPEC-NOTE: Events: commits {Kind "commit", ID sha, Repo = repositories[].name, At = authorTimestamp, Merge}, merge
// commits ARE returned (the cache excludes them from marks); PR events {Kind "pr", ID "<repositoryName>#<id>", Repo =
// repositoryName, At = lastUpdate, Status = status, Merge false}. A status other than OPEN|MERGED|DECLINED becomes
// "UNKNOWN". Event order is not pinned.
// SPEC-NOTE: Probe failure = the summary call answers 401, 403, 404 or a non-JSON body (200 text/html). The result is
// source.ErrUnsupported (source.IsUnsupported), Capabilities().HasDevActivity false, Meta()["devActivity"] ==
// "unavailable", and every later DevActivity call on the same Adapter returns ErrUnsupported without any request.
// Meta()["devActivity"] is absent before any call and "ok" after a successful summary.
// SPEC-NOTE: Per-issue failures (5xx on the summary of one issue, or a failing detail call after a good summary) return a
// non-unsupported error and do NOT switch dev activity off; the next call performs requests again.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

const (
	wp19RepoLast = "2026-11-20T10:00:00.000+0000"
	wp19PRLast   = "2026-11-21T09:00:00.000+0000"
	wp19DevSum   = "/rest/dev-status/latest/issue/summary"
	wp19DevDet   = "/rest/dev-status/latest/issue/detail"
)

func wp19DevSummary(t *testing.T, rc int, rl string, pc int, pl, ps string, repoTypes, prTypes []string) string {
	byType := func(types []string, count int) map[string]any {
		m := map[string]any{}
		for _, ty := range types {
			m[ty] = map[string]any{"count": count, "name": ty}
		}
		return m
	}
	repo := map[string]any{"overall": map[string]any{"count": rc, "lastUpdated": nil}, "byInstanceType": byType(repoTypes, rc)}
	pr := map[string]any{"overall": map[string]any{"count": pc, "lastUpdated": nil, "stateCount": pc}, "byInstanceType": byType(prTypes, pc)}
	if rl != "" {
		repo["overall"].(map[string]any)["lastUpdated"] = rl
	}
	if pl != "" {
		pr["overall"].(map[string]any)["lastUpdated"] = pl
	}
	if ps != "" {
		pr["overall"].(map[string]any)["state"] = ps
	}
	return wp19JSON(t, map[string]any{"errors": []any{}, "configErrors": []any{}, "summary": map[string]any{"repository": repo, "pullrequest": pr}})
}

const wp19GitHubRepoDetail = `{"errors":[],"detail":[{"_instance":{"type":"GitHub"},"repositories":[{"name":"acme/api","commits":[
	{"id":"abc123","displayId":"abc123","authorTimestamp":"2026-11-19T10:00:00.000+0000","merge":false,"message":"Fix retry"},
	{"id":"def456","displayId":"def456","authorTimestamp":"2026-11-20T10:00:00.000+0000","merge":true}]}]}]}`

const wp19BitbucketRepoDetail = `{"detail":[{"repositories":[{"name":"acme/legacy","commits":[
	{"id":"ghi789","authorTimestamp":"2026-11-18T08:00:00.000+0100","merge":false}]}]}]}`

const wp19GitHubPRDetail = `{"detail":[{"pullRequests":[
	{"id":"12","name":"Fix retries","status":"MERGED","lastUpdate":"2026-11-20T09:00:00.000+0100","repositoryId":"r1","repositoryName":"acme/api"},
	{"id":"13","name":"Docs","status":"OPEN","lastUpdate":"2026-11-21T09:00:00.000+0000","repositoryId":"r2","repositoryName":"acme/web"},
	{"id":"14","name":"Odd","status":"SUPERSEDED","lastUpdate":"2026-11-21T10:00:00.000+0000","repositoryId":"r2","repositoryName":"acme/web"}]}]}`

func wp19DevRoutes(t *testing.T, summary string) map[string]wp19Route {
	return map[string]wp19Route{
		"GET " + wp19DevSum: wp19Ok(summary),
		"GET " + wp19DevDet: func(r wp19Req) wp19Resp {
			switch r.Query.Get("applicationType") + "/" + r.Query.Get("dataType") {
			case "GitHub/repository":
				return wp19Resp{Body: wp19GitHubRepoDetail}
			case "bitbucket/repository":
				return wp19Resp{Body: wp19BitbucketRepoDetail}
			case "GitHub/pullrequest":
				return wp19Resp{Body: wp19GitHubPRDetail}
			}
			t.Errorf("unexpected detail query %v", r.Query)
			return wp19Resp{Status: 404, Body: `{}`}
		},
	}
}

func wp19Sha1Hex(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ── DevFingerprint ───────────────────────────────────────────────────────────

func TestDevFingerprint_IsSha1OfThePipeJoinedString(t *testing.T) {
	defer wp19Guard(t)
	tests := []struct {
		name        string
		rc          int
		rl          string
		pc          int
		pl, ps      string
		wantString  string
		wantLiteral string // sha1 hex computed with `printf '%s' <wantString> | shasum`
	}{
		{"full", 3, wp19RepoLast, 1, wp19PRLast, "MERGED", "3|2026-11-20T10:00:00.000+0000|1|2026-11-21T09:00:00.000+0000|MERGED", "c5046a141e8942475db56c79ecea3ea159e20829"},
		{"nothing", 0, "", 0, "", "", "0||0||", "98980092d804fcd472adfc4bc19529efb5b83c1b"},
		{"commits only, offset kept verbatim", 12, "2026-11-19T08:30:00.000+0100", 0, "", "", "12|2026-11-19T08:30:00.000+0100|0||", "16f0e922df327a5f7a32153b025025096b4e1a21"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp19Guard(t)
			got := DevFingerprint(tc.rc, tc.rl, tc.pc, tc.pl, tc.ps)
			if want := wp19Sha1Hex(tc.wantString); got != want {
				t.Errorf("DevFingerprint = %s, want sha1(%q) = %s", got, tc.wantString, want)
			}
			if got != tc.wantLiteral {
				t.Errorf("DevFingerprint = %s, want literal %s", got, tc.wantLiteral)
			}
		})
	}
}

func TestDevFingerprint_ChangesWithEveryComponent(t *testing.T) {
	defer wp19Guard(t)
	base := DevFingerprint(3, wp19RepoLast, 1, wp19PRLast, "OPEN")
	variants := map[string]string{
		"repo count":   DevFingerprint(4, wp19RepoLast, 1, wp19PRLast, "OPEN"),
		"repo updated": DevFingerprint(3, "2026-11-22T10:00:00.000+0000", 1, wp19PRLast, "OPEN"),
		"pr count":     DevFingerprint(3, wp19RepoLast, 2, wp19PRLast, "OPEN"),
		"pr updated":   DevFingerprint(3, wp19RepoLast, 1, "2026-11-22T09:00:00.000+0000", "OPEN"),
		"pr state":     DevFingerprint(3, wp19RepoLast, 1, wp19PRLast, "MERGED"),
	}
	for name, v := range variants {
		if v == base {
			t.Errorf("fingerprint did not change with %s", name)
		}
	}
}

// ── DevActivity ──────────────────────────────────────────────────────────────

func TestDevActivity_UnchangedFingerprintMakesNoDetailCalls(t *testing.T) {
	defer wp19Guard(t)
	summary := wp19DevSummary(t, 3, wp19RepoLast, 1, wp19PRLast, "MERGED", []string{"GitHub"}, []string{"GitHub"})
	srv := newWP19Server(t, map[string]wp19Route{"GET " + wp19DevSum: wp19Ok(summary)}) // detail is unrouted: a call fails the test
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	prev := DevFingerprint(3, wp19RepoLast, 1, wp19PRLast, "MERGED")
	got, err := a.DevActivity(context.Background(), "10390", prev)
	if err != nil {
		t.Fatalf("DevActivity: %v", err)
	}
	if got.Changed {
		t.Error("Changed = true, want false (same fingerprint)")
	}
	if got.Fingerprint != prev {
		t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, prev)
	}
	if len(got.Events) != 0 {
		t.Errorf("Events = %+v, want none", got.Events)
	}
	reqs := srv.all()
	if len(reqs) != 1 || reqs[0].Path != wp19DevSum {
		t.Fatalf("requests = %+v, want just the summary", reqs)
	}
	if reqs[0].Query.Get("issueId") != "10390" {
		t.Errorf("issueId = %q, want 10390", reqs[0].Query.Get("issueId"))
	}
	if !a.Capabilities().HasDevActivity {
		t.Error("HasDevActivity = false after a successful probe")
	}
	if got := wp19MetaString(a.Meta(), "devActivity"); got != "ok" {
		t.Errorf("Meta[devActivity] = %q, want ok", got)
	}
}

func TestDevActivity_ChangedFingerprintFetchesDetailPerInstanceTypeAndMapsEvents(t *testing.T) {
	defer wp19Guard(t)
	summary := wp19DevSummary(t, 3, wp19RepoLast, 1, wp19PRLast, "MERGED", []string{"GitHub", "bitbucket"}, []string{"GitHub"})
	srv := newWP19Server(t, wp19DevRoutes(t, summary))
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.DevActivity(context.Background(), "10390", "an-old-fingerprint")
	if err != nil {
		t.Fatalf("DevActivity: %v", err)
	}
	if !got.Changed {
		t.Error("Changed = false, want true")
	}
	if want := DevFingerprint(3, wp19RepoLast, 1, wp19PRLast, "MERGED"); got.Fingerprint != want {
		t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, want)
	}

	var calls []string
	for _, r := range srv.to("GET", wp19DevDet) {
		if r.Query.Get("issueId") != "10390" {
			t.Errorf("detail issueId = %q", r.Query.Get("issueId"))
		}
		calls = append(calls, r.Query.Get("applicationType")+"/"+r.Query.Get("dataType"))
	}
	sort.Strings(calls)
	if fmt.Sprint(calls) != "[GitHub/pullrequest GitHub/repository bitbucket/repository]" {
		t.Errorf("detail calls = %v, want one per instance type and data type", calls)
	}
	if n := len(srv.to("GET", wp19DevSum)); n != 1 {
		t.Errorf("summary calls = %d, want 1", n)
	}

	at := func(s string) time.Time {
		tm, err := time.Parse("2006-01-02T15:04:05.000-0700", s)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	want := []model.DevEvent{
		{Kind: model.DevCommit, ID: "abc123", Repo: "acme/api", At: at("2026-11-19T10:00:00.000+0000"), Merge: false},
		{Kind: model.DevCommit, ID: "def456", Repo: "acme/api", At: at("2026-11-20T10:00:00.000+0000"), Merge: true},
		{Kind: model.DevCommit, ID: "ghi789", Repo: "acme/legacy", At: at("2026-11-18T08:00:00.000+0100")},
		{Kind: model.DevPR, ID: "acme/api#12", Repo: "acme/api", At: at("2026-11-20T09:00:00.000+0100"), Status: "MERGED"},
		{Kind: model.DevPR, ID: "acme/web#13", Repo: "acme/web", At: at("2026-11-21T09:00:00.000+0000"), Status: "OPEN"},
		{Kind: model.DevPR, ID: "acme/web#14", Repo: "acme/web", At: at("2026-11-21T10:00:00.000+0000"), Status: "UNKNOWN"},
	}
	key := func(e model.DevEvent) string { return e.Kind + "/" + e.ID }
	events := append([]model.DevEvent(nil), got.Events...)
	sort.Slice(events, func(i, j int) bool { return key(events[i]) < key(events[j]) })
	sort.Slice(want, func(i, j int) bool { return key(want[i]) < key(want[j]) })
	if len(events) != len(want) {
		t.Fatalf("events = %+v, want %d", events, len(want))
	}
	for i, w := range want {
		g := events[i]
		if g.Kind != w.Kind || g.ID != w.ID || g.Repo != w.Repo || g.Status != w.Status || g.Merge != w.Merge || !g.At.Equal(w.At) {
			t.Errorf("event %s = %+v, want %+v", key(w), g, w)
		}
	}
}

func TestDevActivity_ChangedWithoutInstanceTypesMakesNoDetailCalls(t *testing.T) {
	defer wp19Guard(t)
	summary := wp19DevSummary(t, 0, "", 0, "", "", nil, nil)
	srv := newWP19Server(t, map[string]wp19Route{"GET " + wp19DevSum: wp19Ok(summary)})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.DevActivity(context.Background(), "10390", "")
	if err != nil {
		t.Fatalf("DevActivity: %v", err)
	}
	if !got.Changed || len(got.Events) != 0 {
		t.Errorf("got = %+v, want Changed with no events (an empty previous fingerprint is always a change)", got)
	}
	if want := "98980092d804fcd472adfc4bc19529efb5b83c1b"; got.Fingerprint != want {
		t.Errorf("Fingerprint = %q, want sha1(\"0||0||\") = %s", got.Fingerprint, want)
	}
	if n := len(srv.all()); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

func TestDevActivity_ProbeFailureDisablesDevActivityForTheAdapter(t *testing.T) {
	defer wp19Guard(t)
	tests := []struct {
		name string
		resp wp19Resp
	}{
		{"404", wp19Resp{Status: 404, Body: `{"errorMessages":["Not Found"]}`}},
		{"403", wp19Resp{Status: 403, Body: `{"errorMessages":["Forbidden"]}`}},
		{"401", wp19Resp{Status: 401, Body: `{}`}},
		{"200 with an HTML body", wp19Resp{Status: 200, Body: `<html><body>Login required</body></html>`, NonJSON: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp19Guard(t)
			srv := newWP19Server(t, map[string]wp19Route{"GET " + wp19DevSum: func(wp19Req) wp19Resp { return tc.resp }})
			a := newWP19Adapter(t, srv.URL, wp19Meta())
			if !a.Capabilities().HasDevActivity {
				t.Fatal("HasDevActivity is false before the probe")
			}
			if v, has := a.Meta()["devActivity"]; has {
				t.Fatalf("Meta[devActivity] = %v before any call, want absent", v)
			}
			_, err := a.DevActivity(context.Background(), "10390", "")
			if !source.IsUnsupported(err) || !errors.Is(err, source.ErrUnsupported) {
				t.Fatalf("err = %v (code %s), want source.ErrUnsupported", err, apperr.CodeOf(err))
			}
			if a.Capabilities().HasDevActivity {
				t.Error("HasDevActivity still true after the probe failed")
			}
			if got := wp19MetaString(a.Meta(), "devActivity"); got != "unavailable" {
				t.Errorf("Meta[devActivity] = %q, want unavailable", got)
			}
			before := len(srv.all())
			for _, id := range []string{"10391", "10392"} {
				_, err := a.DevActivity(context.Background(), id, "")
				if !source.IsUnsupported(err) {
					t.Errorf("later call for %s: err = %v, want ErrUnsupported", id, err)
				}
			}
			if after := len(srv.all()); after != before {
				t.Errorf("later calls made %d requests, want 0", after-before)
			}
			if before != 1 {
				t.Errorf("probe made %d requests, want exactly 1", before)
			}
		})
	}
}

func TestDevActivity_PerIssueServerErrorKeepsDevActivityOn(t *testing.T) {
	defer wp19Guard(t)
	good := wp19DevSummary(t, 1, wp19RepoLast, 0, "", "", []string{"GitHub"}, nil)
	srv := newWP19Server(t, map[string]wp19Route{
		"GET " + wp19DevSum: func(r wp19Req) wp19Resp {
			if r.Query.Get("issueId") == "10391" {
				return wp19Resp{Status: 500, Body: `{"errorMessages":["boom"]}`}
			}
			return wp19Resp{Body: good}
		},
		"GET " + wp19DevDet: wp19Ok(wp19GitHubRepoDetail),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	_, err := a.DevActivity(context.Background(), "10391", "")
	if err == nil {
		t.Fatal("err = nil, want an error for a 500")
	}
	if source.IsUnsupported(err) {
		t.Fatalf("err = %v: a per-issue 5xx must not look like ErrUnsupported", err)
	}
	if !a.Capabilities().HasDevActivity {
		t.Error("HasDevActivity flipped to false on a per-issue failure")
	}
	if v, has := a.Meta()["devActivity"]; has && v == "unavailable" {
		t.Error("Meta[devActivity] = unavailable after a per-issue failure")
	}
	before := len(srv.all())
	got, err := a.DevActivity(context.Background(), "10392", "")
	if err != nil {
		t.Fatalf("next issue: %v", err)
	}
	if !got.Changed || len(got.Events) != 2 {
		t.Errorf("next issue result = %+v", got)
	}
	if len(srv.all()) == before {
		t.Error("the call after a per-issue failure made no requests")
	}
}

func TestDevActivity_DetailFailureAfterGoodSummaryIsAnErrorNotUnsupported(t *testing.T) {
	defer wp19Guard(t)
	summary := wp19DevSummary(t, 3, wp19RepoLast, 1, wp19PRLast, "MERGED", []string{"GitHub"}, []string{"GitHub"})
	srv := newWP19Server(t, map[string]wp19Route{
		"GET " + wp19DevSum: wp19Ok(summary),
		"GET " + wp19DevDet: func(r wp19Req) wp19Resp {
			if r.Query.Get("dataType") == "pullrequest" {
				return wp19Resp{Status: 500, Body: `{"errorMessages":["boom"]}`}
			}
			return wp19Resp{Body: wp19GitHubRepoDetail}
		},
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	_, err := a.DevActivity(context.Background(), "10390", "")
	if err == nil || source.IsUnsupported(err) {
		t.Fatalf("err = %v, want a non-unsupported error", err)
	}
	if !a.Capabilities().HasDevActivity {
		t.Error("HasDevActivity flipped to false on a detail failure")
	}
}
