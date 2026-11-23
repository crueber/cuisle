package app_test

// AppService: Bootstrap, app settings, UI state, OpenIssue, Diagnostics, perf and client-error reporting, and the
// "panics in bound methods are recovered and returned as internal" rule (architecture.md section 3.4).
//
// SPEC-NOTE: (Bootstrap.View selection) architecture 3.2 says View is "the view for ui.lastStandupId, nil when there
// are no standups". Read together with StandupTab.Configured ("DRAFT never viewed"), the tests require: View is the
// view of ui.lastStandupId when that standup is live AND configured; otherwise it falls back to the FIRST configured
// standup in tab order; it is nil only when no configured standup exists.
//
// SPEC-NOTE: (Bootstrap shape) Settings includes every key of settings.AppDefaults (defaults filled in for rows never
// written) and UI.LastPerson is a non-nil map, so the frontend never meets null where it indexes.
//
// SPEC-NOTE: (panics) the contract has no exported recover wrapper, so the tests provoke a panic in a dependency
// callback (Deps.OpenURL, Deps.PerfSink) and require the service method to return apperr internal, log the panic
// value and a stack, and keep serving afterwards. The panic text must not appear in err.Error().
//
// SPEC-NOTE: (OpenIssue) the key must look like an issue key; anything that could change the URL (slashes, "?", "#",
// spaces, scheme) is a validation error and OpenURL is not called.
//
// SPEC-NOTE: (LogClientError) rate limit 20 distinct messages per rolling minute measured on Deps.Clock (not wall
// time), identical message+stack pairs are logged once, and the method never returns an error for being limited.

import (
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/buildinfo"
	"github.com/crueber/cuisle/internal/settings"
)

func TestBootstrap_NoConnectionNoStandups(t *testing.T) {
	ta := newTestApp(t)
	b := ta.bootstrap()

	if b.Connection.State != "missing" {
		t.Errorf("Connection.State = %q, want missing", b.Connection.State)
	}
	if b.View != nil {
		t.Errorf("View = %+v, want nil without standups", b.View)
	}
	if b.Standups == nil || len(b.Standups) != 0 {
		t.Errorf("Standups = %#v, want a non-nil empty slice", b.Standups)
	}
	if b.Sync == nil || len(b.Sync) != 0 {
		t.Errorf("Sync = %#v, want a non-nil empty slice", b.Sync)
	}
	if b.Recovered {
		t.Errorf("Recovered = true on a healthy DB")
	}
	if b.Build != buildinfo.Get() {
		t.Errorf("Build = %+v, want buildinfo.Get() = %+v", b.Build, buildinfo.Get())
	}
	for k, want := range settings.AppDefaults {
		if got, ok := b.Settings[k]; !ok || got != want {
			t.Errorf("Settings[%q] = %q (present %v), want default %q", k, got, ok, want)
		}
	}
	if b.UI.LastStandupID != 0 || b.UI.LastView != "standup" {
		t.Errorf("UI = %+v, want lastStandupId 0 and lastView standup", b.UI)
	}
	if b.UI.LastPerson == nil {
		t.Errorf("UI.LastPerson is nil, want an empty map")
	}
}

func TestBootstrap_ReportsRecoveredFromDeps(t *testing.T) {
	ta := newTestApp(t)
	ta.Deps.Recovered = true
	if !ta.bootstrap().Recovered {
		t.Errorf("Recovered = false, want true when Deps.Recovered is set (settings-loss banner)")
	}
}

