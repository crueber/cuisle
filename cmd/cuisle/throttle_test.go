package main

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source/jira"
	"github.com/crueber/cuisle/internal/store/storetest"
	syncpkg "github.com/crueber/cuisle/internal/sync"
)

type recEmit struct {
	mu sync.Mutex
	ev []string
	pl []any
}

func (r *recEmit) Emit(name string, payload any) {
	r.mu.Lock()
	r.ev = append(r.ev, name)
	r.pl = append(r.pl, payload)
	r.mu.Unlock()
}

func TestThrottleBridge_LimiterThrottleBecomesSyncThrottled(t *testing.T) {
	db := storetest.Open(t, time.Now)
	rec := &recEmit{}
	b := &throttleBridge{inner: rec, db: db}
	b.attach() // no connection yet: nothing to hook, no panic

	row := cache.ConnRow{Source: "jira", BaseURL: "https://throttle-test.example.net", Account: "me@example.com", AuthState: "ok", CreatedAt: time.Now()}
	if _, err := cache.SaveConnection(context.Background(), db.W, row); err != nil {
		t.Fatal(err)
	}
	b.Emit(syncpkg.EvConnectionAuth, syncpkg.ConnectionAuth{State: "ok"}) // Connect re-attaches
	resume := time.Now().Add(3 * time.Second)
	jira.LimiterFor(model.Connection{BaseURL: row.BaseURL, Account: row.Account}).CloseGate(resume)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	var got *syncpkg.SyncThrottled
	for i, n := range rec.ev {
		if n == syncpkg.EvSyncThrottled {
			v := rec.pl[i].(syncpkg.SyncThrottled)
			got = &v
		}
	}
	if got == nil {
		t.Fatalf("no sync:throttled in %v", rec.ev)
	}
	if got.ResumeAt != resume.UnixMilli() || got.Reason == "" {
		t.Errorf("sync:throttled = %+v, want resumeAt %d", got, resume.UnixMilli())
	}
	if rec.ev[0] != syncpkg.EvConnectionAuth {
		t.Errorf("the original event was not forwarded first: %v", rec.ev)
	}
}

func TestApplyMaxRPS_IgnoresInvalidValues(t *testing.T) {
	db := storetest.Open(t, time.Now)
	st := settings.New(db, time.Now)
	prev := jira.DefaultPolicy()
	t.Cleanup(func() { jira.SetDefaultPolicy(prev) })
	applyMaxRPS(context.Background(), st, slog.New(slog.DiscardHandler)) // unset: no change
	if jira.DefaultPolicy() != prev {
		t.Fatal("an unset jira.max_rps replaced the policy")
	}
	if _, err := st.SetAppSetting(context.Background(), settings.KeyJiraMaxRPS, "5"); err != nil {
		t.Fatal(err)
	}
	applyMaxRPS(context.Background(), st, slog.New(slog.DiscardHandler))
	if jira.DefaultPolicy() == prev {
		t.Fatal("jira.max_rps=5 did not replace the policy")
	}
}
