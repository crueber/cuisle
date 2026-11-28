package main

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source/jira"
	"github.com/crueber/cuisle/internal/source/jira/limiter"
	"github.com/crueber/cuisle/internal/store"
	syncpkg "github.com/crueber/cuisle/internal/sync"
)

// throttleBridge forwards every event to the Wails emitter and feeds the connection limiter's throttle events to
// sync:throttled (jira-sync.md §3.1). The limiter lives in source/jira, which sync and app may not import, so the bridge
// is here. It re-attaches on connection:auth because Connect may create the limiter after startup.
type throttleBridge struct {
	inner syncpkg.Emitter
	db    *store.DB
}

func (b *throttleBridge) Emit(name string, payload any) {
	b.inner.Emit(name, payload)
	if name == syncpkg.EvConnectionAuth {
		b.attach()
	}
}

// attach hooks the stored connection's limiter, if there is one. Idempotent: the hook is replaced, not stacked.
func (b *throttleBridge) attach() {
	row, err := cache.GetConnection(context.Background(), b.db.R)
	if err != nil || row.Source != jira.Kind {
		return // no Jira connection yet
	}
	jira.LimiterFor(model.Connection{Source: row.Source, BaseURL: row.BaseURL, Account: row.Account}).SetOnThrottle(func(e limiter.ThrottleEvent) {
		var resume int64
		if !e.ResumeAt.IsZero() {
			resume = e.ResumeAt.UnixMilli()
		}
		b.inner.Emit(syncpkg.EvSyncThrottled, syncpkg.SyncThrottled{ResumeAt: resume, Reason: e.Reason})
	})
}

// applyMaxRPS applies the hidden app setting jira.max_rps (1 to 20) as the process-wide request rate cap. It is read
// once at startup; AIMD can only lower the rate at run time, never above this.
func applyMaxRPS(ctx context.Context, st *settings.Store, logger *slog.Logger) {
	all, err := st.AppSettings(ctx)
	if err != nil {
		return
	}
	n, err := strconv.Atoi(all[settings.KeyJiraMaxRPS])
	if err != nil || n < 1 || n > 20 {
		return
	}
	jira.SetDefaultPolicy(jira.NewPolicy(jira.PolicyOptions{RatePerSecond: float64(n)}))
	logger.Info("request rate cap set", "rps", n)
}