func TestBootstrap_AfterConnectAndSync_CarriesConnectionTabsAndView(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	b := ta.bootstrap()

	c := b.Connection
	if c.State != "ok" || c.Source != "jira" || c.BaseURL != ta.Fake.URL || c.Account != testAccount || c.DisplayName == "" {
		t.Errorf("Connection = %+v, want ok/jira/%s/%s with a display name", c, ta.Fake.URL, testAccount)
	}
	if !c.Capabilities.HasSprints || c.Capabilities.FilterLanguage != "JQL" {
		t.Errorf("Connection.Capabilities = %+v, want the Jira capabilities (sprints, JQL)", c.Capabilities)
	}
	if !contains([]string{"ok", "unavailable", "unknown"}, c.DevActivity) {
		t.Errorf("Connection.DevActivity = %q, want ok|unavailable|unknown", c.DevActivity)
	}
	if len(b.Standups) != 1 {
		t.Fatalf("Standups = %+v, want exactly the one standup", b.Standups)
	}
	tab := b.Standups[0]
	if tab.ID != id || !tab.Configured || tab.SpaceKey != ta.Fx.scrumProject.Key || tab.NeedsSync ||
		tab.LastSyncedAt != ta.Clock.ms() || tab.ActiveCount == 0 || tab.SpeakerCount == 0 {
		t.Errorf("tab = %+v, want configured, synced at %d, with active tickets and speakers", tab, ta.Clock.ms())
	}
	if b.View == nil || b.View.StandupID != id || !b.View.Live || b.View.BoardName != ta.Fx.scrum.Name {
		t.Fatalf("View = %+v, want the live view of standup %d", b.View, id)
	}
	if len(b.Sync) != 1 || b.Sync[0].StandupID != id || b.Sync[0].LastStatus != "ok" || b.Sync[0].State != "idle" {
		t.Errorf("Sync = %+v, want one idle/ok status for the standup", b.Sync)
	}
	if b.Settings["sync_on_open"] == "" {
		t.Errorf("Settings is missing sync_on_open: %+v", b.Settings)
	}
}

func TestBootstrap_ViewFollowsUILastStandupAndPerson(t *testing.T) {
	ta := newTestApp(t)
	first := ta.scrumStandup()
	second := ta.kanbanStandup()

	person := "u-someone"
	noErr(t, ta.Svc.App.SetUIState(ctx, app.UIStatePatch{LastStandupID: ptr(second)}), "SetUIState lastStandupId")
	noErr(t, ta.Svc.App.SetUIState(ctx, app.UIStatePatch{StandupID: ptr(first), LastPerson: ptr(person)}), "SetUIState lastPerson")
	b := ta.bootstrap()
	if b.UI.LastStandupID != second {
		t.Errorf("UI.LastStandupID = %d, want %d", b.UI.LastStandupID, second)
	}
	if b.View == nil || b.View.StandupID != second {
		t.Errorf("View = %+v, want the view of the last standup %d", b.View, second)
	}
	if b.UI.LastPerson[first] != person {
		t.Errorf("UI.LastPerson = %+v, want %q for standup %d", b.UI.LastPerson, person, first)
	}
	if len(b.Standups) != 2 {
		t.Errorf("Standups = %d, want 2", len(b.Standups))
	}
}

func TestBootstrap_ViewFallsBackToFirstConfiguredStandup(t *testing.T) {
	ta := newTestApp(t)
	first := ta.scrumStandup()
	deleted := ta.kanbanStandup()
	draft, err := ta.Svc.Config.Create(ctx)
	noErr(t, err, "Create draft")
	_, err = ta.Svc.Config.Delete(ctx, deleted)
	noErr(t, err, "Delete")

	cases := []struct {
		name string
		last int64
	}{{"unset", 0}, {"archived", deleted}, {"draft", draft.ID}, {"unknown", 99999}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.last != 0 {
				// SetUIState may refuse an unusable last standup (foreign key / not found / validation); either way
				// Bootstrap must still open on the first configured standup.
				if err := ta.Svc.App.SetUIState(ctx, app.UIStatePatch{LastStandupID: ptr(tc.last)}); err != nil {
					wantCodeOneOf(t, err, "SetUIState(unusable lastStandupId)", apperr.Validation, apperr.NotFound)
				}
			}
			b := ta.bootstrap()
			if b.View == nil || b.View.StandupID != first {
				t.Errorf("View = %+v, want the first configured standup %d", b.View, first)
			}
		})
	}
}

