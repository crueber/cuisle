package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
)

// Phase is where an update stands.
type Phase string

const (
	PhaseIdle        Phase = "idle"
	PhaseChecking    Phase = "checking"
	PhaseDownloading Phase = "downloading"
	PhaseVerifying   Phase = "verifying"
	PhaseInstalling  Phase = "installing"
	PhaseRestarting  Phase = "restarting"
	PhaseFailed      Phase = "failed"
)

// Trigger is why a check runs. Only these exist (AGENTS invariant 2): there is no timer in this package.
type Trigger string

const (
	TriggerStart     Trigger = "start"      // app start: checks when the last check is at least 24 h old
	TriggerDayRolled Trigger = "day_rolled" // the local-midnight event: checks when no check ran today
	TriggerManual    Trigger = "manual"     // Configure → Display → Check now: always checks
)

// CheckInterval is the minimum age of the last check before app start checks again.
const CheckInterval = 24 * time.Hour

// Error codes in Status.ErrorCode.
const (
	ErrCodeDownload    = "download"
	ErrCodeVerify      = "verify"
	ErrCodeInstall     = "install"
	ErrCodeUnsupported = "unsupported"
	ErrCodeRestart     = "restart" // installed, but the automatic restart failed: the user reopens Cuisle
)

// Available is the newer release offered to the user.
type Available struct {
	Version     string `json:"version"`
	PageURL     string `json:"pageUrl"`
	PublishedAt int64  `json:"publishedAt"` // unix ms
	// CanInstall is false when this copy cannot replace itself (a .deb install, a translocated or read-only
	// macOS app): the UI then offers the release page instead of Install and restart.
	CanInstall bool `json:"canInstall"`
}

// Status is the whole update state the UI shows (UpdateService.Status and the update:status event).
type Status struct {
	Channel        string     `json:"channel"` // "" = this build never checks and shows no Update button
	CurrentVersion string     `json:"currentVersion"`
	Enabled        bool       `json:"enabled"`       // the "Check for updates daily" setting
	LastCheckedAt  int64      `json:"lastCheckedAt"` // unix ms of the last check, 0 = never
	LastError      string     `json:"lastError"`     // why the last check failed, "" = it worked
	Available      *Available `json:"available"`     // nil = up to date (or not checked yet)
	Phase          Phase      `json:"phase"`
	Progress       int        `json:"progress"`  // download percent 0–100, -1 = size unknown
	Error          string     `json:"error"`     // the last install failure, for the dialog
	ErrorCode      string     `json:"errorCode"` // download|verify|install|unsupported
}

// Installer applies a verified file. Implementations touch the system (hdiutil, NSIS, the AppImage file).
type Installer interface {
	// Check returns nil when this copy can be replaced in place for kind, or an error saying why not.
	Check(kind Kind) error
	// Install applies the verified file and arranges the relaunch after this process exits. sha256Hex is the
	// verified checksum, for installers that run the file later (the Windows helper checks it again right before
	// it starts the installer). It returns nil once the new version is in place (or the installer is queued); the
	// Updater then quits the app.
	Install(ctx context.Context, kind Kind, file, sha256Hex string) error
}

// Options configure an Updater. A nil Releases, an empty Channel or a current version that is not vX.Y.Z disables
// every check: no request is ever made.
type Options struct {
	Channel        string
	CurrentVersion string
	PubKey         string // base64 ed25519 public key; when set the release's SHA256SUMS.txt.sig is required
	GOOS, GOARCH   string
	AppImage       string // $APPIMAGE when running from an AppImage
	Releases       Releases
	ReleaseAPI     string       // the channel's releases-list URL: its host (and GitHub's asset hosts) are the only download hosts
	HTTP           *http.Client // downloads (no overall timeout: the context bounds them); every redirect hop must be https
	UserAgent      string
	State          StateStore
	Enabled        func(ctx context.Context) bool // the daily-check setting; nil = on
	Now            func() time.Time
	Location       *time.Location // local day for the rollover gate; nil = time.Local
	Installer      Installer      // nil = no in-place install on this machine
	Quit           func()         // quits the app after a successful install
	Notify         func(Status)   // update:status
	Logger         *slog.Logger
	TempDir        string // where downloads go (not the AppImage); "" = os.TempDir()
	AllowHTTP      bool   // tests only: allow http:// download URLs
	MaxDownload    int64  // bytes; 0 = 512 MiB
}

// Updater runs checks and installs. Safe for concurrent use; checks and installs are serialised.
type Updater struct {
	o       Options
	current Version
	ok      bool

	opMu sync.Mutex // one check or install at a time

	mu       sync.Mutex
	state    State
	loaded   bool
	phase    Phase
	progress int
	err      string
	errCode  string
	canCache map[Kind]bool
}

