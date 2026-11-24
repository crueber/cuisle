package app

import (
	"context"
	"log/slog"
	"net/http"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/crueber/cuisle/internal/media"
	"github.com/crueber/cuisle/internal/paths"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/sink"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/store"
	syncpkg "github.com/crueber/cuisle/internal/sync"
)

// Deps are the shared dependencies of every service, built by cmd/cuisle.
type Deps struct {
	DB         *store.DB
	Settings   *settings.Store
	Keyring    secrets.Keyring
	Sync       *syncpkg.Orchestrator
	Sinks      sink.Dispatcher
	Emitter    syncpkg.Emitter
	Clock      syncpkg.Clock
	Logger     *slog.Logger
	Paths      paths.Paths
	SourceOpts source.Options
	HTTP       *http.Client
	OpenURL    func(url string) error         // opens the OS browser
	Recovered  bool                           // store.OpenInfo.Recovered
	PerfSink   func(marks map[string]float64) // --bench-startup collector; nil in normal builds
	Now        func() time.Time
	Media      media.Cache             // attachment cache (ADR 0015); nil = no media cache (originals never stored)
	OpenFile   func(path string) error // opens a file with the OS default app (OpenAttachment); nil = unsupported

	// Ticket modal state (WP-T9): per-issue write locks, the Refresh cooldown, the last network verdict and the
	// background work (thumbnails after a Refresh) that Services.Shutdown waits for.
	ticketMu  gosync.Mutex
	busy      map[int64]struct{}
	refreshAt map[int64]time.Time
	offlineAt atomic.Int64 // unix ms of the last offline verdict, 0 = online (see noteNet and Deps.isOffline)
	bg        gosync.WaitGroup
	bgCtx     context.Context
	bgCancel  context.CancelFunc
}

// Services is every bound service, sharing Deps.
type Services struct {
	App        *AppService
	Standup    *StandupService
	Config     *ConfigService
	Connection *ConnectionService
	Sync       *SyncService
	Sink       *SinkService
	History    *HistoryService
	Ticket     *TicketService
}

// NewServices constructs all services over d.
func NewServices(d *Deps) *Services {
	return &Services{
		App: &AppService{d: d}, Standup: &StandupService{d: d}, Config: &ConfigService{d: d},
		Connection: &ConnectionService{d: d}, Sync: &SyncService{d: d}, Sink: &SinkService{d: d},
		History: &HistoryService{d: d}, Ticket: &TicketService{d: d},
	}
}
