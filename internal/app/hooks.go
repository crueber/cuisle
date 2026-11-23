package app

import (
	"context"
	"strconv"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/buildinfo"
	"github.com/crueber/cuisle/internal/export"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/sink"
	syncpkg "github.com/crueber/cuisle/internal/sync"
	"github.com/crueber/cuisle/internal/view"
)

// FROZEN signatures (added after Wave 0.5 review).

// RunSyncOnOpen applies the sync-on-open policy once at startup (architecture §5.1, §6.4): reads sync_on_open and
// sync_stale_hours; "always" syncs every configured standup, "stale" those never synced or with now − last_success_at
// strictly greater than the threshold, "never" nothing; incremental runs with trigger "open", the visible
// (ui.lastStandupId) standup first. Returns once the runs are started; never returns a run's failure; a quiet no-op
// without a connection or with an invalid one. cmd/cuisle calls it 300 ms after the window's ready event.
// It is on Services (not AppService) so Wails does not bind it.
func (s *Services) RunSyncOnOpen(ctx context.Context) (err error) {
	d := s.App.d
	defer d.guard("RunSyncOnOpen", &err)
	if ctx.Err() != nil {
		return nil // shutting down before the policy ran
	}

	prefs, err := d.Settings.AppSettings(ctx)
	if err != nil {
		return err
	}
	policy := prefs[settings.KeySyncOnOpen]
	if policy != "always" && policy != "stale" {
		return nil
	}
	staleAfter := 6 * time.Hour
	if h, perr := strconv.Atoi(prefs[settings.KeySyncStaleHours]); perr == nil && h > 0 {
		staleAfter = time.Duration(h) * time.Hour
	}

	row, ok, err := d.connRow(ctx)
	if err != nil || !ok {
		return err
	}
	if row.AuthState == "invalid" || row.AuthState == "missing" {
		return nil
	}
	tabs, err := d.tabs(ctx)
	if err != nil {
		return err
	}
	ui, err := d.Settings.UIState(ctx)
	if err != nil {
		return err
	}
	visible := visibleStandup(tabs, ui.LastStandupID)

	nowMs := d.now().UnixMilli()
	order := make([]int64, 0, len(tabs))
	if visible != 0 {
		order = append(order, visible)
	}
	lastSynced := map[int64]int64{}
	for _, t := range tabs {
		lastSynced[t.ID] = t.LastSyncedAt
		if t.Configured && t.ID != visible {
			order = append(order, t.ID)
		}
	}
	for _, id := range order {
		if ctx.Err() != nil {
			return nil // shutting down: start nothing more
		}
		last := lastSynced[id]
		if policy == "stale" && last != 0 && nowMs-last <= staleAfter.Milliseconds() {
			continue // synced recently enough (exactly the threshold is not stale)
		}
		if _, err := d.Sync.Start(ctx, id, syncpkg.ModeIncremental, syncpkg.TriggerOpen); err != nil {
			d.log().Debug("sync on open: not started", "standup", id, "err", err)
		}
	}
	return nil
}

// NewSinkBuild returns the sink.BuildFunc the dispatcher uses: view.Build of the standup's live snapshot → export
// document (kind current; range → apperr not_implemented), its frozen JSON, the standup key (space key or
// "standup-<id>") and the local date.
func NewSinkBuild(d *Deps) sink.BuildFunc {
	return func(ctx context.Context, standupID int64, req sink.Request) (export.Document, []byte, string, string, error) {
		if req.Kind != "current" {
			return export.Document{}, nil, "", "", apperr.NotImplemented
		}
		v, err := d.buildView(ctx, standupID)
		if err != nil {
			return export.Document{}, nil, "", "", err
		}
		cfg, err := d.standupConfig(ctx, standupID)
		if err != nil {
			return export.Document{}, nil, "", "", err
		}
		meta := export.Meta{
			GeneratedAt: d.now(), Version: buildinfo.Version, Kind: req.Kind, StandupID: standupID, Name: v.Name,
			SpaceKey: v.SpaceKey, BoardName: v.BoardName, BoardKind: v.BoardType, Scope: v.Scope,
			Timezone: ianaLocation(d.location()),
		}
		if cfg.Space != nil {
			meta.SpaceName = cfg.Space.Name
		}
		if cfg.Board != nil {
			meta.BoardID = cfg.Board.ID
		}
		if row, ok, err := d.connRow(ctx); err == nil && ok {
			meta.SourceKind, meta.BaseURL = row.Source, row.BaseURL
		}
		doc := export.Build([]view.StandupView{v}, meta)
		raw, err := export.Encode(doc)
		if err != nil {
			return export.Document{}, nil, "", "", apperr.Wrap(apperr.Internal, "The export could not be built.", err)
		}
		key := v.SpaceKey
		if key == "" {
			key = "standup-" + strconv.FormatInt(standupID, 10)
		}
		return doc, raw, key, v.Today, nil
	}
}

// SinkNotifier maps dispatcher events to sink:delivered / sink:failed on e.
func SinkNotifier(e syncpkg.Emitter) func(sink.Event) {
	return func(ev sink.Event) {
		if e == nil {
			return
		}
		if ev.Delivered {
			e.Emit(EvSinkDelivered, SinkDelivered{OutboxID: ev.OutboxID, SinkID: ev.SinkID, StandupID: ev.StandupID, Kind: ev.Kind, Ref: ev.Ref})
			return
		}
		e.Emit(EvSinkFailed, SinkFailed{
			OutboxID: ev.OutboxID, SinkID: ev.SinkID, StandupID: ev.StandupID, Kind: ev.Kind, ErrorCode: ev.ErrorCode,
			ErrorMessage: ev.ErrorMessage, Attempts: ev.Attempts, WillRetry: ev.WillRetry,
		})
	}
}
