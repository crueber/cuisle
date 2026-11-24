package app_test

// ConnectionService: Connect/Disconnect, the keychain-only secret rule, auth failures, and the Jira-backed
// autocompletes with their offline fallbacks (jira-sync.md sections 2.3, 2.4, 2.12 and 4.7).
//
// SPEC-NOTE: (loopback http) jira-sync section 1 says the base URL is https only, but the fake Jira listens on
// http://127.0.0.1:<port>. Connect must therefore accept http for loopback hosts (https enforcement, if implemented,
// must exempt them). It rejects an empty URL and any scheme other than http/https as a validation error.
//
// SPEC-NOTE: (Connect) the secret is verified against the source BEFORE anything is persisted: a rejected token
// stores nothing (no keychain item, no connection row). A trailing slash on the base URL is trimmed. Connect emits
// connection:auth with state ok (success) or invalid (token rejected). The keychain account is
// secrets.ConnectionAccount(...); the tests only require that exactly one item holds the token and that its key
// starts with "jira:" and ends with ":<account>" (the host form of host:port is the keyring helper's business).
//
// SPEC-NOTE: (Disconnect) deletes the keychain item, keeps the connection row and every synced record, sets the
// connection state to "missing", emits connection:auth {missing}, and is idempotent. Autocompletes keep answering
// from the local caches without any network call afterwards.
//
// SPEC-NOTE: (auth failures during autocomplete) when the source answers 401 while the user types, the search falls
// back to the caches (no error), the connection turns "invalid", connection:auth {invalid} is emitted, and sync
// triggers are suppressed with apperr auth_invalid until Connect succeeds again (jira-sync 4.7).
//
// SPEC-NOTE: (offline autocomplete) a search with the source unreachable answers from the caches with a
// case-insensitive substring match on key/name and returns no error; results are never nil.

import (
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/store/storetest"
)

func TestConnection_MissingBeforeConnect(t *testing.T) {
	ta := newTestApp(t)
	c, err := ta.Svc.Connection.Connection(ctx)
	noErr(t, err, "Connection")
	if c.State != "missing" {
		t.Errorf("State = %q, want missing", c.State)
	}
}

func TestConnect_VerifiesStoresTheSecretOnlyInTheKeychainAndReportsTheAccount(t *testing.T) {
	ta := newTestApp(t, withFileDB())
	in := ta.connectInput()
	in.BaseURL += "/" // a trailing slash is trimmed
	c, err := ta.Svc.Connection.Connect(ctx, in)
	noErr(t, err, "Connect")

	if c.State != "ok" || c.Source != "jira" || c.BaseURL != ta.Fake.URL || c.Account != testAccount || c.DisplayName == "" {
		t.Errorf("Connection = %+v, want ok/jira/%s/%s with a display name", c, ta.Fake.URL, testAccount)
	}
	if !c.Capabilities.HasSprints || !c.Capabilities.HasExtraFilter || c.Capabilities.FilterLanguage != "JQL" || len(c.Capabilities.AuthFields) == 0 {
		t.Errorf("Capabilities = %+v, want the Jira adapter's", c.Capabilities)
	}
	// verified with /myself, over the network, once
	var myself int
	for _, r := range ta.Fake.Requests() {
		if r.Method == "GET" && r.Path == "/rest/api/3/myself" {
			myself++
		}
	}
	if myself == 0 {
		t.Errorf("Connect never called /rest/api/3/myself: %+v", ta.Fake.Requests())
	}
	// the secret is in the keychain, exactly once, under a <source>:<host>:<account> style key
	accts := ta.KR.accountsHolding(jiratestToken)
	if len(accts) != 1 || !strings.HasPrefix(accts[0], "jira:") || !strings.HasSuffix(accts[0], ":"+testAccount) {
		t.Errorf("keychain items holding the token = %v, want exactly one \"jira:...:%s\"", accts, testAccount)
	}
	if want := secrets.ConnectionAccount("jira", ta.Fake.URL, testAccount); len(accts) == 1 && accts[0] != want {
		t.Errorf("keychain account = %q, want secrets.ConnectionAccount(...) = %q", accts[0], want)
	}
	// ... and nowhere else
	scanDBForText(t, ta.DB, jiratestToken)
	scanFileBytes(t, ta.DB, jiratestToken, testAccountBasicAuth())
	if log := ta.Log.String(); strings.Contains(log, jiratestToken) || strings.Contains(log, testAccountBasicAuth()) {
		t.Errorf("the log contains the token or the Authorization value")
	}
	// events and state
	evs := ta.Em.named("connection:auth")
	if len(evs) == 0 || evs[len(evs)-1].Payload["state"] != "ok" {
		t.Errorf("connection:auth events = %+v, want a last one with state ok", evs)
	}
	if got, _ := ta.Svc.Connection.Connection(ctx); got.State != "ok" || got.DisplayName != c.DisplayName {
		t.Errorf("Connection() = %+v, want the connected account", got)
	}
}

