package main

import (
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/buildinfo"
	"github.com/crueber/cuisle/internal/settings"
	syncpkg "github.com/crueber/cuisle/internal/sync"
	"github.com/crueber/cuisle/internal/update"
)

// updaterConfig is what newUpdater reads besides buildinfo; a struct so tests can drive the "never" cases.
type updaterConfig struct {
	channel, releaseAPI, version, pubKey string
	fake, bench                          bool
	dataDir                              string
}

func buildUpdaterConfig(fake, bench bool, dataDir string) updaterConfig {
	return updaterConfig{
		channel: buildinfo.Channel, releaseAPI: buildinfo.ReleaseAPI, version: buildinfo.Version,
		pubKey: buildinfo.ReleasePubKey, fake: fake, bench: bench, dataDir: dataDir,
	}
}

// newUpdater builds the auto-updater (ADR 0016), or nil when this run must never check: a build without a channel
// (local `task build`/`task package`, dev), the fake Jira (task dev, tests) and --bench-startup.
func newUpdater(c updaterConfig, st *settings.Store, emitter syncpkg.Emitter, logger *slog.Logger, quit func()) *update.Updater {
	if c.channel == "" || c.fake || c.bench {
		return nil
	}
	ua := "Cuisle/" + c.version // the version only: nothing about the machine or the user (ADR 0016)
	client := newDownloadClient()
	rel := update.NewReleases(c.channel, c.releaseAPI, client, ua)
	if rel == nil {
		logger.Warn("update: unknown channel or no releases URL; updates are off", "channel", c.channel)
		return nil
	}
	exe, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
	}
	appImage := update.AppImagePath(os.Getenv)
	return update.New(update.Options{
		Channel: c.channel, CurrentVersion: c.version, PubKey: c.pubKey, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		AppImage: appImage, Releases: rel, ReleaseAPI: c.releaseAPI, HTTP: client, UserAgent: ua,
		State:   update.FileState{Path: filepath.Join(c.dataDir, "update.json")},
		Enabled: st.UpdateCheckDaily, Now: time.Now,
		Installer: &update.System{
			GOOS: runtime.GOOS, Exe: exe, PID: os.Getpid(), AppImage: appImage, Run: update.ExecRunner{},
			PowerShell: update.PowerShellPath(os.Getenv),
		},
		Quit:   quit,
		Notify: func(s update.Status) { emitter.Emit(app.EvUpdateStatus, s) },
		Logger: logger,
	})
}

// newDownloadClient is the updater's own client (releases list and downloads): no cookie jar and no overall timeout
// (a disk image takes longer than the shared client's 30 s); connect, TLS and first-byte timeouts still apply and
// contexts bound the rest. Every redirect hop must be https, at most update.MaxRedirects of them. Compression is
// off so the bytes hashed and the byte counts checked are exactly the asset's.
func newDownloadClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.ResponseHeaderTimeout = 30 * time.Second
	tr.DisableCompression = true
	return &http.Client{Transport: tr, CheckRedirect: update.RedirectPolicy(false)}
}
