package update

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRunner stands in for hdiutil, ditto and the relaunch helper.
type fakeRunner struct {
	runs    [][]string
	starts  [][]string
	mounted string
	dmgApp  string // name of the .app inside the fake disk image
	fail    map[string]error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	f.runs = append(f.runs, append([]string{name}, args...))
	if err := f.fail[filepath.Base(name)+" "+args[0]]; err != nil {
		return err
	}
	switch {
	case name == hdiutil && args[0] == "attach":
		mnt := args[len(args)-2]
		f.mounted = mnt
		app := filepath.Join(mnt, f.dmgApp, "Contents", "MacOS")
		if err := os.MkdirAll(app, 0o755); err != nil {
			return err
		}
		_ = os.Symlink("/Applications", filepath.Join(mnt, "Applications"))
		return os.WriteFile(filepath.Join(app, "cuisle"), []byte("new"), 0o755)
	case name == hdiutil && args[0] == "detach":
		f.mounted = ""
		return os.RemoveAll(filepath.Join(args[1], f.dmgApp))
	case name == ditto:
		return copyTree(args[0], args[1])
	}
	return nil
}

func (f *fakeRunner) Start(name string, args ...string) error {
	f.starts = append(f.starts, append([]string{name}, args...))
	return f.fail["start"]
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o755)
	})
}

