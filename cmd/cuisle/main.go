// Command cuisle is the desktop app: flags, paths, logger, store, adapter registration, services, window.
// WP-35 adds the --bench-startup measurement (the flag is parsed here).
package main

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/crueber/cuisle/frontend"
	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/buildinfo"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/logx"
	"github.com/crueber/cuisle/internal/media"
	"github.com/crueber/cuisle/internal/paths"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/sink"
	"github.com/crueber/cuisle/internal/sink/file"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/source/jira"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
	"github.com/crueber/cuisle/internal/store"
	syncpkg "github.com/crueber/cuisle/internal/sync"
)

// syncOnOpenDelay is how long after the window's runtime is ready the sync-on-open policy runs (architecture 5.1).
const syncOnOpenDelay = 300 * time.Millisecond

func main() { os.Exit(run()) }

func run() int {
	opts, err := parseOptions(os.Args[1:], os.Getenv)
	if err != nil {
		return 2
	}
	var probe *benchProbe // non-nil under --bench-startup
	if opts.benchStartup {
		probe = newBenchProbe(procStart)
	}

	fake := opts.fakeJira != ""
	if fake && os.Getenv(paths.EnvDataDir) == "" {
		// The fake's port and token are throwaway: keep its data away from the real database.
		dir, err := filepath.Abs(".dev-data")
		if err != nil {
			return fail("cannot resolve the dev data folder", err)
		}
		_ = os.Setenv(paths.EnvDataDir, dir)
	}
	p, err := paths.Default()
	if err != nil {
		return failStartup(opts, "cannot prepare the data folders", err)
	}
	dev := buildinfo.Get().Dev
	logger, logCloser, err := logx.New(logx.Options{Dir: p.Logs, Level: logx.LevelFromEnv(os.Getenv(logx.EnvLevel)), Dev: dev})
	if err != nil {
		return failStartup(opts, "cannot open the log", err)
	}
	logger.Info("starting", "version", buildinfo.Version, "fake", fake, "data", p.Data)

	startup := context.Background()
	db, info, err := store.Open(startup, p.DB(), store.Options{Logger: logger, Dev: dev})
	if err != nil {
		logger.Error("cannot open the database", "err", err)
		return failStartup(opts, "cannot open the database", err)
	}
	if n, err := db.MarkInterruptedRuns(startup, time.Now()); err != nil {
		logger.Warn("could not mark interrupted runs", "err", err)
	} else if n > 0 {
		logger.Info("marked interrupted runs failed", "rows", n)
	}
	if err := cache.Trim(startup, db.W, time.Now()); err != nil {
		logger.Warn("could not trim old operational rows", "err", err)
	}
	if _, err := db.WeeklyBackup(startup, time.Now()); err != nil {
		logger.Warn("weekly backup failed", "err", err)
	}

	if probe != nil {
		probe.Mark("db_ready")
	}

	var kr secrets.Keyring
	if fake {
		kr = secrets.NewMemory()
	} else {
		kr = secrets.WithEnvToken(secrets.NewSystem(), os.Getenv)
	}
	source.Register(jira.Kind, jira.New, jira.IssueURL)
	sink.Register(file.New())

	clock := app.SystemClock{}
	wailsEmitter := &app.WailsEmitter{} // App is set once the application exists
	throttle := &throttleBridge{inner: wailsEmitter, db: db, logger: logger}
	var emitter syncpkg.Emitter = throttle
	applyMaxRPS(startup, settings.New(db, time.Now), logger)
	httpClient := newHTTPClient()
	srcOpts := source.Options{HTTP: httpClient, Logger: logger, Now: time.Now, UserAgent: buildinfo.UserAgent()}

	var wapp *application.App
	deps := &app.Deps{
		DB: db, Settings: settings.New(db, time.Now), Keyring: kr, Emitter: emitter, Clock: clock, Logger: logger,
		Paths: p, SourceOpts: srcOpts, HTTP: httpClient, Recovered: info.Recovered, Now: time.Now,
		OpenURL: func(u string) error { return openURL(wapp, u) },
	}
	if probe != nil {
		deps.PerfSink = probe.Sink
	}
	deps.Media = media.New(filepath.Join(p.Data, "media"), db, app.MediaCap(deps.Settings), media.Options{Logger: logger})
	deps.OpenFile = func(path string) error { return openFile(wapp, path) }
	deps.Sync = syncpkg.New(syncpkg.Options{
		DB: db, Keyring: kr, Emitter: emitter, Clock: clock, Logger: logger, SourceOpts: srcOpts,
		Media: app.NewMediaStore(deps.Media),
		AfterSync: func(ctx context.Context, standupID, runID int64) {
			if err := deps.Sinks.AfterSync(ctx, standupID, runID); err != nil {
				logger.Warn("after-sync delivery", "standup", standupID, "run", runID, "err", err)
			}
		},
	})
	deps.Sinks = sink.NewDispatcher(db, kr, app.NewSinkBuild(deps), sink.DispatcherOptions{
		Logger: logger, Now: time.Now, Notify: app.SinkNotifier(emitter),
	})
	app.RegisterEvents()
	svcs := app.NewServices(deps)
	throttle.attach() // an existing connection's limiter feeds sync:throttled; Connect re-attaches (connection:auth)

	var fakeSrv *jiratest.Server
	if fake && !benchSeeded(startup, svcs, probe) {
		var fx *jiratest.Fixture
		if fakeSrv, fx, err = startFakeJira(opts.fakeJira, storedFakeAddr(startup, db)); err != nil {
			logger.Error("cannot start the fake jira", "err", err)
			return fail("cannot start the fake Jira", err)
		}
		logger.Info("fake jira", "url", fakeSrv.URL)
		if err := seedFake(startup, svcs, fakeSrv, fx); err != nil {
			logger.Error("cannot set up the fake jira", "err", err)
			return fail("cannot set up the fake Jira", err)
		}
	}

	dist, err := fs.Sub(frontend.Dist, "dist")
	if err != nil {
		return fail("cannot read the embedded UI", err)
	}

	appCtx, cancelApp := context.WithCancel(context.Background())
	var (
		tracker    *windowTracker
		dayRoll    *dayTimer
		openTimer  *time.Timer
		openMu     sync.Mutex
		readyOnce  sync.Once
		cleanOnce  sync.Once
		cleanupAll = func() {
			cleanOnce.Do(func() {
				cancelApp()
				openMu.Lock()
				if openTimer != nil {
					openTimer.Stop()
				}
				openMu.Unlock()
				if dayRoll != nil {
					dayRoll.Stop()
				}
				if tracker != nil && probe == nil { // a bench run must not rewrite its data dir's window state
					tracker.stopSaving()
					if ws, ok := tracker.snapshot(); ok {
						if err := svcs.App.SetUIState(context.Background(), app.UIStatePatch{Window: &ws}); err != nil {
							logger.Warn("could not save the window state", "err", err)
						}
					}
				}
				stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := deps.Sync.Shutdown(stop); err != nil {
					logger.Warn("sync did not stop in time", "err", err)
				}
				if err := svcs.Shutdown(stop); err != nil {
					logger.Warn("background work did not stop in time", "err", err)
				}
				if fakeSrv != nil {
					fakeSrv.Close()
				}
				// The database gets its own fresh budget: Shutdown may have used up the one above.
				closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer closeCancel()
				if err := db.Close(closeCtx); err != nil {
					logger.Warn("closing the database", "err", err)
				}
				logger.Info("stopped")
				_ = logCloser.Close()
			})
		}
	)

	wapp = application.New(application.Options{
		Name:        "Cuisle",
		Description: "Standups from your Jira boards",
		Logger:      logger,
		Services:    svcs.Bound(),
		Assets: application.AssetOptions{
			Handler:    application.AssetFileServerFS(dist),
			Middleware: app.AssetMiddleware(svcs),
		},
		MarshalError:   apperr.Marshal,
		SingleInstance: &application.SingleInstanceOptions{UniqueID: "crueber.cuisle"},
		Mac:            application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		OnShutdown:     cleanupAll,
	})
	wailsEmitter.App = wapp

	var saved *app.WindowState
	theme := settings.AppDefaults[settings.KeyTheme]
	if ui, err := deps.Settings.UIState(startup); err == nil {
		saved = ui.Window
	}
	if prefs, err := deps.Settings.AppSettings(startup); err == nil {
		theme = prefs[settings.KeyTheme]
	}
	wopts := windowOptions(saved, backgroundFor(theme, wapp.Env.IsDarkMode), dev)
	win := wapp.Window.NewWithOptions(wopts)
	tracker = trackWindow(win, saved, wopts.Width, wopts.Height)
	if probe == nil {
		tracker.setSave(func(ws app.WindowState) {
			if err := svcs.App.SetUIState(appCtx, app.UIStatePatch{Window: &ws}); err != nil && appCtx.Err() == nil {
				logger.Warn("could not save the window state", "err", err)
			}
		})
	}
	win.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		readyOnce.Do(func() {
			tracker.calibrate()
			if probe != nil {
				return // bench mode never syncs
			}
			go func() { // orphan sweep and cap check of the image cache, off the cold-start path (ADR 0015)
				if _, err := deps.Media.Evict(appCtx); err != nil && appCtx.Err() == nil {
					logger.Warn("image cache cleanup failed", "err", err)
				}
			}()
			openMu.Lock()
			defer openMu.Unlock()
			openTimer = time.AfterFunc(syncOnOpenDelay, func() {
				if err := svcs.RunSyncOnOpen(appCtx); err != nil {
					logger.Warn("sync on open", "err", err)
				}
			})
		})
	})
	if probe != nil {
		win.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) { probe.Mark("window_shown") })
		go func() {
			rep, code := probe.finish(appCtx, benchTimeout, loadViews(svcs), readMem, fake)
			writeReport(os.Stdout, rep)
			cleanupAll()
			os.Exit(code)
		}()
	} else {
		dayRoll = startDayTimer(clock, emitter)
	}

	err = wapp.Run()
	if err != nil {
		logger.Error("application error", "err", err) // before cleanupAll closes the log file
	}
	cleanupAll()
	if err != nil {
		return fail("the application stopped with an error", err)
	}
	return 0
}

// newHTTPClient is the one shared client for every source request (jira-sync.md section 1: 10 s connect, 30 s per
// request, keep-alive).
func newHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.IdleConnTimeout = 90 * time.Second
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}

// openURL opens an http(s) or mailto link in the OS browser or mail client through Wails.
func openURL(a *application.App, raw string) error {
	u, err := url.Parse(raw)
	web := u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
	mail := u != nil && u.Scheme == "mailto" && (u.Opaque != "" || u.Path != "")
	if err != nil || !(web || mail) {
		return apperr.New(apperr.Validation, "Only web and mail links can be opened.")
	}
	if a == nil {
		return apperr.New(apperr.Internal, "The application is not running.")
	}
	return a.Browser.OpenURL(raw)
}

// fail reports a startup problem on stderr (the log may not exist yet) and returns the exit code.
func fail(what string, err error) int {
	fmt.Fprintf(os.Stderr, "cuisle: %s: %v\n", what, err)
	return 1
}

// openFile opens a downloaded attachment with the OS default application through Wails.
func openFile(a *application.App, path string) error {
	if a == nil {
		return apperr.New(apperr.Internal, "The application is not running.")
	}
	return a.Browser.OpenFile(path)
}
