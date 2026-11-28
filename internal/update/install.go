package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Runner runs system commands. The real one is ExecRunner; tests pass a fake that records calls.
type Runner interface {
	// Run runs a command to completion.
	Run(ctx context.Context, name string, args ...string) error
	// Start starts a detached helper that outlives this process (the relaunch) and does not wait for it.
	Start(name string, args ...string) error
}

// ExecRunner runs real commands.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (ExecRunner) Start(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// System installs on the running OS:
//   - macOS (dmg): mount read-only, copy the .app next to the running bundle with ditto, swap it in by two renames
//     (old aside, new in), remove the old one, detach, and relaunch with `open -n` once this process has exited.
//   - Windows (nsis): a hidden PowerShell helper waits for this process to exit, runs the installer with /S
//     (elevated through the shell, as the installer asks for admin rights), starts the app again and deletes the
//     installer's folder.
//   - Linux (appimage): make the downloaded file executable and rename it over $APPIMAGE (same folder, so the
//     rename is atomic); the helper starts the new AppImage once this process has exited.
type System struct {
	GOOS       string
	Exe        string // os.Executable(), symlinks resolved
	PID        int
	AppImage   string // $APPIMAGE, symlinks resolved (AppImagePath): the real file is replaced, not a link to it
	PowerShell string // absolute powershell.exe (PowerShellPath); "" = the default under C:\Windows
	Run        Runner
}

// Paths of the system tools, absolute so PATH cannot substitute them.
const (
	hdiutil = "/usr/bin/hdiutil"
	ditto   = "/usr/bin/ditto"
	openCmd = "/usr/bin/open"
	sh      = "/bin/sh"
)

// PowerShellPath is the absolute Windows PowerShell under %SystemRoot%, so PATH cannot substitute it.
func PowerShellPath(getenv func(string) string) string {
	root := strings.TrimRight(getenv("SystemRoot"), `\/`)
	if root == "" {
		root = `C:\Windows`
	}
	return root + `\System32\WindowsPowerShell\v1.0\powershell.exe`
}

// relaunchWait is the shell loop the macOS and Linux helpers run before starting the app again: it waits while
// this process ($1) is alive, for at most 60 s (300 x 0.2 s; the Windows helper's Wait-Process -Timeout 60 matches).
// If the old process is still alive after 60 s, the helper exits instead of starting a duplicate instance.
const relaunchWait = `i=0; while kill -0 "$1" 2>/dev/null && [ "$i" -lt 300 ]; do sleep 0.2; i=$((i+1)); done; if kill -0 "$1" 2>/dev/null; then exit 0; fi; `

// ErrCannotInstall is wrapped when this copy cannot be replaced in place.
var ErrCannotInstall = errors.New("update: this copy cannot be replaced in place")

// Check implements Installer.
func (s *System) Check(kind Kind) error {
	switch kind {
	case KindDMG:
		if s.GOOS != "darwin" {
			break
		}
		bundle, err := s.bundle()
		if err != nil {
			return err
		}
		return writable(filepath.Dir(bundle))
	case KindNSIS:
		if s.GOOS != "windows" {
			break
		}
		return nil
	case KindAppImage:
		if s.GOOS != "linux" || s.AppImage == "" {
			break
		}
		return writable(filepath.Dir(s.AppImage))
	}
	return fmt.Errorf("%w: %s on %s", ErrCannotInstall, kind, s.GOOS)
}

// Install implements Installer.
func (s *System) Install(ctx context.Context, kind Kind, file, sha256Hex string) error {
	if err := s.Check(kind); err != nil {
		return err
	}
	switch kind {
	case KindDMG:
		return s.installDMG(ctx, file)
	case KindNSIS:
		return s.installNSIS(file, sha256Hex)
	case KindAppImage:
		return s.installAppImage(file)
	}
	return fmt.Errorf("%w: %s", ErrCannotInstall, kind)
}