func TestConnect_WrongTokenIsAuthInvalidAndStoresNothing(t *testing.T) {
	ta := newTestApp(t, withFileDB())
	in := ta.connectInput()
	in.Secret = "wrong-token-xyz"
	_, err := ta.Svc.Connection.Connect(ctx, in)
	ae := wantCode(t, err, apperr.AuthInvalid, "Connect(wrong token)")

	for what, text := range map[string]string{"Error()": err.Error(), "message": ae.Message, "marshalled": string(apperr.Marshal(err))} {
		if strings.Contains(text, "wrong-token-xyz") || strings.Contains(text, in.Account+":"+in.Secret) {
			t.Errorf("the rejected token appears in the error %s: %q", what, text)
		}
	}
	if items := ta.KR.items(); len(items) != 0 {
		t.Errorf("a rejected token must not reach the keychain: %v", items)
	}
	if n := storetest.Count(t, ta.DB, "connection", ""); n != 0 {
		t.Errorf("a rejected Connect stored %d connection rows, want 0", n)
	}
	scanDBForText(t, ta.DB, "wrong-token-xyz")
	scanFileBytes(t, ta.DB, "wrong-token-xyz")
	if log := ta.Log.String(); strings.Contains(log, "wrong-token-xyz") {
		t.Errorf("the log contains the rejected token")
	}
	evs := ta.Em.named("connection:auth")
	if len(evs) == 0 || evs[len(evs)-1].Payload["state"] != "invalid" {
		t.Errorf("connection:auth events = %+v, want state invalid", evs)
	}
	if c, _ := ta.Svc.Connection.Connection(ctx); c.State != "missing" {
		t.Errorf("Connection().State = %q after a failed first Connect, want missing", c.State)
	}
}

func TestConnect_RejectsBadInputWithoutAnyNetworkCall(t *testing.T) {
	ta := newTestApp(t)
	base := ta.connectInput()
	tests := map[string]func(in *app.ConnectInput){
		"empty base url": func(in *app.ConnectInput) { in.BaseURL = "" },
		"ftp scheme":     func(in *app.ConnectInput) { in.BaseURL = "ftp://example.atlassian.net" },
		"no scheme":      func(in *app.ConnectInput) { in.BaseURL = "example.atlassian.net" },
		"empty account":  func(in *app.ConnectInput) { in.Account = "" },
		"empty secret":   func(in *app.ConnectInput) { in.Secret = "" },
	}
	mark := ta.networkCalls()
	ta.Fake.ResetRequests()
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			in := base
			mutate(&in)
			_, err := ta.Svc.Connection.Connect(ctx, in)
			wantCode(t, err, apperr.Validation, "Connect")
		})
	}
	ta.assertNoNetwork(mark, "invalid Connect input")
	if items := ta.KR.items(); len(items) != 0 {
		t.Errorf("invalid input reached the keychain: %v", items)
	}
}

func TestConnect_SourceUnreachableIsOfflineAndStoresNothing(t *testing.T) {
	ta := newTestApp(t)
	ta.Net.down.Store(true)
	_, err := ta.Svc.Connection.Connect(ctx, ta.connectInput())
	wantCode(t, err, apperr.Offline, "Connect(offline)")
	if items := ta.KR.items(); len(items) != 0 {
		t.Errorf("keychain items after a failed Connect: %v", items)
	}
	if n := storetest.Count(t, ta.DB, "connection", ""); n != 0 {
		t.Errorf("connection rows after a failed Connect: %d", n)
	}
}

func TestConnect_KeychainUnavailableIsReportedAndNothingIsPersisted(t *testing.T) {
	ta := newTestApp(t)
	ta.KR.setErr = secrets.ErrUnavailable
	_, err := ta.Svc.Connection.Connect(ctx, ta.connectInput())
	ae := wantCode(t, err, apperr.KeychainUnavailable, "Connect(no keychain)")
	if ae.Message == "" || strings.Contains(err.Error(), jiratestToken) {
		t.Errorf("message = %q; it must explain the problem and never carry the token", ae.Message)
	}
	if n := storetest.Count(t, ta.DB, "connection", ""); n != 0 {
		t.Errorf("a Connect that could not store its secret persisted %d connection rows", n)
	}
}