func TestBootstrap_OnlyADraftMeansNoView(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()
	draft, err := ta.Svc.Config.Create(ctx)
	noErr(t, err, "Create")
	b := ta.bootstrap()
	if b.View != nil {
		t.Errorf("View = %+v, want nil when the only standup is a draft", b.View)
	}
	if len(b.Standups) != 1 || b.Standups[0].ID != draft.ID || b.Standups[0].Configured {
		t.Errorf("Standups = %+v, want the draft listed with Configured=false (the Configure rail needs it)", b.Standups)
	}
}

func TestSetAppSetting_PersistsAndAcknowledges(t *testing.T) {
	tests := []struct{ key, value string }{
		{"theme", "dark"}, {"theme", "light"}, {"theme", "system"},
		{"text_size", "large"}, {"text_size", "standard"},
		{"sync_on_open", "always"}, {"sync_on_open", "never"}, {"sync_on_open", "stale"},
		{"sync_stale_hours", "1"}, {"sync_stale_hours", "12"}, {"sync_stale_hours", "48"},
	}
	ta := newTestApp(t)
	for _, tc := range tests {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			ack, err := ta.Svc.App.SetAppSetting(ctx, tc.key, tc.value)
			noErr(t, err, "SetAppSetting")
			if ack.SavedAt != ta.Clock.ms() || ack.NeedsSync {
				t.Errorf("ack = %+v, want SavedAt %d and NeedsSync=false", ack, ta.Clock.ms())
			}
			if got := ta.bootstrap().Settings[tc.key]; got != tc.value {
				t.Errorf("Bootstrap.Settings[%q] = %q, want %q", tc.key, got, tc.value)
			}
		})
	}
}

func TestSetAppSetting_RejectsInvalidValuesAndKeys(t *testing.T) {
	tests := []struct{ key, value string }{
		{"theme", "purple"}, {"text_size", "huge"}, {"sync_on_open", "sometimes"},
		{"sync_stale_hours", "0"}, {"sync_stale_hours", "49"}, {"sync_stale_hours", "abc"}, {"sync_stale_hours", ""},
		{"no_such_key", "x"},
	}
	ta := newTestApp(t)
	before := ta.bootstrap().Settings
	for _, tc := range tests {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			_, err := ta.Svc.App.SetAppSetting(ctx, tc.key, tc.value)
			wantCode(t, err, apperr.Validation, "SetAppSetting")
		})
	}
	after := ta.bootstrap().Settings
	if fmtJSON(before) != fmtJSON(after) {
		t.Errorf("rejected settings changed state:\nbefore %s\nafter  %s", fmtJSON(before), fmtJSON(after))
	}
}

func TestSetUIState_PersistsEachFieldIndependently(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()

	noErr(t, ta.Svc.App.SetUIState(ctx, app.UIStatePatch{LastView: ptr("configure")}), "lastView")
	noErr(t, ta.Svc.App.SetUIState(ctx, app.UIStatePatch{LastStandupID: ptr(id)}), "lastStandupId")
	win := &app.WindowState{X: 10, Y: 20, W: 1440, H: 900, Maximised: true}
	noErr(t, ta.Svc.App.SetUIState(ctx, app.UIStatePatch{Window: win}), "window")
	noErr(t, ta.Svc.App.SetUIState(ctx, app.UIStatePatch{StandupID: ptr(id), LastPerson: ptr("")}), "whole board")

	ui := ta.bootstrap().UI
	if ui.LastView != "configure" || ui.LastStandupID != id {
		t.Errorf("UI = %+v, want lastView configure and lastStandupId %d kept across patches", ui, id)
	}
	if ui.Window == nil || *ui.Window != *win {
		t.Errorf("UI.Window = %+v, want %+v", ui.Window, win)
	}
	if p, ok := ui.LastPerson[id]; !ok || p != "" {
		t.Errorf("UI.LastPerson[%d] = %q (present %v), want the whole-board marker \"\"", id, p, ok)
	}
}

func TestSetUIState_RejectsUnknownView(t *testing.T) {
	ta := newTestApp(t)
	err := ta.Svc.App.SetUIState(ctx, app.UIStatePatch{LastView: ptr("settings")})
	wantCode(t, err, apperr.Validation, "SetUIState")
}

