package app

import (
	"log/slog"
	"net/http"
	"time"

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
}

// NewServices constructs all services over d.
func NewServices(d *Deps) *Services {
	return &Services{
		App: &AppService{d: d}, Standup: &StandupService{d: d}, Config: &ConfigService{d: d},
		Connection: &ConnectionService{d: d}, Sync: &SyncService{d: d}, Sink: &SinkService{d: d},
		History: &HistoryService{d: d},
	}
}
