package app

import (
	"context"

	"github.com/crueber/cuisle/internal/buildinfo"
	"github.com/crueber/cuisle/internal/update"
)

// EvUpdateStatus carries update.Status whenever a check or an install changes it (ADR 0016).
const EvUpdateStatus = "update:status"

// UpdateService is the auto-updater (ADR 0016). Status is local only; CheckNow sends one releases-list request;
// Install downloads, verifies and installs the newer release, then quits so the new version starts. Builds without
// an update channel (local, dev, fake and bench runs) have a nil Deps.Updater: Status reports no channel, nothing
// is ever requested and Install is refused.
type UpdateService struct{ d *Deps }

func (s *UpdateService) Status(ctx context.Context) (out update.Status, err error) {
	defer s.d.guard("UpdateStatus", &err)
	return s.d.updateStatus(ctx), nil
}

func (s *UpdateService) CheckNow(ctx context.Context) (out update.Status, err error) {
	defer s.d.guard("UpdateCheckNow", &err)
	if s.d.Updater == nil {
		return s.d.updateStatus(ctx), nil
	}
	return s.d.Updater.Check(ctx, update.TriggerManual)
}

func (s *UpdateService) Install(ctx context.Context) (err error) {
	defer s.d.guard("UpdateInstall", &err)
	return s.d.Updater.Install(ctx)
}

func (d *Deps) updateStatus(ctx context.Context) update.Status {
	if d.Updater == nil {
		return update.Status{CurrentVersion: buildinfo.Version, Enabled: true, Phase: update.PhaseIdle}
	}
	return d.Updater.Status(ctx)
}

// CheckForUpdates runs a scheduled check (app start, day rollover). The daily gate inside the Updater decides
// whether a request is sent; failures are logged and shown through update:status, never retried.
func (s *Services) CheckForUpdates(ctx context.Context, t update.Trigger) {
	d := s.App.d
	if d.Updater == nil {
		return
	}
	if _, err := d.Updater.Check(ctx, t); err != nil {
		d.log().Warn("update check", "trigger", string(t), "err", err)
	}
}