// New builds an Updater. It reads nothing and sends nothing until a method is called.
func New(o Options) *Updater {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Location == nil {
		o.Location = time.Local
	}
	o.HTTP = withRedirectPolicy(o.HTTP, o.AllowHTTP)
	if o.State == nil {
		o.State = &MemState{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.MaxDownload <= 0 {
		o.MaxDownload = 512 << 20
	}
	u := &Updater{o: o, phase: PhaseIdle}
	u.current, u.ok = ParseVersion(o.CurrentVersion)
	if u.current.Pre != "" {
		u.ok = false // pre-release and dev builds do not update themselves
	}
	return u
}

// Supported reports whether this build checks for updates at all (a channel, a releases client and a version).
func (u *Updater) Supported() bool {
	return u != nil && u.ok && u.o.Channel != "" && u.o.Releases != nil
}

func (u *Updater) enabled(ctx context.Context) bool {
	return u.o.Enabled == nil || u.o.Enabled(ctx)
}

// loadLocked reads the persisted state once. The caller holds u.mu.
func (u *Updater) loadLocked() {
	if u.loaded {
		return
	}
	u.loaded = true
	s, err := u.o.State.Load()
	if err != nil {
		u.o.Logger.Warn("update: cannot read the update state", "err", err)
		return
	}
	u.state = s
}

// Status returns the current state (local only; never a request).
func (u *Updater) Status(ctx context.Context) Status {
	if u == nil {
		return Status{Phase: PhaseIdle}
	}
	enabled := u.enabled(ctx)
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.statusLocked(enabled)
}

func (u *Updater) statusLocked(enabled bool) Status {
	u.loadLocked()
	st := Status{
		CurrentVersion: u.o.CurrentVersion, Enabled: enabled, Phase: u.phase, Progress: u.progress,
		Error: u.err, ErrorCode: u.errCode,
	}
	if !u.Supported() {
		return st
	}
	st.Channel = u.o.Channel
	if !u.state.LastCheckAt.IsZero() {
		st.LastCheckedAt = u.state.LastCheckAt.UnixMilli()
	}
	st.LastError = u.state.LastError
	if f := u.state.Latest; f != nil {
		if v, ok := ParseVersion(f.Version); ok && v.Compare(u.current) > 0 {
			st.Available = &Available{
				Version: f.Version, PageURL: f.PageURL, PublishedAt: f.PublishedAt.UnixMilli(),
				CanInstall: u.canInstall(f.Kind),
			}
		}
	}
	return st
}

// canInstall is computed once per kind and cached: Installer.Check may touch the disk (a write test next to the
// app), and Status runs on every progress event. Install checks again before it starts. The caller holds u.mu.
func (u *Updater) canInstall(k Kind) bool {
	if k == KindPage || u.o.Installer == nil {
		return false
	}
	if v, ok := u.canCache[k]; ok {
		return v
	}
	if u.canCache == nil {
		u.canCache = map[Kind]bool{}
	}
	v := u.o.Installer.Check(k) == nil
	u.canCache[k] = v
	return v
}

func (u *Updater) notify(ctx context.Context) {
	if u.o.Notify == nil {
		return
	}
	u.o.Notify(u.Status(ctx))
}

// Due reports whether trigger would send a request now (the daily gate). Manual checks are always due.
func (u *Updater) Due(ctx context.Context, t Trigger) bool {
	if !u.Supported() {
		return false
	}
	if t == TriggerManual {
		return true
	}
	if !u.enabled(ctx) {
		return false
	}
	u.mu.Lock()
	u.loadLocked()
	last := u.state.LastCheckAt
	u.mu.Unlock()
	if last.IsZero() {
		return true
	}
	now := u.o.Now()
	switch t {
	case TriggerStart:
		return now.Sub(last) >= CheckInterval || now.Before(last) // a clock set backwards must not stop checks
	case TriggerDayRolled:
		y1, m1, d1 := last.In(u.o.Location).Date()
		y2, m2, d2 := now.In(u.o.Location).Date()
		return y1 != y2 || m1 != m2 || d1 != d2
	}
	return false
}

// Check runs one releases-list request when trigger is due, records the result and returns the new status.
// A check that is not due sends nothing and returns the current status.
func (u *Updater) Check(ctx context.Context, t Trigger) (Status, error) {
	if u == nil {
		return Status{Phase: PhaseIdle}, nil
	}
	u.opMu.Lock()
	defer u.opMu.Unlock()
	if !u.Due(ctx, t) {
		return u.Status(ctx), nil
	}
	u.setPhase(ctx, PhaseChecking, 0)
	found, err := u.lookup(ctx)
	if err != nil && errors.Is(err, context.Canceled) && ctx.Err() != nil {
		// App quit or caller cancelled mid-check: don't burn the daily quota or persist a false offline error.
		u.mu.Lock()
		u.phase, u.progress = PhaseIdle, 0
		u.mu.Unlock()
		u.notify(context.WithoutCancel(ctx))
		return u.Status(context.WithoutCancel(ctx)), nil
	}
	now := u.o.Now()

	u.mu.Lock()
	u.state.LastCheckAt = now // a failed attempt counts too: at most one request a day
	if err != nil {
		u.state.LastError = "Couldn't reach the release server."
	} else {
		u.state.LastError = ""
		u.state.Latest = found
	}
	saveErr := u.o.State.Save(u.state)
	u.phase, u.progress = PhaseIdle, 0
	u.mu.Unlock()
	if saveErr != nil {
		u.o.Logger.Warn("update: cannot save the update state", "err", saveErr)
	}
	if err != nil {
		u.o.Logger.Warn("update: check failed", "trigger", string(t), "err", err)
	} else if found != nil {
		u.o.Logger.Info("update: newer release available", "trigger", string(t), "version", found.Version)
	} else {
		u.o.Logger.Info("update: up to date", "trigger", string(t))
	}
	u.notify(ctx)
	st := u.Status(ctx)
	if err != nil && t == TriggerManual {
		return st, apperr.Wrap(apperr.Offline, "Couldn't reach the release server.", err)
	}
	return st, nil
}

// lookup lists the releases and returns the newest one this machine can use, or nil when there is none newer.
// A release counts only once this platform's asset and SHA256SUMS.txt are both attached (the release jobs create
// the release first and upload the files after).
func (u *Updater) lookup(ctx context.Context) (*Found, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rs, err := u.o.Releases.List(ctx)
	if err != nil {
		return nil, err
	}
	var stable []Release
	for _, r := range rs {
		if r.Draft || r.Prerelease {
			continue
		}
		v, ok := ParseVersion(r.Tag)
		if !ok || v.Pre != "" || v.Compare(u.current) <= 0 {
			continue
		}
		r.Version = v
		stable = append(stable, r)
	}
	// newest first; a newest release still missing files falls back to the next complete one
	slices.SortFunc(stable, func(a, b Release) int { return b.Version.Compare(a.Version) })
	for _, best := range stable {
		asset, kind, ok := SelectAsset(best.Assets, u.o.GOOS, u.o.GOARCH, u.o.AppImage != "")
		sums, hasSums := best.Asset(SumsName)
		if ok && hasSums {
			f := &Found{
				Version: best.Version.String(), PageURL: best.PageURL, PublishedAt: best.PublishedAt,
				Kind: kind, Asset: asset, Sums: sums,
			}
			if sig, ok := best.Asset(SigName); ok {
				f.Sig = &sig
			}
			if u.usable(f) == nil {
				return f, nil
			}
			u.o.Logger.Warn("update: skipping a release whose files don't fit its tag or host", "version", f.Version)
		}
	}
	return nil, nil
}

func (u *Updater) setPhase(ctx context.Context, p Phase, progress int) {
	u.mu.Lock()
	u.phase, u.progress = p, progress
	if p != PhaseFailed {
		u.err, u.errCode = "", ""
	}
	u.mu.Unlock()
	u.notify(ctx)
}

func (u *Updater) fail(ctx context.Context, code, msg string, cause error) error {
	u.mu.Lock()
	u.phase, u.progress, u.err, u.errCode = PhaseFailed, 0, msg, code
	u.mu.Unlock()
	u.o.Logger.Warn("update: install failed", "code", code, "err", cause)
	u.notify(ctx)
	apCode := apperr.Internal
	switch code {
	case ErrCodeUnsupported:
		apCode = apperr.Unsupported
	case ErrCodeDownload:
		apCode = apperr.Offline
	}
	return apperr.Wrap(apCode, msg, cause)
}

// Install downloads the available release for this platform, verifies it and installs it. Nothing is run or
// installed unless verification passed. On success the app quits (Options.Quit) and the new version starts.
func (u *Updater) Install(ctx context.Context) error {
	if !u.Supported() {
		return apperr.New(apperr.Unsupported, "This copy of Cuisle doesn't update itself.")
	}
	if !u.opMu.TryLock() {
		return apperr.New(apperr.Conflict, "An update check or install is already running.")
	}
	defer u.opMu.Unlock()

	u.mu.Lock()
	u.loadLocked()
	f := u.state.Latest
	u.mu.Unlock()
	if f != nil && u.usable(f) != nil {
		// update.json is not trusted blindly: a stored release whose URLs or names don't fit the channel is
		// looked up again (Install is an explicit trigger) and dropped if it still doesn't fit.
		u.o.Logger.Warn("update: stored release does not fit the channel; looking it up again", "version", f.Version)
		fresh, err := u.lookup(ctx)
		if err != nil {
			return u.fail(ctx, ErrCodeDownload, "Couldn't reach the release server.", err)
		}
		u.mu.Lock()
		u.state.Latest = fresh
		if serr := u.o.State.Save(u.state); serr != nil {
			u.o.Logger.Warn("update: cannot save the update state", "err", serr)
		}
		u.mu.Unlock()
		f = fresh
	}
	if f == nil {
		return apperr.New(apperr.NotFound, "There is no newer version to install.")
	}
	if v, ok := ParseVersion(f.Version); !ok || v.Compare(u.current) <= 0 {
		return apperr.New(apperr.NotFound, "There is no newer version to install.")
	}
	if f.Kind == KindPage || u.o.Installer == nil {
		return u.fail(ctx, ErrCodeUnsupported, "This copy can't update itself. Download the new version from the release page.", nil)
	}
	if err := u.o.Installer.Check(f.Kind); err != nil {
		u.mu.Lock()
		u.canCache = map[Kind]bool{f.Kind: false}
		u.mu.Unlock()
		return u.fail(ctx, ErrCodeUnsupported, "This copy can't update itself here. Download the new version from the release page.", err)
	}

	u.setPhase(ctx, PhaseDownloading, 0)
	sums, sig, err := u.fetchSums(ctx, f)
	if err != nil {
		if errors.Is(err, ErrVerify) {
			return u.fail(ctx, ErrCodeVerify, "Couldn't verify the download, so nothing was installed.", err)
		}
		return u.fail(ctx, ErrCodeDownload, "Couldn't download the update. Check your connection and try again.", err)
	}
	if u.o.PubKey != "" {
		if sig == nil {
			return u.fail(ctx, ErrCodeVerify, "Couldn't verify the download: the release isn't signed, so nothing was installed.", ErrVerify)
		}
		if err := VerifySums(u.o.PubKey, sums, sig); err != nil {
			return u.fail(ctx, ErrCodeVerify, "Couldn't verify the download, so nothing was installed.", err)
		}
	}
	table, err := ParseSums(sums)
	if err != nil {
		return u.fail(ctx, ErrCodeVerify, "Couldn't verify the download, so nothing was installed.", err)
	}
	if _, listed := table[f.Asset.Name]; !listed {
		return u.fail(ctx, ErrCodeVerify, "Couldn't verify the download, so nothing was installed.", fmt.Errorf("%w: %s not listed", ErrVerify, f.Asset.Name))
	}

	file, cleanup, err := u.downloadTarget(f)
	if err != nil {
		return u.fail(ctx, ErrCodeInstall, "Couldn't prepare the download folder.", err)
	}
	installed := false
	defer func() {
		if !installed {
			cleanup()
		}
	}()
	got, err := u.download(ctx, f.Asset, file)
	if err != nil {
		return u.fail(ctx, ErrCodeDownload, "Couldn't download the update. Check your connection and try again.", err)
	}
	u.setPhase(ctx, PhaseVerifying, 100)
	if err := CheckSum(table, f.Asset.Name, got); err != nil {
		return u.fail(ctx, ErrCodeVerify, "Couldn't verify the download, so nothing was installed.", err)
	}

	u.setPhase(ctx, PhaseInstalling, 100)
	if err := u.o.Installer.Install(ctx, f.Kind, file, got); err != nil {
		if errors.Is(err, ErrInstalledNoRelaunch) {
			// the new version is in place; only the automatic restart failed, so don't quit and don't say "unchanged"
			installed = true
			cleanup()
			return u.fail(ctx, ErrCodeRestart, "Cuisle "+f.Version+" is installed. Quit and open Cuisle again to finish.", err)
		}
		return u.fail(ctx, ErrCodeInstall, "Couldn't install the update. Cuisle is unchanged.", err)
	}
	installed = true
	if f.Kind != KindNSIS {
		cleanup() // the NSIS helper deletes its installer itself, after running it
	}
	u.o.Logger.Info("update: installed; restarting", "version", f.Version)
	u.setPhase(ctx, PhaseRestarting, 100)
	if u.o.Quit != nil {
		u.o.Quit()
	}
	return nil
}

// usable checks a found release before anything is fetched: every URL on the channel's hosts, and a version in a
// dmg or installer name equal to the release's tag.
func (u *Updater) usable(f *Found) error {
	if err := f.trusted(u.o.Channel, u.o.ReleaseAPI); err != nil {
		return err
	}
	v, ok := ParseVersion(f.Version)
	if !ok || !nameMatchesVersion(f.Asset.Name, f.Kind, v) {
		return fmt.Errorf("%w: %s does not belong to %s", ErrVerify, f.Asset.Name, f.Version)
	}
	return nil
}

// downloadTarget is the file the asset is written to: next to the AppImage (so the final rename is atomic), or a
// fresh temporary folder. cleanup removes whatever was created.
func (u *Updater) downloadTarget(f *Found) (string, func(), error) {
	if f.Kind == KindAppImage {
		tmp, err := os.CreateTemp(filepath.Dir(u.o.AppImage), ".cuisle-update-*.AppImage")
		if err != nil {
			return "", func() {}, err
		}
		name := tmp.Name()
		tmp.Close()
		return name, func() { os.Remove(name) }, nil
	}
	base := u.o.TempDir
	if base == "" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "cuisle-update-")
	if err != nil {
		return "", func() {}, err
	}
	return filepath.Join(dir, filepath.Base(f.Asset.Name)), func() { os.RemoveAll(dir) }, nil
}

func (u *Updater) checkURL(raw string) error {
	p, err := url.Parse(raw)
	if err != nil || p.Host == "" {
		return fmt.Errorf("update: bad download URL")
	}
	if p.Scheme == "https" || (u.o.AllowHTTP && p.Scheme == "http") {
		return nil
	}
	return fmt.Errorf("update: refusing a %s download URL", p.Scheme)
}

func (u *Updater) get(ctx context.Context, raw string) (*http.Response, error) {
	if err := u.checkURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", u.o.UserAgent)
	resp, err := u.o.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("update: download answered HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

const maxSumsBytes = 1 << 20

func (u *Updater) fetchSmall(ctx context.Context, a Asset) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	resp, err := u.get(ctx, a.URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxSumsBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxSumsBytes {
		return nil, fmt.Errorf("%w: %s is too large", ErrVerify, a.Name)
	}
	return b, nil
}

func (u *Updater) fetchSums(ctx context.Context, f *Found) (sums, sig []byte, err error) {
	if sums, err = u.fetchSmall(ctx, f.Sums); err != nil {
		return nil, nil, err
	}
	if f.Sig != nil && u.o.PubKey != "" {
		if sig, err = u.fetchSmall(ctx, *f.Sig); err != nil {
			return nil, nil, err
		}
	}
	return sums, sig, nil
}

// downloadTimeout caps an asset download so a stalled TCP connection cannot hold opMu forever.
const downloadTimeout = 15 * time.Minute

// download streams the asset to file, hashing as it goes, and reports progress. It returns the hex SHA-256.
func (u *Updater) download(ctx context.Context, a Asset, file string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	resp, err := u.get(ctx, a.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	total := a.Size
	if total <= 0 {
		total = resp.ContentLength
	}
	if total > u.o.MaxDownload {
		return "", fmt.Errorf("update: asset is larger than %d bytes", u.o.MaxDownload)
	}
	out, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	pw := &progressWriter{total: total, last: -1, report: func(p int) { u.setPhase(ctx, PhaseDownloading, p) }}
	limit := u.o.MaxDownload + 1
	if a.Size > 0 {
		limit = a.Size + 1 // one byte more than the release says is enough to notice a longer body
	}
	n, err := io.Copy(io.MultiWriter(out, h, pw), io.LimitReader(resp.Body, limit))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n > u.o.MaxDownload || (a.Size > 0 && n != a.Size) {
		return "", fmt.Errorf("update: downloaded %d bytes, expected %d", n, a.Size)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type progressWriter struct {
	total, done int64
	last        int
	report      func(int)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	pct := -1
	if p.total > 0 {
		pct = int(p.done * 100 / p.total)
		if pct > 100 {
			pct = 100
		}
	}
	if pct != p.last {
		p.last = pct
		p.report(pct)
	}
	return len(b), nil
}

// AppImagePath returns $APPIMAGE when it names an absolute path (the AppImage runtime sets it), else "". A symlink
// (~/bin/cuisle -> ~/Apps/Cuisle.AppImage) is resolved, so the update replaces the real file and keeps the link.
func AppImagePath(getenv func(string) string) string {
	p := strings.TrimSpace(getenv("APPIMAGE"))
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return p
}