func TestOpenIssue_OpensTheSourceIssueURLWithoutNetwork(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()
	mark := ta.networkCalls()
	ta.Fake.ResetRequests()

	noErr(t, ta.Svc.App.OpenIssue(ctx, ta.Fx.activeKey), "OpenIssue")
	want := ta.Fake.URL + "/browse/" + ta.Fx.activeKey
	if got := ta.openedURLs(); len(got) != 1 || got[0] != want {
		t.Errorf("opened %v, want exactly [%s]", got, want)
	}
	ta.assertNoNetwork(mark, "OpenIssue")
}

func TestOpenIssue_RejectsKeysThatCouldChangeTheURL(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()
	for _, key := range []string{"", "../admin", "MOB-1/../../x", "MOB-1?next=http://evil.example", "MOB-1#frag", "MOB 1", "http://evil.example/"} {
		t.Run(key, func(t *testing.T) {
			wantCode(t, ta.Svc.App.OpenIssue(ctx, key), apperr.Validation, "OpenIssue")
		})
	}
	if got := ta.openedURLs(); len(got) != 0 {
		t.Errorf("OpenURL was called for rejected keys: %v", got)
	}
}

func TestDiagnostics_ReportsBuildPathsSchemaAndRecentRuns(t *testing.T) {
	ta := newTestApp(t, withFileDB())
	id := ta.scrumStandup()
	d, err := ta.Svc.App.Diagnostics(ctx)
	noErr(t, err, "Diagnostics")

	if d.Build != buildinfo.Get() || d.OS != runtime.GOOS {
		t.Errorf("Build/OS = %+v / %q, want buildinfo.Get() and %q", d.Build, d.OS, runtime.GOOS)
	}
	if d.DataDir != ta.Paths.Data || d.LogDir != ta.Paths.Logs {
		t.Errorf("DataDir/LogDir = %q / %q, want %q / %q", d.DataDir, d.LogDir, ta.Paths.Data, ta.Paths.Logs)
	}
	if d.SchemaVersion < 1 || d.DBBytes <= 0 {
		t.Errorf("SchemaVersion/DBBytes = %d / %d, want >= 1 and > 0", d.SchemaVersion, d.DBBytes)
	}
	if len(d.RecentRuns) != 1 {
		t.Fatalf("RecentRuns = %+v, want the one sync run", d.RecentRuns)
	}
	r := d.RecentRuns[0]
	if r.StandupID != id || r.Status != "ok" || r.Trigger != "manual" || r.Mode != "full" || r.Requests <= 0 ||
		r.FinishedAt < r.StartedAt || r.StartedAt == 0 || r.Warnings == nil {
		t.Errorf("run = %+v, want standup %d, ok/manual/full, requests > 0, finished >= started, non-nil warnings", r, id)
	}
	if js := fmtJSON(d); strings.Contains(js, fakeToken()) || strings.Contains(js, testAccountBasicAuth()) {
		t.Errorf("Diagnostics JSON contains the token")
	}
}

func TestDiagnostics_KeepsOnlyTheLastFiveRunsNewestFirst(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	var ids []int64
	ids = append(ids, ta.runs()[0].ID)
	for i := 0; i < 5; i++ {
		ta.Clock.Advance(time.Minute)
		ids = append(ids, ta.syncNow(id))
	}
	runs := ta.runs()
	if len(runs) != 5 {
		t.Fatalf("RecentRuns has %d entries after 6 runs, want the last 5", len(runs))
	}
	for i, r := range runs {
		if want := ids[len(ids)-1-i]; r.ID != want {
			t.Errorf("RecentRuns[%d].ID = %d, want %d (newest first)", i, r.ID, want)
		}
	}
}

func TestDiagnostics_NoRunsIsAnEmptyList(t *testing.T) {
	ta := newTestApp(t)
	d, err := ta.Svc.App.Diagnostics(ctx)
	noErr(t, err, "Diagnostics")
	if d.RecentRuns == nil || len(d.RecentRuns) != 0 {
		t.Errorf("RecentRuns = %#v, want a non-nil empty slice", d.RecentRuns)
	}
}

