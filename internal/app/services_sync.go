package app

import (
	"context"

	syncpkg "github.com/crueber/cuisle/internal/sync"
	"github.com/crueber/cuisle/internal/view"
)

type SyncService struct{ d *Deps }

func (s *SyncService) Start(ctx context.Context, standupID int64, mode string) (run int64, err error) {
	defer s.d.guard("Start", &err)
	// Start begins a manual sync and returns the run id at once. A run's own failure (offline, a rejected token mid-run,
	// cancellation) is reported through Status and sync:finished, never as an error here.
	return s.d.Sync.Start(ctx, standupID, mode, syncpkg.TriggerManual)
}

func (s *SyncService) Cancel(ctx context.Context, standupID int64) (err error) {
	defer s.d.guard("Cancel", &err)
	return s.d.Sync.Cancel(standupID)
}

func (s *SyncService) Status(ctx context.Context) (out []view.SyncStatus, err error) {
	defer s.d.guard("Status", &err)
	return s.d.syncStatuses(ctx)
}
