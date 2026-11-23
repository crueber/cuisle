package app_test

// Sync-on-open policy (architecture.md sections 5.1 and 6.4; testing.md section 3 "Sync-on-open stale").
//
// SPEC-NOTE: (CONTRACT GAP: the startup hook) the frozen contract has no way to run the sync-on-open check.
// architecture 5.1 says "Go's ServiceStartup has already scheduled the sync-on-open check (runs once, 300 ms after the
// window's ready event)", but neither Deps nor Services exposes that check, and nothing may be added to the BOUND
// services (the method sets are frozen and Wails would expose a new method to the frontend). These tests are written
// against this proposed hook, which the orchestrator should add to internal/app (on Services, which is not bound):
//
//	// RunSyncOnOpen applies the sync_on_open policy once. The scheduled one-shot (ServiceStartup, 300 ms after the
//	// window is ready) is its only caller and the only timer in the app.
//	func (s *Services) RunSyncOnOpen(ctx context.Context) error
//
// Required behaviour:
//   - It reads app settings sync_on_open (never|stale|always) and sync_stale_hours; "never" does nothing.
//   - Candidates are live, configured standups (never drafts or archived ones). The visible standup (ui.lastStandupId,
//     else the first configured one) is started first, then the rest in tab order.
//   - "always" starts every candidate. "stale" starts a candidate that never synced successfully or whose
//     now - last_success_at is STRICTLY greater than sync_stale_hours hours (exactly the threshold is not stale).
//   - It starts incremental syncs through the orchestrator with trigger "open" and returns once the runs have been
//     STARTED (not finished); it never waits for them, and a run's own failure (offline, filter_invalid, ...) is
//     never returned: offline at launch is silent (jira-sync 4.7, no banner).
//   - It is a quiet no-op (nil error, zero requests) when there is no connection or the connection is "invalid"
//     (all sync triggers are suppressed until Connect succeeds).
// Until the hook exists these tests fail with a "contract gap" message (they find it by reflection so the package
// still compiles).
//
// What IS testable today without the hook is covered in notrigger_test.go (Bootstrap and the boot middleware never
// sync under any policy) and appservice_test.go (setting validation).

import (
	"reflect"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/app"
)

const hookGap = "contract gap: (*app.Services).RunSyncOnOpen(ctx context.Context) error does not exist. IMPLEMENTERS: do NOT add it yourself; it is an orchestrator contract decision (see the SPEC-NOTE at the top of syncopen_test.go)"

// runSyncOnOpen calls the proposed hook by reflection and returns its error.
func runSyncOnOpen(t *testing.T, ta *testApp) error {
	t.Helper()
	m := reflect.ValueOf(ta.Svc).MethodByName("RunSyncOnOpen")
	if !m.IsValid() {
		t.Fatal(hookGap)
	}
	mt := m.Type()
	errType := reflect.TypeOf((*error)(nil)).Elem()
	if mt.NumIn() != 1 || mt.NumOut() != 1 || mt.Out(0) != errType {
		t.Fatalf("%s (found %s)", hookGap, mt)
	}
	out := m.Call([]reflect.Value{reflect.ValueOf(ctx)})
	if out[0].IsNil() {
		return nil
	}
	return out[0].Interface().(error)
}

func (ta *testApp) setPolicy(policy string, staleHours string) {
	ta.t.Helper()
	if _, err := ta.Svc.App.SetAppSetting(ctx, "sync_on_open", policy); err != nil {
		ta.t.Fatalf("SetAppSetting(sync_on_open): %v", err)
	}
	if staleHours != "" {
		if _, err := ta.Svc.App.SetAppSetting(ctx, "sync_stale_hours", staleHours); err != nil {
			ta.t.Fatalf("SetAppSetting(sync_stale_hours): %v", err)
		}
	}
}

// openRuns returns the runs with trigger "open", newest first.
func (ta *testApp) openRuns() []app.RunInfo {
	var out []app.RunInfo
	for _, r := range ta.runs() {
		if r.Trigger == "open" {
			out = append(out, r)
		}
	}
	return out
}

func (ta *testApp) waitAll(ids ...int64) {
	for _, id := range ids {
		ta.Orch.Wait(id)
	}
}

func TestSyncOnOpen_NeverDoesNothing(t *testing.T) {
	ta := newTestApp(t)
	ta.scrumStandup()
	kanban := ta.kanbanStandup() // never synced, the most tempting candidate
	ta.setPolicy("never", "")
	mark := ta.networkCalls()
	ta.Fake.ResetRequests()

	noErr(t, runSyncOnOpen(t, ta), "RunSyncOnOpen(never)")
	ta.waitAll(kanban)
	ta.assertNoNetwork(mark, "sync_on_open=never")
}

