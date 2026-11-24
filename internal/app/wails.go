package app

import (
	"encoding/json"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"

	syncpkg "github.com/crueber/cuisle/internal/sync"
)

// RegisterEvents declares the typed events for the binding generator and runtime (call once, before application.New).
func RegisterEvents() {
	application.RegisterEvent[syncpkg.SyncStarted](syncpkg.EvSyncStarted)
	application.RegisterEvent[syncpkg.SyncProgress](syncpkg.EvSyncProgress)
	application.RegisterEvent[syncpkg.SyncFinished](syncpkg.EvSyncFinished)
	application.RegisterEvent[syncpkg.DataChanged](syncpkg.EvDataChanged)
	application.RegisterEvent[syncpkg.ConnectionAuth](syncpkg.EvConnectionAuth)
	application.RegisterEvent[SinkDelivered](EvSinkDelivered)
	application.RegisterEvent[SinkFailed](EvSinkFailed)
	application.RegisterEvent[DayRolled](EvDayRolled)
	application.RegisterEvent[syncpkg.TicketUpdated](syncpkg.EvTicketUpdated)
	application.RegisterEvent[TicketWriteFailed](EvTicketWriteFailed)
	application.RegisterEvent[syncpkg.MediaReady](syncpkg.EvMediaReady)
	application.RegisterEvent[syncpkg.SyncThrottled](syncpkg.EvSyncThrottled)
}

// BootMiddleware serves GET /boot.js as `window.__CUISLE_BOOT__ = <Bootstrap JSON>;` (architecture.md §5.1): the
// cached board is in the page before the first paint, with no IPC round trip. Everything else passes through.
func BootMiddleware(s *Services) application.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/boot.js" {
				next.ServeHTTP(w, r)
				return
			}
			s.serveBoot(w, r)
		})
	}
}

func (s *Services) serveBoot(w http.ResponseWriter, r *http.Request) {
	d := s.App.d
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	b, err := s.App.Bootstrap(r.Context())
	var body []byte
	if err == nil {
		body, err = json.Marshal(b)
	}
	if err != nil {
		// No boot object: the frontend falls back to AppService.Bootstrap() and shows the error itself.
		d.log().Error("boot.js: bootstrap failed", "err", err)
		_, _ = w.Write([]byte("/* bootstrap unavailable */\n"))
		return
	}
	_, _ = w.Write([]byte("window.__CUISLE_BOOT__ = "))
	_, _ = w.Write(body)
	_, _ = w.Write([]byte(";\n"))
}

// WailsEmitter adapts the Wails event manager to sync.Emitter.
type WailsEmitter struct{ App *application.App }

func (e WailsEmitter) Emit(name string, payload any) {
	if e.App != nil {
		e.App.Event.Emit(name, payload)
	}
}

// Bound returns the services for application.Options.Services.
func (s *Services) Bound() []application.Service {
	return []application.Service{
		application.NewService(s.App), application.NewService(s.Standup), application.NewService(s.Config),
		application.NewService(s.Connection), application.NewService(s.Sync), application.NewService(s.Ticket),
		application.NewService(s.Sink), application.NewService(s.History),
	}
}
