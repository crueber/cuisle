package jira

// WP-T4 tests for Adapter.GetIssue and the Content capability (jira-sync.md §2.15 "Refresh", §2.8–2.10, ADR 0015).
// Harness: hydrate_test.go (newF12Server) and hydrate_content_test.go.
//
// SPEC-NOTE (GetIssue): GetIssue(sourceID) is "Hydrate of one id" and returns exactly what Hydrate returns for that id
// from the same provider data (reflect.DeepEqual, Content.Hash included). It is NOT a search: it sends
// GET /rest/api/3/issue/{id}?fields=*all,-comment,-worklog (the fields value is exactly that string), one
// POST /rest/api/3/changelog/bulkfetch with issueIdsOrKeys == [id], and the paged GET /issue/{id}/comment of
// §2.10 (orderBy=created, maxResults=100), plus the registry request shared per Adapter. No POST /search/jql.
// A 404 → not_found, 401 → auth_invalid, 403 → forbidden. An issue the provider returns without an id/key pair is
// treated as not found.
// SPEC-NOTE (capabilities): Capabilities().Content is true once WP-T4 lands; HasSprints, HasBacklogRank, HasFlagging,
// HasAssigneeHistory, HasExtraFilter, FilterLanguage "JQL" and AuthFields keep their values. Writes belong to WP-T5
// and are not asserted here.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/source"
)

func TestCapabilities_ContentIsTrueAndTheRestIsUnchanged(t *testing.T) {
	defer wp19Guard(t)
	a := newWP19Adapter(t, "http://127.0.0.1:1", wp19Meta())
	c := a.Capabilities()
	if !c.Content {
		t.Error("Capabilities().Content = false, want true (Fields, FetchMedia, GetIssue and content Hydrate are implemented)")
	}
	if !c.HasSprints || !c.HasBacklogRank || !c.HasFlagging || !c.HasExtraFilter || !c.HasAssigneeHistory || c.FilterLanguage != "JQL" {
		t.Errorf("unrelated capabilities changed: %+v", c)
	}
	if strings.Join(c.AuthFields, ",") != "baseUrl,account,secret" {
		t.Errorf("AuthFields = %v", c.AuthFields)
	}
}

// f12OneIssueGen is a rich issue used by both Hydrate and GetIssue so their results can be compared.
func f12OneIssueGen(id string) map[string]any {
	return f12Issue(id, map[string]any{
		"description":       f12Doc(f12DescSentinel),
		"environment":       f12Doc("iOS"),
		"labels":            []any{"ux"},
		"customfield_10016": 8,
		"attachment": []any{map[string]any{"id": "20001", "filename": "a.png", "mimeType": "image/png", "size": 5,
			"created": "2026-11-18T09:00:00.000+0000", "author": map[string]any{"accountId": "u-iris"}}},
		"issuelinks": []any{map[string]any{"id": "50001", "type": map[string]any{"name": "Blocks", "inward": "is blocked by", "outward": "blocks"},
			"inwardIssue": f12IssueRef("10428", "MOB-1428", "Cooking mode", "10002", "10003")}},
		"subtasks": []any{f12IssueRef("10436", "MOB-1436", "Timer", "10002", "10005")},
		"watches":  map[string]any{"watchCount": 2},
	})
}

func f12OneIssueComments() []map[string]any {
	return []map[string]any{
		f12Cmt("c1", wp19Day(3), wp19Day(2), "u-silas", "atlassian", f12Doc(wp19Sentinel), map[string]any{"type": "role", "value": "Developers"}),
		f12Cmt("c2", wp19Day(1), wp19Day(1), "bot-automation", "app", f12Doc("bot"), nil),
	}
}

func f12GetIssueServer(t *testing.T, hist []wp19Hist) *wp19Srv {
	return newF12Server(t, map[string]wp19Route{
		"GET /rest/api/3/issue/{id}": func(r wp19Req) wp19Resp {
			if id := f12IssueIDOf(r.Path); id != "10001" {
				return wp19Resp{Status: 404, Body: `{"errorMessages":["Issue does not exist or you do not have permission to see it."],"errors":{}}`}
			}
			return wp19Resp{Body: wp19JSON(t, f12OneIssueGen("10001"))}
		},
		"POST /rest/api/3/changelog/bulkfetch": wp19BulkRoute(t, func(string) []wp19Hist { return hist }),
		"GET /rest/api/3/issue/{id}/comment":   f12RawCommentRoute(t, f12OneIssueComments(), 0),
	})
}

func f12HydrateServerSame(t *testing.T, hist []wp19Hist) *wp19Srv {
	return newF12Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql":          wp19SearchRoute(t, f12OneIssueGen, 0),
		"POST /rest/api/3/changelog/bulkfetch": wp19BulkRoute(t, func(string) []wp19Hist { return hist }),
		"GET /rest/api/3/issue/{id}/comment":   f12RawCommentRoute(t, f12OneIssueComments(), 0),
	})
}