func TestConnect_ReconnectingUpdatesTheOneConnectionAndKeepsStandups(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	before := storetest.Count(t, ta.DB, "standup_issue", "")

	in := ta.connectInput()
	in.Account = "other@example.com"
	c, err := ta.Svc.Connection.Connect(ctx, in)
	noErr(t, err, "Connect(again)")
	if c.Account != "other@example.com" || c.State != "ok" {
		t.Errorf("Connection = %+v, want the new account, ok", c)
	}
	if n := storetest.Count(t, ta.DB, "connection", ""); n != 1 {
		t.Errorf("connection rows = %d, want 1 (v1 keeps exactly one)", n)
	}
	if n := storetest.Count(t, ta.DB, "standup_issue", ""); n != before {
		t.Errorf("standup_issue rows = %d, want %d unchanged", n, before)
	}
	if tabs, _ := ta.Svc.Standup.List(ctx); len(tabs) != 1 || tabs[0].ID != id {
		t.Errorf("standups after reconnect = %+v", tabs)
	}
	ta.syncNow(id) // the new connection syncs
}

func TestDisconnect_DeletesTheSecretKeepsEverythingElse(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	issues := storetest.Count(t, ta.DB, "issue", "")
	ta.Em.reset()
	mark := ta.networkCalls()
	ta.Fake.ResetRequests()

	noErr(t, ta.Svc.Connection.Disconnect(ctx, false), "Disconnect")
	if items := ta.KR.items(); len(items) != 0 {
		t.Errorf("keychain items after Disconnect: %v", items)
	}
	if c, _ := ta.Svc.Connection.Connection(ctx); c.State != "missing" {
		t.Errorf("Connection().State = %q, want missing", c.State)
	}
	evs := ta.Em.named("connection:auth")
	if len(evs) == 0 || evs[len(evs)-1].Payload["state"] != "missing" {
		t.Errorf("connection:auth events = %+v, want state missing", evs)
	}
	if n := storetest.Count(t, ta.DB, "issue", ""); n != issues || issues == 0 {
		t.Errorf("issue rows = %d, want %d kept", n, issues)
	}
	b := ta.bootstrap()
	if len(b.Standups) != 1 || b.View == nil || b.View.StandupID != id || len(b.View.Tickets) == 0 {
		t.Errorf("after Disconnect the cached standup must still open: %+v", b.Standups)
	}
	// autocompletes answer from the caches, offline-style, without a token
	spaces, err := ta.Svc.Connection.SearchSpaces(ctx, ta.Fx.scrumProject.Key)
	noErr(t, err, "SearchSpaces after Disconnect")
	if len(spaces) != 1 || spaces[0].Key != ta.Fx.scrumProject.Key {
		t.Errorf("cached SearchSpaces = %+v", spaces)
	}
	ta.assertNoNetwork(mark, "Disconnect and cached lookups")
	noErr(t, ta.Svc.Connection.Disconnect(ctx, false), "Disconnect (again)")
}

func TestDisconnect_WithoutAConnectionIsANoOp(t *testing.T) {
	ta := newTestApp(t)
	noErr(t, ta.Svc.Connection.Disconnect(ctx, false), "Disconnect")
}

func TestSearchSpaces_OnlineFiltersByQueryAndFillsTheCache(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()

	all, err := ta.Svc.Connection.SearchSpaces(ctx, "")
	noErr(t, err, "SearchSpaces(\"\")")
	keys := map[string]string{}
	for _, s := range all {
		if s.ID == "" || s.Key == "" || s.Name == "" {
			t.Errorf("incomplete option %+v", s)
		}
		keys[s.Key] = s.Name
	}
	if keys[ta.Fx.scrumProject.Key] != ta.Fx.scrumProject.Name || keys[ta.Fx.kanbanProject.Key] != ta.Fx.kanbanProject.Name {
		t.Errorf("SearchSpaces(\"\") = %+v, want both fixture projects", all)
	}
	one, err := ta.Svc.Connection.SearchSpaces(ctx, "opsx")
	noErr(t, err, "SearchSpaces(opsx)")
	if len(one) != 1 || one[0].Key != "OPSX" {
		t.Errorf("SearchSpaces(opsx) = %+v, want only OPSX", one)
	}
	none, err := ta.Svc.Connection.SearchSpaces(ctx, "zzzz-no-such-space")
	noErr(t, err, "SearchSpaces(none)")
	if none == nil || len(none) != 0 {
		t.Errorf("no match must be a non-nil empty slice, got %#v", none)
	}
	if n := storetest.Count(t, ta.DB, "space", ""); n < 2 {
		t.Errorf("space cache has %d rows, want the searched spaces cached (jira-sync 2.3)", n)
	}
}