// bundle is the .app that contains the running executable (…/X.app/Contents/MacOS/x). A copy that macOS runs
// from a randomised read-only location (App Translocation: opened straight from Downloads without moving it)
// cannot be replaced.
func (s *System) bundle() (string, error) {
	exe := filepath.Clean(s.Exe)
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	bundle := filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(bundle, ".app") {
		return "", fmt.Errorf("%w: not running from an .app bundle", ErrCannotInstall)
	}
	if strings.Contains(bundle, "/AppTranslocation/") {
		return "", fmt.Errorf("%w: the app is translocated; move it to Applications first", ErrCannotInstall)
	}
	return bundle, nil
}

// writable checks that dir accepts a new file (and so a rename into it).
func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".cuisle-write-check-*")
	if err != nil {
		return fmt.Errorf("%w: %s is not writable", ErrCannotInstall, dir)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}

func (s *System) installDMG(ctx context.Context, dmg string) (err error) {
	bundle, err := s.bundle()
	if err != nil {
		return err
	}
	parent := filepath.Dir(bundle)
	mnt, err := os.MkdirTemp(filepath.Dir(dmg), "mnt-")
	if err != nil {
		return err
	}
	defer os.Remove(mnt)
	if err := s.Run.Run(ctx, hdiutil, "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mnt, dmg); err != nil {
		return err
	}
	// Always detach, also when the copy fails; a cancelled ctx must not leave the image mounted. Detaching is
	// best-effort: the image is read-only and the copy is already staged, so a failure here changes nothing.
	detached := false
	detach := func() {
		if !detached {
			detached = true
			dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer dcancel()
			_ = s.Run.Run(dctx, hdiutil, "detach", mnt, "-force")
		}
	}
	defer detach()
	src, err := findApp(mnt)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".cuisle-update-")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCannotInstall, err)
	}
	keepStage := false // set when the old app could not be put back: it is still inside stage
	defer func() {
		if !keepStage {
			os.RemoveAll(stage)
		}
	}()
	staged := filepath.Join(stage, filepath.Base(bundle))
	if err := s.Run.Run(ctx, ditto, src, staged); err != nil {
		return err
	}
	if fi, err := os.Stat(filepath.Join(staged, "Contents", "MacOS")); err != nil || !fi.IsDir() {
		return fmt.Errorf("update: the copied app has no Contents/MacOS")
	}
	detach()
	aside := filepath.Join(stage, "previous.app")
	if err := swapDir(bundle, staged, aside); err != nil {
		if errors.Is(err, ErrRestoreFailed) {
			keepStage = true
			return fmt.Errorf("%w; the previous Cuisle is at %s", err, aside)
		}
		return err
	}
	// From here the new app is in place: nothing below may report "unchanged". The old copy (inside stage) is
	// removed by the deferred RemoveAll, best-effort.
	script := relaunchWait + `exec ` + openCmd + ` -n "$2"`
	if err := s.Run.Start(sh, "-c", script, "cuisle-relaunch", strconv.Itoa(s.PID), bundle); err != nil {
		return fmt.Errorf("%w: %v", ErrInstalledNoRelaunch, err)
	}
	return nil
}

// ErrInstalledNoRelaunch is wrapped when the new version is in place but the relaunch helper could not start: the
// app must not claim it is unchanged, and it must not quit (the user reopens it).
var ErrInstalledNoRelaunch = errors.New("update: installed, but Cuisle could not restart itself")

// ErrRestoreFailed is wrapped when the new app could not move in AND the old one could not be moved back.
var ErrRestoreFailed = errors.New("update: the app could not be put back")

// rename is os.Rename; tests replace it to make a particular rename fail.
var rename = os.Rename