func TestGetIssue_ReturnsTheSameCompleteDetailAsHydrateOfOneId(t *testing.T) {
	defer wp19Guard(t)
	hist := wp19DefaultHist("10001")
	viaHydrate := f12Hydrate(t, f12HydrateServerSame(t, hist), "10001")[0]
	c := f12ContentOf(t, viaHydrate) // precondition: Hydrate itself carries content
	if len(viaHydrate.Comments) != 2 || len(viaHydrate.StatusChanges) != 1 || c.Description == nil {
		t.Fatalf("precondition: Hydrate detail lacks comments/changes/description: %+v", viaHydrate)
	}

	srv := f12GetIssueServer(t, hist)
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.GetIssue(context.Background(), "10001")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if !reflect.DeepEqual(got, viaHydrate) {
		gb, _ := json.MarshalIndent(got, "", " ")
		hb, _ := json.MarshalIndent(viaHydrate, "", " ")
		t.Errorf("GetIssue differs from Hydrate of one id\nGetIssue: %s\nHydrate:  %s", gb, hb)
	}
}

func TestGetIssue_IsASingleIssueReadNotASearch(t *testing.T) {
	defer wp19Guard(t)
	srv := f12GetIssueServer(t, wp19DefaultHist("10001"))
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	if _, err := a.GetIssue(context.Background(), "10001"); err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	gets := srv.to("GET", "/rest/api/3/issue/10001")
	if len(gets) != 1 {
		t.Fatalf("GET /issue/10001 requests = %d, want 1", len(gets))
	}
	if got := gets[0].Query.Get("fields"); got != "*all,-comment,-worklog" {
		t.Errorf("fields = %q, want *all,-comment,-worklog", got)
	}
	if n := len(srv.to("POST", "/rest/api/3/search/jql")); n != 0 {
		t.Errorf("search requests = %d, want 0", n)
	}
	bulks := srv.to("POST", "/rest/api/3/changelog/bulkfetch")
	if len(bulks) != 1 {
		t.Fatalf("bulkfetch requests = %d, want 1", len(bulks))
	}
	if ids := wp19StrSlice(bulks[0].json(t)["issueIdsOrKeys"]); len(ids) != 1 || ids[0] != "10001" {
		t.Errorf("bulkfetch ids = %v, want [10001]", ids)
	}
	cs := srv.comments("10001")
	if len(cs) != 1 || cs[0].Query.Get("orderBy") != "created" || cs[0].Query.Get("maxResults") != "100" || cs[0].Query.Get("startAt") != "0" {
		t.Errorf("comment requests = %+v, want one page orderBy=created maxResults=100 startAt=0", cs)
	}
	if n := len(srv.to("GET", "/rest/api/3/field")); n != 1 {
		t.Errorf("GET /field requests = %d, want 1", n)
	}
	for _, r := range srv.all() {
		if r.Method != "GET" && r.Method != "POST" {
			t.Errorf("unexpected method in %+v", r)
		}
	}
}

func TestGetIssue_PagesCommentsLikeHydrate(t *testing.T) {
	defer wp19Guard(t)
	all := f12ManyComments(130)
	srv := newF12Server(t, map[string]wp19Route{
		"GET /rest/api/3/issue/{id}":           func(r wp19Req) wp19Resp { return wp19Resp{Body: wp19JSON(t, f12OneIssueGen("10001"))} },
		"POST /rest/api/3/changelog/bulkfetch": wp19BulkRoute(t, func(string) []wp19Hist { return nil }),
		"GET /rest/api/3/issue/{id}/comment":   f12RawCommentRoute(t, all, 0),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	d, err := a.GetIssue(context.Background(), "10001")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if d.CommentTotal != 130 || len(d.Comments) != 130 {
		t.Errorf("CommentTotal/Comments = %d/%d, want 130/130", d.CommentTotal, len(d.Comments))
	}
	if n := len(srv.comments("10001")); n != 2 {
		t.Errorf("comment requests = %d, want 2 (100 + 30)", n)
	}
}

func TestGetIssue_MapsErrors(t *testing.T) {
	defer wp19Guard(t)
	tests := []struct {
		name string
		id   string
		h    wp19Route
		want apperr.Code
	}{
		{"unknown issue", "10099", nil, apperr.NotFound},
		{"401", "10001", wp19Fail(401, `{}`), apperr.AuthInvalid},
		{"403", "10001", wp19Fail(403, `{}`), apperr.Forbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp19Guard(t)
			srv := f12GetIssueServer(t, nil)
			if tc.h != nil {
				srv = newF12Server(t, map[string]wp19Route{"GET /rest/api/3/issue/{id}": tc.h})
			}
			a := newWP19Adapter(t, srv.URL, wp19Meta())
			_, err := a.GetIssue(context.Background(), tc.id)
			wp19AssertCode(t, err, tc.want)
		})
	}
}

func TestGetIssue_WorksThroughTheSourceInterface(t *testing.T) {
	defer wp19Guard(t)
	srv := f12GetIssueServer(t, nil)
	var s source.Source = newWP19Adapter(t, srv.URL, wp19Meta())
	d, err := s.GetIssue(context.Background(), "10001")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if d.SourceID != "10001" || d.Key != "MOB-10001" || d.Content == nil || len(d.Comments) != 2 {
		t.Errorf("detail = %+v", d.Issue)
	}
}