func TestSearchSpaces_OfflineAnswersFromTheCaseInsensitiveCache(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()
	_, err := ta.Svc.Connection.SearchSpaces(ctx, "") // warm the cache
	noErr(t, err, "warm")
	ta.Net.down.Store(true)

	for _, q := range []string{"OPSX", "opsx", "Operations", "ratio"} {
		got, err := ta.Svc.Connection.SearchSpaces(ctx, q)
		noErr(t, err, "offline SearchSpaces("+q+")")
		if len(got) != 1 || got[0].Key != "OPSX" {
			t.Errorf("offline SearchSpaces(%q) = %+v, want only OPSX (case-insensitive substring on key/name)", q, got)
		}
	}
	none, err := ta.Svc.Connection.SearchSpaces(ctx, "zzzz-none")
	noErr(t, err, "offline no match")
	if none == nil || len(none) != 0 {
		t.Errorf("offline no match = %#v, want non-nil empty", none)
	}
}

func TestSearchBoards_OnlineAndOffline(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()
	_, err := ta.Svc.Connection.SearchSpaces(ctx, "") // the board cache is keyed through the space cache
	noErr(t, err, "SearchSpaces")
	got, err := ta.Svc.Connection.SearchBoards(ctx, ta.Fx.scrumProject.Key, "")
	noErr(t, err, "SearchBoards")
	var scrum bool
	for _, b := range got {
		if b.ID == ta.Fx.scrumID() && b.Name == ta.Fx.scrum.Name && b.Type == "scrum" {
			scrum = true
		}
		if b.ID == ta.Fx.kanbanID() {
			t.Errorf("SearchBoards(%s) returned a board of another project: %+v", ta.Fx.scrumProject.Key, b)
		}
	}
	if !scrum {
		t.Errorf("SearchBoards(%s) = %+v, want the scrum board", ta.Fx.scrumProject.Key, got)
	}
	kb, err := ta.Svc.Connection.SearchBoards(ctx, "OPSX", "board")
	noErr(t, err, "SearchBoards(OPSX, board)")
	if len(kb) != 1 || kb[0].ID != ta.Fx.kanbanID() || kb[0].Type != "kanban" {
		t.Errorf("SearchBoards(OPSX, board) = %+v, want the kanban board", kb)
	}
	none, err := ta.Svc.Connection.SearchBoards(ctx, "OPSX", "zzzz")
	noErr(t, err, "SearchBoards(no match)")
	if none == nil || len(none) != 0 {
		t.Errorf("no match = %#v, want non-nil empty", none)
	}

	// offline: same answers from the board cache, filtered by space and name
	ta.Net.down.Store(true)
	off, err := ta.Svc.Connection.SearchBoards(ctx, "OPSX", "BOARD")
	noErr(t, err, "offline SearchBoards")
	if len(off) != 1 || off[0].ID != ta.Fx.kanbanID() || off[0].Type != "kanban" {
		t.Errorf("offline SearchBoards = %+v, want the cached kanban board only", off)
	}
}

