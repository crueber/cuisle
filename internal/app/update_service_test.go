package app_test

// UpdateService (ADR 0016): local-only Status, gated scheduled checks, Check now, and no network in builds
// without a channel.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/update"
)

type countingReleases struct {
	mu    sync.Mutex
	calls int
	rs    []update.Release
}

func (c *countingReleases) List(context.Context) ([]update.Release, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.rs, nil
}

func (c *countingReleases) n() int { c.mu.Lock(); defer c.mu.Unlock(); return c.calls }

func TestUpdateService_NoUpdaterMeansNoChannelAndNoInstall(t *testing.T) {
	ta := newTestApp(t)
	mark := ta.networkCalls()
	st, err := ta.Svc.Update.Status(ctx)
	noErr(t, err, "Status")
	if st.Channel != "" || st.Available != nil || st.Phase != update.PhaseIdle {
		t.Fatalf("status %+v", st)
	}
	st, err = ta.Svc.Update.CheckNow(ctx)
	noErr(t, err, "CheckNow")
	if st.Channel != "" {
		t.Fatalf("status %+v", st)
	}
	ta.Svc.CheckForUpdates(ctx, update.TriggerStart)
	ta.Svc.CheckForUpdates(ctx, update.TriggerDayRolled)
	wantCode(t, ta.Svc.Update.Install(ctx), apperr.Unsupported, "Install")
	ta.assertNoNetwork(mark, "UpdateService without a channel")
}

func TestUpdateService_DailyGateFollowsTheSettingAndEmitsStatus(t *testing.T) {
	ta := newTestApp(t)
	rel := &countingReleases{rs: []update.Release{{
		Tag: "v1.3.0", PageURL: "https://example.test/r/v1.3.0",
		Assets: []update.Asset{{Name: "Cuisle-v1.3.0-macos-universal.dmg", URL: "https://example.test/a.dmg"}, {Name: update.SumsName, URL: "https://example.test/s"}},
	}}}
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	ta.Deps.Updater = update.New(update.Options{
		Channel: update.ChannelGitHub, ReleaseAPI: "https://example.test/api", CurrentVersion: "v1.2.0", GOOS: "darwin", GOARCH: "arm64",
		Releases: rel, Enabled: ta.Deps.Settings.UpdateCheckDaily, Now: func() time.Time { return now }, Location: time.UTC,
		Notify: func(s update.Status) { ta.Em.Emit(app.EvUpdateStatus, s) },
	})

	// Status is local: no request
	st, err := ta.Svc.Update.Status(ctx)
	noErr(t, err, "Status")
	if rel.n() != 0 || st.Channel != update.ChannelGitHub || !st.Enabled {
		t.Fatalf("status %+v, %d requests", st, rel.n())
	}

	// setting off: scheduled checks send nothing
	_, err = ta.Svc.App.SetAppSetting(ctx, settings.KeyUpdateCheckDaily, "off")
	noErr(t, err, "SetAppSetting off")
	ta.Svc.CheckForUpdates(ctx, update.TriggerStart)
	ta.Svc.CheckForUpdates(ctx, update.TriggerDayRolled)
	if rel.n() != 0 {
		t.Fatalf("checked with the setting off: %d", rel.n())
	}
	// setting on: one request on start, none again the same day
	_, err = ta.Svc.App.SetAppSetting(ctx, settings.KeyUpdateCheckDaily, "on")
	noErr(t, err, "SetAppSetting on")
	ta.Svc.CheckForUpdates(ctx, update.TriggerStart)
	ta.Svc.CheckForUpdates(ctx, update.TriggerStart)
	ta.Svc.CheckForUpdates(ctx, update.TriggerDayRolled)
	if rel.n() != 1 {
		t.Fatalf("requests %d, want 1", rel.n())
	}
	if len(ta.Em.named(app.EvUpdateStatus)) == 0 {
		t.Fatal("no update:status event")
	}
	st, _ = ta.Svc.Update.Status(ctx)
	if st.Available == nil || st.Available.Version != "v1.3.0" {
		t.Fatalf("available %+v", st.Available)
	}
	// Check now always asks
	_, err = ta.Svc.Update.CheckNow(ctx)
	noErr(t, err, "CheckNow")
	if rel.n() != 2 {
		t.Fatalf("check now: %d", rel.n())
	}
}