func TestSyncOnOpen_AlwaysStartsEveryConfiguredStandupVisibleFirst(t *testing.T) {
	ta := newTestApp(t)
	scrum := ta.scrumStandup()
	kanban := ta.kanbanStandup()
	draft, err := ta.Svc.Config.Create(ctx)
	noErr(t, err, "Create draft")
	archived := ta.kanbanStandup()
	_, err = ta.Svc.Config.Delete(ctx, archived)
	noErr(t, err, "Delete")
	ta.setPolicy("always", "")
	noErr(t, ta.Svc.App.SetUIState(ctx, app.UIStatePatch{LastStandupID: ptr(kanban)}), "SetUIState")
	ta.Em.reset()

	noErr(t, runSyncOnOpen(t, ta), "RunSyncOnOpen(always)")
	ta.waitAll(scrum, kanban, draft.ID, archived)

	runs := ta.openRuns()
	if len(runs) != 2 {
		t.Fatalf("runs with trigger open = %+v, want exactly the 2 configured standups (no draft, no archived)", runs)
	}
	byStandup := map[int64]app.RunInfo{}
	for _, r := range runs {
		byStandup[r.StandupID] = r
	}
	if _, ok := byStandup[scrum]; !ok {
		t.Errorf("the scrum standup was not synced on open: %+v", runs)
	}
	k, ok := byStandup[kanban]
	if !ok {
		t.Fatalf("the kanban standup was not synced on open: %+v", runs)
	}
	if byStandup[scrum].Mode != "incremental" {
		t.Errorf("an already synced standup must be synced incrementally, got mode %q", byStandup[scrum].Mode)
	}
	if k.Mode != "full" {
		t.Errorf("a never-synced standup is promoted to a full sync, got mode %q", k.Mode)
	}
	if k.ID >= byStandup[scrum].ID {
		t.Errorf("the visible standup (kanban, run %d) must be started before the other (run %d)", k.ID, byStandup[scrum].ID)
	}
	for _, e := range ta.Em.named("sync:started") {
		if e.Payload["trigger"] != "open" {
			t.Errorf("sync:started = %+v, want trigger open", e.Payload)
		}
	}
}

func TestSyncOnOpen_StaleOnlySyncsStandupsOlderThanTheThreshold(t *testing.T) {
	tests := []struct {
		name  string
		hours string // sync_stale_hours; "" keeps the default 6
		age   time.Duration
		want  bool
	}{
		{"fresh", "", time.Hour, false},
		{"just under the default threshold", "", 6*time.Hour - time.Second, false},
		{"exactly the default threshold is not stale", "", 6 * time.Hour, false},
		{"just over the default threshold", "", 6*time.Hour + time.Second, true},
		{"a day old", "", 24 * time.Hour, true},
		{"1 hour setting, 59 minutes", "1", 59 * time.Minute, false},
		{"1 hour setting, 61 minutes", "1", 61 * time.Minute, true},
		{"48 hour setting, 47 hours", "48", 47 * time.Hour, false},
		{"48 hour setting, 49 hours", "48", 49 * time.Hour, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ta := newTestApp(t)
			id := ta.scrumStandup() // last success = baseTime
			ta.setPolicy("stale", tc.hours)
			ta.Clock.Advance(tc.age)

			noErr(t, runSyncOnOpen(t, ta), "RunSyncOnOpen(stale)")
			ta.waitAll(id)
			got := len(ta.openRuns())
			if tc.want && got != 1 || !tc.want && got != 0 {
				t.Errorf("age %v with sync_stale_hours=%q: %d open runs, want sync=%v", tc.age, tc.hours, got, tc.want)
			}
		})
	}
}

func TestSyncOnOpen_StaleAlwaysSyncsAStandupThatNeverSynced(t *testing.T) {
	ta := newTestApp(t)
	fresh := ta.scrumStandup()
	never := ta.kanbanStandup()
	ta.setPolicy("stale", "")

	noErr(t, runSyncOnOpen(t, ta), "RunSyncOnOpen(stale)")
	ta.waitAll(fresh, never)
	runs := ta.openRuns()
	if len(runs) != 1 || runs[0].StandupID != never {
		t.Errorf("open runs = %+v, want only the never-synced standup %d (the fresh one is skipped)", runs, never)
	}
}

func TestSyncOnOpen_OfflineLaunchIsSilentAndKeepsTheCachedView(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	tickets := len(ta.view(id).Tickets)
	ta.setPolicy("always", "")
	ta.Net.down.Store(true)

	noErr(t, runSyncOnOpen(t, ta), "RunSyncOnOpen(offline) must not return the run's failure")
	ta.waitAll(id)
	st, _ := ta.Svc.Sync.Status(ctx)
	if len(st) != 1 || st[0].LastStatus != "failed" || st[0].ErrorCode != "offline" {
		t.Errorf("Status = %+v, want a failed/offline open run recorded", st)
	}
	if got := len(ta.view(id).Tickets); got != tickets {
		t.Errorf("the cached view changed: %d -> %d tickets", tickets, got)
	}
}

func TestSyncOnOpen_WithoutAConnectionOrStandupsIsAQuietNoOp(t *testing.T) {
	ta := newTestApp(t)
	ta.setPolicy("always", "")
	mark := ta.networkCalls()
	ta.Fake.ResetRequests()
	noErr(t, runSyncOnOpen(t, ta), "RunSyncOnOpen(nothing configured)")
	ta.assertNoNetwork(mark, "sync-on-open with nothing configured")
	if n := ta.runRows(); n != 0 {
		t.Errorf("%d runs were created", n)
	}
}

func TestSyncOnOpen_IsSuppressedWhileTheTokenIsRejected(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	ta.Fake.SetUnauthorized(true)
	_, err := ta.Svc.Sync.Start(ctx, id, "incremental") // discovers the 401: the connection turns invalid
	noErr(t, err, "Start")
	ta.Orch.Wait(id)
	ta.setPolicy("always", "")
	runs := ta.runRows()
	mark := ta.networkCalls()

	noErr(t, runSyncOnOpen(t, ta), "RunSyncOnOpen(invalid connection)")
	ta.waitAll(id)
	if got := ta.runRows(); got != runs {
		t.Errorf("sync-on-open created %d runs while the connection is invalid", got-runs)
	}
	if n := ta.networkCalls() - mark; n != 0 {
		t.Errorf("sync-on-open made %d HTTP attempts while the connection is invalid", n)
	}
}