// swapDir replaces target with staged: target is renamed to aside, then staged is renamed to target. If the second
// rename fails the first is undone, so target is never left missing. Removing aside is the caller's (best-effort)
// job: once the second rename succeeded the install has happened.
func swapDir(target, staged, aside string) error {
	if err := rename(target, aside); err != nil {
		return fmt.Errorf("update: cannot move the old app aside: %w", err)
	}
	if err := rename(staged, target); err != nil {
		if rerr := rename(aside, target); rerr != nil {
			return fmt.Errorf("%w: cannot move the new app in (%v) nor restore the old one (%v)", ErrRestoreFailed, err, rerr)
		}
		return fmt.Errorf("update: cannot move the new app in: %w", err)
	}
	return nil
}

// findApp returns the one .app at the top of a mounted image (the "Applications" symlink and other files are
// ignored).
func findApp(mnt string) (string, error) {
	entries, err := os.ReadDir(mnt)
	if err != nil {
		return "", err
	}
	var apps []string
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".app") {
			apps = append(apps, filepath.Join(mnt, e.Name()))
		}
	}
	if len(apps) != 1 {
		return "", fmt.Errorf("update: expected one .app in the disk image, found %d", len(apps))
	}
	return apps[0], nil
}

// psQuote quotes a string for a PowerShell single-quoted literal. PowerShell also ends such a literal at the
// typographic single quotes U+2018, U+2019, U+201A and U+201B, so each of those is doubled as well.
func psQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'', '\u2018', '\u2019', '\u201A', '\u201B':
			b.WriteRune(r)
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

// nsisScript is the PowerShell the Windows helper runs: wait for Cuisle to exit (60 s at most), check the
// installer's SHA-256 again immediately before running it (the file waited in the user's own temp folder while
// Cuisle quit; a changed file is deleted, never run), run it silently and elevated (it installs for the machine by
// default), start Cuisle again (the new one, or the old one if the check failed or the user declined the elevation
// prompt) and remove the downloaded installer.
func nsisScript(pid int, installer, sha256Hex, exe string) string {
	dir := psQuote(filepath.Dir(installer))
	return strings.Join([]string{
		"$ErrorActionPreference = 'SilentlyContinue'",
		fmt.Sprintf("Wait-Process -Id %d -Timeout 60", pid),
		fmt.Sprintf("if (Get-Process -Id %d) { Remove-Item -LiteralPath %s -Recurse -Force; exit 0 }", pid, dir),
		fmt.Sprintf("$h = (Get-FileHash -LiteralPath %s -Algorithm SHA256).Hash", psQuote(installer)),
		fmt.Sprintf("if ($h -and $h.ToLower() -eq %s) { Start-Process -FilePath %s -ArgumentList '/S' -Verb RunAs -Wait }",
			psQuote(strings.ToLower(sha256Hex)), psQuote(installer)),
		fmt.Sprintf("Start-Process -FilePath %s", psQuote(exe)),
		fmt.Sprintf("Remove-Item -LiteralPath %s -Recurse -Force", dir),
	}, "; ")
}

func (s *System) installNSIS(installer, sha256Hex string) error {
	if len(sha256Hex) != 64 {
		return fmt.Errorf("update: no checksum for the installer")
	}
	ps := s.PowerShell
	if ps == "" {
		ps = PowerShellPath(func(string) string { return "" })
	}
	return s.Run.Start(ps, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command",
		nsisScript(s.PID, installer, sha256Hex, s.Exe))
}

// installAppImage replaces $APPIMAGE with the verified download (written in the same folder by the Updater).
func (s *System) installAppImage(file string) error {
	if filepath.Dir(file) != filepath.Dir(s.AppImage) {
		return fmt.Errorf("update: the new AppImage is not next to the old one")
	}
	if err := os.Chmod(file, 0o755); err != nil {
		return err
	}
	if err := os.Rename(file, s.AppImage); err != nil {
		return fmt.Errorf("update: cannot replace the AppImage: %w", err)
	}
	script := relaunchWait + `exec "$2"`
	if err := s.Run.Start(sh, "-c", script, "cuisle-relaunch", strconv.Itoa(s.PID), s.AppImage); err != nil {
		return fmt.Errorf("%w: %v", ErrInstalledNoRelaunch, err)
	}
	return nil
}