func TestSearchUsers_MarksCurrentSpeakersAndFallsBackToTheCache(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()

	assignee := ta.Fx.userName(ta.Fx.activeAssign)
	found, err := ta.Svc.Connection.SearchUsers(ctx, id, strings.Fields(assignee)[0])
	noErr(t, err, "SearchUsers(assignee)")
	var a *app.UserOption
	for i := range found {
		if found[i].AccountID == ta.Fx.activeAssign {
			a = &found[i]
		}
	}
	if a == nil || !a.OnBoard || a.DisplayName != assignee || a.Initials == "" {
		t.Errorf("SearchUsers(%q) = %+v, want %s with OnBoard=true (already a speaker), name %q and initials", assignee, found, ta.Fx.activeAssign, assignee)
	}
	zed, err := ta.Svc.Connection.SearchUsers(ctx, id, "Zed")
	noErr(t, err, "SearchUsers(Zed)")
	if len(zed) != 1 || zed[0].AccountID != extraUserID || zed[0].OnBoard || zed[0].DisplayName != extraUser || zed[0].Initials != "ZQ" {
		t.Errorf("SearchUsers(Zed) = %+v, want only Zed Quinn with OnBoard=false and initials ZQ", zed)
	}
	// offline: from the person cache, case-insensitive
	ta.Net.down.Store(true)
	off, err := ta.Svc.Connection.SearchUsers(ctx, id, "zed")
	noErr(t, err, "offline SearchUsers")
	if len(off) != 1 || off[0].AccountID != extraUserID {
		t.Errorf("offline SearchUsers(zed) = %+v, want only %s", off, extraUserID)
	}
	empty, err := ta.Svc.Connection.SearchUsers(ctx, id, "zzzz-none")
	noErr(t, err, "offline no match")
	if empty == nil || len(empty) != 0 {
		t.Errorf("no match = %#v, want non-nil empty", empty)
	}
}

func TestSearchUsers_UnknownStandupIsNotFound(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()
	_, err := ta.Svc.Connection.SearchUsers(ctx, 4242, "a")
	wantCode(t, err, apperr.NotFound, "SearchUsers(unknown standup)")
}

func TestValidateFilter_ReturnsAResultAndRecordsItOnTheStandup(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()

	good, err := ta.Svc.Connection.ValidateFilter(ctx, id, `status in ("In Progress")`)
	noErr(t, err, "ValidateFilter(valid)")
	if !good.OK || good.Errors == nil || len(good.Errors) != 0 || good.CheckedAt != ta.Clock.ms() {
		t.Errorf("valid filter check = %+v, want OK, empty non-nil Errors, CheckedAt %d", good, ta.Clock.ms())
	}
	bad, err := ta.Svc.Connection.ValidateFilter(ctx, id, `status in (`)
	noErr(t, err, "ValidateFilter(invalid) must return a result, not an error")
	if bad.OK || len(bad.Errors) == 0 || bad.CheckedAt != ta.Clock.ms() {
		t.Errorf("invalid filter check = %+v, want !OK with messages", bad)
	}
	if fc := ta.config(id).FilterCheck; fc == nil || fc.OK || len(fc.Errors) == 0 {
		t.Errorf("Config.FilterCheck = %+v, want the last result (invalid) persisted", fc)
	}
	_, err = ta.Svc.Connection.ValidateFilter(ctx, 4242, "x")
	wantCode(t, err, apperr.NotFound, "ValidateFilter(unknown standup)")
}

func TestAuthRejectedWhileTyping_FallsBackToCacheAndSuppressesSyncUntilReconnect(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	ta.Em.reset()

	ta.Fake.SetUnauthorized(true)
	got, err := ta.Svc.Connection.SearchSpaces(ctx, ta.Fx.scrumProject.Key)
	noErr(t, err, "SearchSpaces while the token is rejected must fall back to the cache")
	if len(got) != 1 || got[0].Key != ta.Fx.scrumProject.Key {
		t.Errorf("fallback result = %+v, want the cached scrum space", got)
	}
	if c, _ := ta.Svc.Connection.Connection(ctx); c.State != "invalid" {
		t.Errorf("Connection().State = %q, want invalid after a 401", c.State)
	}
	evs := ta.Em.named("connection:auth")
	if len(evs) == 0 || evs[len(evs)-1].Payload["state"] != "invalid" {
		t.Errorf("connection:auth events = %+v, want state invalid", evs)
	}
	if b := ta.bootstrap(); b.Connection.State != "invalid" || b.View == nil {
		t.Errorf("Bootstrap = connection %q view %v, want invalid with the cached view", b.Connection.State, b.View != nil)
	}

	runs := ta.runRows()
	_, err = ta.Svc.Sync.Start(ctx, id, "incremental")
	wantCode(t, err, apperr.AuthInvalid, "Sync.Start while the token is invalid")
	if got := ta.runRows(); got != runs {
		t.Errorf("a suppressed sync created %d new runs", got-runs)
	}

	ta.Fake.SetUnauthorized(false)
	ta.connect()
	if c, _ := ta.Svc.Connection.Connection(ctx); c.State != "ok" {
		t.Errorf("Connection().State = %q after reconnecting", c.State)
	}
	ta.syncNow(id)
}
