package app_test

// WP-38 review fixes for ConnectionService.Connect: switching sites never touches an existing connection's history
// and leaves the keychain alone; the site address carries no credentials or path.

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
	"github.com/crueber/cuisle/internal/store/storetest"
)

func TestConnect_OtherSiteWhileHistoryExists_IsRefusedAndChangesNothing(t *testing.T) {
	ta := newTestApp(t)
	ta.scrumStandup()

	before := map[string]int{}
	tables := []string{"issue", "status_change", "standup_issue_span", "standup", "connection"}
	for _, tb := range tables {
		before[tb] = storetest.Count(t, ta.DB, tb, "")
	}
	if before["issue"] == 0 || before["status_change"] == 0 || before["standup_issue_span"] == 0 {
		t.Fatalf("setup: expected synced history, got %v", before)
	}
	rowBefore, err := cache.GetConnection(ctx, ta.DB.R)
	noErr(t, err, "GetConnection")
	keysBefore := ta.KR.items()

	other := jiratest.NewServer(t, ta.Fx.fx, jiratest.Options{Now: baseTime, Loc: testLoc})
	in := ta.connectInput()
	in.BaseURL = other.URL
	_, err = ta.Svc.Connection.Connect(ctx, in)
	if !apperr.Is(err, apperr.Conflict) {
		t.Fatalf("Connect to another site = %v, want conflict", err)
	}
	if !strings.Contains(err.Error(), "Switching to another Jira site isn't supported yet.") {
		t.Errorf("message = %q", err.Error())
	}

	for _, tb := range tables {
		if n := storetest.Count(t, ta.DB, tb, ""); n != before[tb] {
			t.Errorf("%s rows = %d, want %d (unchanged)", tb, n, before[tb])
		}
	}
	rowAfter, err := cache.GetConnection(ctx, ta.DB.R)
	noErr(t, err, "GetConnection")
	if !reflect.DeepEqual(rowAfter, rowBefore) {
		t.Errorf("connection row changed:\n got %+v\nwant %+v", rowAfter, rowBefore)
	}
	if got := ta.KR.items(); !reflect.DeepEqual(got, keysBefore) {
		t.Errorf("keychain = %v, want only the first site's item %v", got, keysBefore)
	}
}

func TestConnect_SameSiteNewAccount_KeepsTheConnectionID(t *testing.T) {
	ta := newTestApp(t)
	ta.scrumStandup()
	before, err := cache.GetConnection(ctx, ta.DB.R)
	noErr(t, err, "GetConnection")

	in := ta.connectInput()
	in.Account = "someone.else@example.com"
	c, err := ta.Svc.Connection.Connect(ctx, in)
	noErr(t, err, "Connect")
	if c.Account != in.Account {
		t.Errorf("Account = %q, want %q", c.Account, in.Account)
	}
	after, err := cache.GetConnection(ctx, ta.DB.R)
	noErr(t, err, "GetConnection")
	if after.ID != before.ID || after.BaseURL != before.BaseURL || after.Account != in.Account {
		t.Errorf("connection = %+v, want id %d, same site, new account", after, before.ID)
	}
	if n := storetest.Count(t, ta.DB, "connection", ""); n != 1 {
		t.Errorf("connection rows = %d, want 1", n)
	}
	if n := storetest.Count(t, ta.DB, "standup", "connection_id = ?", before.ID); n != 1 {
		t.Errorf("standups on the connection = %d, want 1", n)
	}
}

func TestConnect_RejectsCredentialsAndPathsInTheSiteAddress(t *testing.T) {
	ta := newTestApp(t)
	cases := []struct{ name, base string }{
		{"userinfo", "https://me:pw@team.atlassian.net"},
		{"user only", "https://me@team.atlassian.net"},
		{"path", "https://team.atlassian.net/jira"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := app.ConnectInput{Source: "jira", BaseURL: c.base, Account: testAccount, Secret: "tok"}
			_, err := ta.Svc.Connection.Connect(ctx, in)
			if !apperr.Is(err, apperr.Validation) {
				t.Fatalf("Connect(%q) = %v, want validation", c.base, err)
			}
			if f := apperr.From(err).Field; f != "baseUrl" {
				t.Errorf("Field = %q, want baseUrl", f)
			}
		})
	}
	if n := storetest.Count(t, ta.DB, "connection", ""); n != 0 {
		t.Errorf("connection rows = %d, want 0", n)
	}
	if got := ta.KR.items(); len(got) != 0 {
		t.Errorf("keychain = %v, want empty", got)
	}
	in := app.ConnectInput{Source: "jira", BaseURL: ta.Fake.URL + "/", Account: testAccount, Secret: jiratest.Token}
	if _, err := ta.Svc.Connection.Connect(ctx, in); err != nil {
		t.Errorf("a bare site address with a trailing slash must still work: %v", err)
	}
}

func TestSyncOnOpen_StartsNothingOnceTheContextIsDone(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	_, err := ta.Svc.App.SetAppSetting(ctx, settings.KeySyncOnOpen, "always")
	noErr(t, err, "SetAppSetting")
	rows := ta.runRows()

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	noErr(t, ta.Svc.RunSyncOnOpen(cctx), "RunSyncOnOpen (cancelled)")
	ta.Orch.Wait(id)
	if n := ta.runRows(); n != rows {
		t.Errorf("sync_run rows = %d, want %d (no run after the context ended)", n, rows)
	}

	noErr(t, ta.Svc.RunSyncOnOpen(ctx), "RunSyncOnOpen")
	ta.Orch.Wait(id)
	if n := ta.runRows(); n != rows+1 {
		t.Errorf("sync_run rows = %d, want %d (a live context starts the run)", n, rows+1)
	}
}