func makeBundle(t *testing.T, dir, name string) string {
	t.Helper()
	exe := filepath.Join(dir, name, "Contents", "MacOS", "cuisle")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestSystemDMG_SwapsTheBundleDetachesAndRelaunches(t *testing.T) {
	apps := t.TempDir()
	exe := makeBundle(t, apps, "Cuisle.app")
	dl := t.TempDir()
	dmg := filepath.Join(dl, "Cuisle-v1.3.0-macos-universal.dmg")
	_ = os.WriteFile(dmg, []byte("dmg"), 0o600)
	run := &fakeRunner{dmgApp: "cuisle.app"}
	s := &System{GOOS: "darwin", Exe: exe, PID: 4242, Run: run}
	if err := s.Check(KindDMG); err != nil {
		t.Fatal(err)
	}
	if err := s.Install(context.Background(), KindDMG, dmg, testSHA); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "new" {
		t.Fatalf("bundle not replaced: %q", got)
	}
	entries, _ := os.ReadDir(apps)
	if len(entries) != 1 || entries[0].Name() != "Cuisle.app" {
		t.Fatalf("leftovers next to the app: %v", entries)
	}
	if run.mounted != "" {
		t.Fatal("image left mounted")
	}
	attach := run.runs[0]
	if attach[0] != hdiutil || strings.Join(attach[1:5], " ") != "attach -nobrowse -readonly -noautoopen" {
		t.Fatalf("attach %v", attach)
	}
	if len(run.starts) != 1 {
		t.Fatalf("starts %v", run.starts)
	}
	st := run.starts[0]
	if st[0] != sh || st[len(st)-2] != "4242" || st[len(st)-1] != filepath.Join(apps, "Cuisle.app") || !strings.Contains(st[2], openCmd+" -n") {
		t.Fatalf("relaunch %v", st)
	}
}

func TestSystemDMG_CopyFailureLeavesTheOldAppAndDetaches(t *testing.T) {
	apps := t.TempDir()
	exe := makeBundle(t, apps, "Cuisle.app")
	dmg := filepath.Join(t.TempDir(), "x.dmg")
	run := &fakeRunner{dmgApp: "cuisle.app"}
	failing := &dittoFails{fakeRunner: run}
	s := &System{GOOS: "darwin", Exe: exe, PID: 1, Run: failing}
	if err := s.Install(context.Background(), KindDMG, dmg, testSHA); err == nil {
		t.Fatal("want an error")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "old" {
		t.Fatal("old app damaged")
	}
	if run.mounted != "" {
		t.Fatal("image left mounted after a failure")
	}
	if len(run.starts) != 0 {
		t.Fatal("relaunched after a failure")
	}
	entries, _ := os.ReadDir(apps)
	if len(entries) != 1 {
		t.Fatalf("leftovers: %v", entries)
	}
}

// A failing detach (the image is read-only and the copy is staged) must not undo or misreport the install, and the
// image is detached before the swap and the relaunch.
func TestSystemDMG_DetachFailureIsNotAnInstallFailure(t *testing.T) {
	apps := t.TempDir()
	exe := makeBundle(t, apps, "Cuisle.app")
	dmg := filepath.Join(t.TempDir(), "x.dmg")
	run := &fakeRunner{dmgApp: "cuisle.app", fail: map[string]error{"hdiutil detach": errors.New("resource busy")}}
	s := &System{GOOS: "darwin", Exe: exe, PID: 9, Run: run}
	if err := s.Install(context.Background(), KindDMG, dmg, testSHA); err != nil {
		t.Fatalf("detach failure reported as an install failure: %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatal("bundle not replaced")
	}
	if len(run.starts) != 1 {
		t.Fatal("no relaunch")
	}
	detachAt, dittoAt := -1, -1
	for i, r := range run.runs {
		if r[0] == hdiutil && r[1] == "detach" {
			detachAt = i
		}
		if r[0] == ditto {
			dittoAt = i
		}
	}
	if detachAt < dittoAt || detachAt < 0 {
		t.Fatalf("detach order: %v", run.runs)
	}
	if n := countDetach(run.runs); n != 1 {
		t.Fatalf("detached %d times", n)
	}
}

func countDetach(runs [][]string) int {
	n := 0
	for _, r := range runs {
		if r[0] == hdiutil && r[1] == "detach" {
			n++
		}
	}
	return n
}

// After the swap the new app is in place: a relaunch failure is ErrInstalledNoRelaunch, never "unchanged".
func TestSystem_RelaunchFailureAfterInstall(t *testing.T) {
	apps := t.TempDir()
	exe := makeBundle(t, apps, "Cuisle.app")
	run := &fakeRunner{dmgApp: "cuisle.app", fail: map[string]error{"start": errors.New("fork failed")}}
	s := &System{GOOS: "darwin", Exe: exe, PID: 9, Run: run}
	err := s.Install(context.Background(), KindDMG, filepath.Join(t.TempDir(), "x.dmg"), testSHA)
	if !errors.Is(err, ErrInstalledNoRelaunch) {
		t.Fatalf("err %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatal("bundle not replaced")
	}

	dir := t.TempDir()
	appImage := filepath.Join(dir, "Cuisle.AppImage")
	_ = os.WriteFile(appImage, []byte("old"), 0o755)
	newFile := filepath.Join(dir, ".cuisle-update-1.AppImage")
	_ = os.WriteFile(newFile, []byte("new"), 0o600)
	s = &System{GOOS: "linux", AppImage: appImage, PID: 9, Run: run}
	if err := s.Install(context.Background(), KindAppImage, newFile, testSHA); !errors.Is(err, ErrInstalledNoRelaunch) {
		t.Fatalf("appimage err %v", err)
	}
	if b, _ := os.ReadFile(appImage); string(b) != "new" {
		t.Fatal("AppImage not replaced")
	}
}

type dittoFails struct{ *fakeRunner }

func (d *dittoFails) Run(ctx context.Context, name string, args ...string) error {
	if name == ditto {
		return errors.New("ditto: no space")
	}
	return d.fakeRunner.Run(ctx, name, args...)
}

func TestSystemDMG_CheckRefusesNonBundlesAndTranslocation(t *testing.T) {
	for _, exe := range []string{
		"/usr/local/bin/cuisle",
		"/private/var/folders/x/AppTranslocation/ABC/d/Cuisle.app/Contents/MacOS/cuisle",
	} {
		s := &System{GOOS: "darwin", Exe: exe}
		if err := s.Check(KindDMG); !errors.Is(err, ErrCannotInstall) {
			t.Errorf("%s: %v", exe, err)
		}
	}
	if err := (&System{GOOS: "linux"}).Check(KindDMG); err == nil {
		t.Error("dmg on linux")
	}
	if err := (&System{GOOS: "linux"}).Check(KindPage); err == nil {
		t.Error("page is never installable")
	}
}

func TestSwapDir_RollsBackWhenTheNewAppCannotMoveIn(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Cuisle.app")
	_ = os.MkdirAll(target, 0o755)
	_ = os.WriteFile(filepath.Join(target, "v"), []byte("old"), 0o644)
	err := swapDir(target, filepath.Join(dir, "missing.app"), filepath.Join(dir, "aside.app"))
	if err == nil {
		t.Fatal("want an error")
	}
	if b, _ := os.ReadFile(filepath.Join(target, "v")); string(b) != "old" {
		t.Fatal("old app not restored")
	}
}

func TestSystemAppImage_ReplacesInPlaceAndRelaunches(t *testing.T) {
	dir := t.TempDir()
	appImage := filepath.Join(dir, "Cuisle.AppImage")
	_ = os.WriteFile(appImage, []byte("old"), 0o755)
	newFile := filepath.Join(dir, ".cuisle-update-1.AppImage")
	_ = os.WriteFile(newFile, []byte("new"), 0o600)
	run := &fakeRunner{}
	s := &System{GOOS: "linux", AppImage: appImage, PID: 77, Run: run}
	if err := s.Install(context.Background(), KindAppImage, newFile, testSHA); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(appImage)
	fi, _ := os.Stat(appImage)
	// Windows file systems have no execute bit; the AppImage path only runs on Linux anyway.
	if string(b) != "new" || (runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0) {
		t.Fatalf("content %q mode %v", b, fi.Mode())
	}
	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Fatal("download left behind")
	}
	if len(run.starts) != 1 || run.starts[0][len(run.starts[0])-1] != appImage || run.starts[0][len(run.starts[0])-2] != "77" {
		t.Fatalf("relaunch %v", run.starts)
	}
	// a file somewhere else is refused (the rename would not be atomic)
	other := filepath.Join(t.TempDir(), "x.AppImage")
	_ = os.WriteFile(other, []byte("x"), 0o600)
	if err := s.Install(context.Background(), KindAppImage, other, testSHA); err == nil {
		t.Fatal("accepted a file from another folder")
	}
	if err := (&System{GOOS: "linux"}).Check(KindAppImage); err == nil {
		t.Fatal("AppImage install without $APPIMAGE")
	}
}

// The Updater downloads an AppImage next to $APPIMAGE so the System installer's rename is atomic.
func TestUpdater_AppImageDownloadsNextToTheAppImage(t *testing.T) {
	dir := t.TempDir()
	appImage := filepath.Join(dir, "Cuisle.AppImage")
	_ = os.WriteFile(appImage, []byte("old"), 0o755)
	u := New(Options{Channel: ChannelForgejo, CurrentVersion: "v1.0.0", GOOS: "linux", AppImage: appImage})
	file, cleanup, err := u.downloadTarget(&Found{Kind: KindAppImage, Asset: Asset{Name: "cuisle-x86_64.AppImage"}})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(file) != dir {
		t.Fatalf("download at %s", file)
	}
	cleanup()
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("cleanup left the partial file")
	}
}