func TestReportPerf_ForwardsMarksToThePerfSink(t *testing.T) {
	ta := newTestApp(t)
	var got map[string]float64
	ta.Deps.PerfSink = func(m map[string]float64) { got = m }
	marks := map[string]float64{"first-view": 123.5, "boot": 40}
	noErr(t, ta.Svc.App.ReportPerf(ctx, marks), "ReportPerf")
	if len(got) != 2 || got["first-view"] != 123.5 || got["boot"] != 40 {
		t.Errorf("PerfSink got %v, want %v", got, marks)
	}
}

var clientMsgRE = regexp.MustCompile(`clienterr-\d\d`)

func distinctClientMsgs(log string) int {
	seen := map[string]bool{}
	for _, m := range clientMsgRE.FindAllString(log, -1) {
		seen[m] = true
	}
	return len(seen)
}

func TestLogClientError_LogsAnIdenticalErrorOnce(t *testing.T) {
	ta := newTestApp(t)
	for i := 0; i < 3; i++ {
		noErr(t, ta.Svc.App.LogClientError(ctx, "clienterr-00 TypeError: x is undefined", "at f (app.js:1:1)"), "LogClientError")
	}
	if n := strings.Count(ta.Log.String(), "clienterr-00"); n != 1 {
		t.Errorf("an identical client error was logged %d times, want 1 (deduplicated)", n)
	}
}

func TestLogClientError_RateLimitsTo20PerMinuteOnTheAppClock(t *testing.T) {
	ta := newTestApp(t)
	for i := 0; i < 25; i++ { // distinct errors within one minute: never an error to the caller
		noErr(t, ta.Svc.App.LogClientError(ctx, "clienterr-"+twoDigits(i), "stack"), "LogClientError")
	}
	if n := distinctClientMsgs(ta.Log.String()); n != 20 {
		t.Errorf("%d distinct client errors were logged within a minute, want exactly 20 (rate limit)", n)
	}
	ta.Clock.Advance(61 * time.Second) // a minute later (fake clock) the limiter lets new ones through
	noErr(t, ta.Svc.App.LogClientError(ctx, "clienterr-90 later", "stack"), "LogClientError (after window)")
	if !strings.Contains(ta.Log.String(), "clienterr-90") {
		t.Errorf("a client error after the rate window was not logged; the limiter must use Deps.Clock")
	}
}

func twoDigits(i int) string {
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestServiceMethods_PanicIsRecoveredAsInternalAndLogged(t *testing.T) {
	tests := []struct {
		name  string
		setup func(ta *testApp)
		call  func(ta *testApp) error
		frame string // must appear in the logged stack
	}{
		{"OpenIssue/OpenURL panics",
			func(ta *testApp) { ta.Deps.OpenURL = func(string) error { panic("boom-open-secret") } },
			func(ta *testApp) error { return ta.Svc.App.OpenIssue(ctx, ta.Fx.activeKey) }, "OpenIssue"},
		{"ReportPerf/PerfSink panics",
			func(ta *testApp) { ta.Deps.PerfSink = func(map[string]float64) { panic("boom-perf-secret") } },
			func(ta *testApp) error { return ta.Svc.App.ReportPerf(ctx, map[string]float64{"x": 1}) }, "ReportPerf"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ta := newTestApp(t)
			ta.connect()
			tc.setup(ta)
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic escaped the service method: %v", r)
					}
				}()
				err = tc.call(ta)
			}()
			ae := wantCode(t, err, apperr.Internal, "panicking call")
			if strings.Contains(err.Error(), "boom") || strings.Contains(ae.Message, "boom") {
				t.Errorf("the panic value leaked into the user-facing error: %q", err.Error())
			}
			log := ta.Log.String()
			if !strings.Contains(log, "boom-") || !strings.Contains(log, tc.frame) {
				t.Errorf("the panic must be logged with its value and stack (want %q and %q in the log)", "boom-", tc.frame)
			}
			// the service keeps working afterwards
			ta.Deps.OpenURL = func(string) error { return nil }
			noErr(t, ta.Svc.App.OpenIssue(ctx, ta.Fx.activeKey), "OpenIssue after a recovered panic")
		})
	}
}
