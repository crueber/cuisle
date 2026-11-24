package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeReleases counts List calls (the network) and returns canned releases.
type fakeReleases struct {
	mu    sync.Mutex
	calls int
	rs    []Release
	err   error
}

func (f *fakeReleases) List(context.Context) ([]Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.rs, f.err
}

func (f *fakeReleases) n() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

var utc = time.UTC

func stable(tag string, assets ...string) Release {
	r := Release{Tag: tag, PageURL: "https://example.test/releases/" + tag}
	for _, a := range assets {
		r.Assets = append(r.Assets, Asset{Name: a, URL: "https://example.test/dl/" + tag + "/" + a})
	}
	return r
}

func macRelease(tag string) Release {
	return stable(tag, "Cuisle-"+tag+"-macos-universal.dmg", SumsName)
}

func newGateUpdater(rel *fakeReleases, c *clock, enabled *bool) *Updater {
	return New(Options{
		Channel: ChannelGitHub, ReleaseAPI: "https://example.test/api", CurrentVersion: "v1.2.0", GOOS: "darwin", GOARCH: "arm64",
		Releases: rel, State: &MemState{}, Now: c.now, Location: utc,
		Enabled: func(context.Context) bool { return enabled == nil || *enabled },
	})
}

func TestCheck_ResultNewerEqualOlder(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		latest string
		want   string
	}{{"v1.3.0", "v1.3.0"}, {"v1.2.0", ""}, {"v1.1.9", ""}} {
		rel := &fakeReleases{rs: []Release{macRelease(c.latest), macRelease("v1.0.0")}}
		u := newGateUpdater(rel, &clock{t: time.Date(2026, 11, 24, 9, 0, 0, 0, utc)}, nil)
		st, err := u.Check(ctx, TriggerStart)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if st.Available != nil {
			got = st.Available.Version
		}
		if got != c.want {
			t.Errorf("latest %s: available %q, want %q", c.latest, got, c.want)
		}
		if st.Channel != ChannelGitHub || st.CurrentVersion != "v1.2.0" || st.LastCheckedAt == 0 || st.LastError != "" {
			t.Errorf("status %+v", st)
		}
	}
}

func TestCheck_IgnoresDraftsPrereleasesAndIncompleteReleases(t *testing.T) {
	ctx := context.Background()
	rel := &fakeReleases{rs: []Release{
		{Tag: "v3.0.0", Draft: true, Assets: macRelease("v3.0.0").Assets},
		{Tag: "v2.5.0", Prerelease: true, Assets: macRelease("v2.5.0").Assets},
		macRelease("v2.4.0-rc.1"),
		stable("v2.3.0", "Cuisle-v2.3.0-macos-universal.dmg"),               // no SHA256SUMS.txt yet
		stable("v2.2.0", "Cuisle-v2.2.0-windows-amd64-setup.exe", SumsName), // nothing for macOS
		macRelease("v2.1.0"),
	}}
	u := newGateUpdater(rel, &clock{t: time.Date(2026, 11, 24, 9, 0, 0, 0, utc)}, nil)
	st, _ := u.Check(ctx, TriggerStart)
	if st.Available == nil || st.Available.Version != "v2.1.0" {
		t.Fatalf("available %+v", st.Available)
	}

	rel.rs = []Release{stable("v9.0.0", "Cuisle-v9.0.0-windows-amd64-setup.exe", SumsName)}
	st, _ = u.Check(ctx, TriggerManual)
	if st.Available != nil {
		t.Fatalf("offered a release with no asset for this platform: %+v", st.Available)
	}
}

func TestGate_DailyRules(t *testing.T) {
	ctx := context.Background()
	c := &clock{t: time.Date(2026, 11, 24, 9, 0, 0, 0, utc)}
	rel := &fakeReleases{rs: []Release{macRelease("v1.2.0")}}
	on := true
	u := newGateUpdater(rel, c, &on)

	// first start ever: checks
	_, _ = u.Check(ctx, TriggerStart)
	if rel.n() != 1 {
		t.Fatalf("first start: %d requests", rel.n())
	}
	// restart 23 h later: no request
	c.t = c.t.Add(23 * time.Hour)
	_, _ = u.Check(ctx, TriggerStart)
	if rel.n() != 1 {
		t.Fatalf("start under 24 h: %d requests", rel.n())
	}
	// restart at 24 h: checks
	c.t = c.t.Add(time.Hour)
	_, _ = u.Check(ctx, TriggerStart)
	if rel.n() != 2 {
		t.Fatalf("start at 24 h: %d requests", rel.n())
	}
	// day rollover the same day as the last check (it ran today at 09:00): none
	_, _ = u.Check(ctx, TriggerDayRolled)
	if rel.n() != 2 {
		t.Fatalf("rollover on the day of a check: %d requests", rel.n())
	}
	// the next local midnight: one request, and a second rollover event that day sends nothing
	c.t = time.Date(2026, 11, 26, 0, 0, 1, 0, utc)
	_, _ = u.Check(ctx, TriggerDayRolled)
	_, _ = u.Check(ctx, TriggerDayRolled)
	if rel.n() != 3 {
		t.Fatalf("rollover: %d requests", rel.n())
	}
	// setting off: neither start nor rollover checks, even days later
	on = false
	c.t = c.t.Add(72 * time.Hour)
	_, _ = u.Check(ctx, TriggerStart)
	_, _ = u.Check(ctx, TriggerDayRolled)
	if rel.n() != 3 {
		t.Fatalf("setting off: %d requests", rel.n())
	}
	// Check now always asks, even with the setting off
	if _, err := u.Check(ctx, TriggerManual); err != nil || rel.n() != 4 {
		t.Fatalf("check now: %d requests, %v", rel.n(), err)
	}
}

func TestGate_FailedAttemptCountsAndIsReported(t *testing.T) {
	ctx := context.Background()
	c := &clock{t: time.Date(2026, 11, 24, 9, 0, 0, 0, utc)}
	rel := &fakeReleases{err: errors.New("dial tcp: no route")}
	u := newGateUpdater(rel, c, nil)
	st, err := u.Check(ctx, TriggerStart)
	if err != nil {
		t.Fatalf("a scheduled check reports by status, not error: %v", err)
	}
	if st.LastError == "" || st.LastCheckedAt == 0 {
		t.Fatalf("status %+v", st)
	}
	c.t = c.t.Add(time.Hour)
	_, _ = u.Check(ctx, TriggerStart)
	if rel.n() != 1 {
		t.Fatalf("retried within the day: %d", rel.n())
	}
	if _, err := u.Check(ctx, TriggerManual); err == nil {
		t.Fatal("check now should return the failure")
	}
}

func TestGate_NoChannelOrDevBuildNeverChecks(t *testing.T) {
	ctx := context.Background()
	for name, o := range map[string]Options{
		"no channel":      {CurrentVersion: "v1.0.0"},
		"dev version":     {Channel: ChannelGitHub, CurrentVersion: "dev"},
		"ci build":        {Channel: ChannelForgejo, CurrentVersion: "0.0.0-abc1234"},
		"no releases api": {Channel: ChannelGitHub, CurrentVersion: "v1.0.0", Releases: nil},
	} {
		rel := &fakeReleases{rs: []Release{macRelease("v9.0.0")}}
		if name != "no releases api" {
			o.Releases = rel
		}
		u := New(o)
		for _, tr := range []Trigger{TriggerStart, TriggerDayRolled, TriggerManual} {
			st, _ := u.Check(ctx, tr)
			if st.Channel != "" || st.Available != nil {
				t.Errorf("%s: status %+v", name, st)
			}
		}
		if rel.n() != 0 {
			t.Errorf("%s: %d requests", name, rel.n())
		}
		if err := u.Install(ctx); err == nil {
			t.Errorf("%s: install allowed", name)
		}
	}
}

func TestFileState_RoundTripAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.json")
	c := &clock{t: time.Date(2026, 11, 24, 9, 0, 0, 0, utc)}
	rel := &fakeReleases{rs: []Release{macRelease("v1.3.0")}}
	mk := func() *Updater {
		return New(Options{Channel: ChannelGitHub, ReleaseAPI: "https://example.test/api", CurrentVersion: "v1.2.0", GOOS: "darwin", GOARCH: "arm64",
			Releases: rel, State: FileState{Path: path}, Now: c.now, Location: utc})
	}
	_, _ = mk().Check(context.Background(), TriggerStart)
	c.t = c.t.Add(time.Hour)
	u2 := mk() // a restart
	st := u2.Status(context.Background())
	if st.Available == nil || st.Available.Version != "v1.3.0" || st.LastCheckedAt != time.Date(2026, 11, 24, 9, 0, 0, 0, utc).UnixMilli() {
		t.Fatalf("restored %+v", st)
	}
	_, _ = u2.Check(context.Background(), TriggerStart)
	if rel.n() != 1 {
		t.Fatalf("restart within a day re-checked: %d", rel.n())
	}
	// after the update is installed the stored release is no longer newer
	u3 := New(Options{Channel: ChannelGitHub, CurrentVersion: "v1.3.0", Releases: rel, State: FileState{Path: path}})
	if st := u3.Status(context.Background()); st.Available != nil {
		t.Fatalf("offered the running version: %+v", st.Available)
	}
	// a damaged file costs one check, nothing more
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := (FileState{Path: path}).Load(); err != nil || !s.LastCheckAt.IsZero() {
		t.Fatalf("damaged: %+v %v", s, err)
	}
}

// ---- install --------------------------------------------------------------------------------------------------

type fakeInstaller struct {
	checkErr error
	err      error
	files    []string
	content  []byte
}

func (f *fakeInstaller) Check(Kind) error { return f.checkErr }
func (f *fakeInstaller) Install(_ context.Context, _ Kind, file, _ string) error {
	f.files = append(f.files, file)
	f.content, _ = os.ReadFile(file)
	return f.err
}

type installRig struct {
	u      *Updater
	inst   *fakeInstaller
	tmp    string
	quit   int
	events []Status
}

// newInstallRig serves a release whose dmg is payload, whose SHA256SUMS.txt lists sumsFor (the payload's checksum
// unless overridden) and, when sig is non-nil, a SHA256SUMS.txt.sig.
func newInstallRig(t *testing.T, payload []byte, sums string, sig []byte, pubKey string) *installRig {
	t.Helper()
	const dmg = "Cuisle-v1.3.0-macos-universal.dmg"
	mux := http.NewServeMux()
	mux.HandleFunc("/dl/"+dmg, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) })
	mux.HandleFunc("/dl/"+SumsName, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(sums)) })
	mux.HandleFunc("/dl/"+SigName, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(sig) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	r := Release{Tag: "v1.3.0", PageURL: srv.URL + "/page", Assets: []Asset{
		{Name: dmg, URL: srv.URL + "/dl/" + dmg, Size: int64(len(payload))},
		{Name: SumsName, URL: srv.URL + "/dl/" + SumsName},
	}}
	if sig != nil {
		r.Assets = append(r.Assets, Asset{Name: SigName, URL: srv.URL + "/dl/" + SigName})
	}
	rig := &installRig{inst: &fakeInstaller{}, tmp: t.TempDir()}
	rig.u = New(Options{
		Channel: ChannelGitHub, ReleaseAPI: srv.URL + "/api", CurrentVersion: "v1.2.0", GOOS: "darwin", GOARCH: "arm64", PubKey: pubKey,
		Releases: &fakeReleases{rs: []Release{r}}, HTTP: srv.Client(), UserAgent: "Cuisle/v1.2.0",
		Installer: rig.inst, Quit: func() { rig.quit++ }, TempDir: rig.tmp, AllowHTTP: true,
		Notify: func(s Status) { rig.events = append(rig.events, s) },
	})
	if _, err := rig.u.Check(context.Background(), TriggerManual); err != nil {
		t.Fatal(err)
	}
	return rig
}

func (r *installRig) leftovers(t *testing.T) []string {
	t.Helper()
	es, _ := os.ReadDir(r.tmp)
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func sumsLine(payload []byte) string {
	return fmt.Sprintf("%s  Cuisle-v1.3.0-macos-universal.dmg\n%s  other.AppImage\n", sumOf(payload), sumOf([]byte("o")))
}

func TestInstall_GoodChecksumInstallsAndQuits(t *testing.T) {
	payload := []byte("the new dmg bytes")
	rig := newInstallRig(t, payload, sumsLine(payload), nil, "")
	if err := rig.u.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rig.inst.files) != 1 || string(rig.inst.content) != string(payload) {
		t.Fatalf("installer got %v %q", rig.inst.files, rig.inst.content)
	}
	if rig.quit != 1 {
		t.Fatalf("quit %d", rig.quit)
	}
	if left := rig.leftovers(t); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	var phases []Phase
	for _, e := range rig.events {
		if len(phases) == 0 || phases[len(phases)-1] != e.Phase {
			phases = append(phases, e.Phase)
		}
	}
	want := []Phase{PhaseChecking, PhaseIdle, PhaseDownloading, PhaseVerifying, PhaseInstalling, PhaseRestarting}
	if fmt.Sprint(phases) != fmt.Sprint(want) {
		t.Fatalf("phases %v, want %v", phases, want)
	}
	sawPct := false
	for _, e := range rig.events {
		if e.Phase == PhaseDownloading && e.Progress == 100 {
			sawPct = true
		}
	}
	if !sawPct {
		t.Fatal("no download progress reported")
	}
}

func TestInstall_BadChecksumIsRejectedAndNothingRuns(t *testing.T) {
	payload := []byte("tampered bytes")
	rig := newInstallRig(t, payload, sumsLine([]byte("what was published")), nil, "")
	err := rig.u.Install(context.Background())
	if err == nil {
		t.Fatal("a bad checksum installed")
	}
	if len(rig.inst.files) != 0 || rig.quit != 0 {
		t.Fatal("installer ran on an unverified file")
	}
	st := rig.u.Status(context.Background())
	if st.Phase != PhaseFailed || st.ErrorCode != ErrCodeVerify || st.Error == "" {
		t.Fatalf("status %+v", st)
	}
	if left := rig.leftovers(t); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestInstall_AssetNotListedInSums(t *testing.T) {
	payload := []byte("x")
	rig := newInstallRig(t, payload, sumOf(payload)+"  something-else.dmg\n", nil, "")
	if err := rig.u.Install(context.Background()); err == nil || len(rig.inst.files) != 0 {
		t.Fatalf("installed an unlisted file: %v", err)
	}
	if st := rig.u.Status(context.Background()); st.ErrorCode != ErrCodeVerify {
		t.Fatalf("code %q", st.ErrorCode)
	}
}

func TestInstall_Signature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pubText := base64.StdEncoding.EncodeToString(pub)
	payload := []byte("signed dmg")
	sums := sumsLine(payload)
	goodSig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sums))) + "\n")
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	badSig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(otherPriv, []byte(sums))))

	cases := []struct {
		name    string
		sig     []byte
		pub     string
		install bool
	}{
		{"key and good signature", goodSig, pubText, true},
		{"key and wrong signature", badSig, pubText, false},
		{"key but release unsigned", nil, pubText, false},
		{"no key, signature present (cannot check; SHA-256 still required)", badSig, "", true},
		{"no key, no signature", nil, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rig := newInstallRig(t, payload, sums, c.sig, c.pub)
			err := rig.u.Install(context.Background())
			if c.install != (err == nil) || c.install != (len(rig.inst.files) == 1) {
				t.Fatalf("err %v, installer calls %d", err, len(rig.inst.files))
			}
			if !c.install {
				if st := rig.u.Status(context.Background()); st.ErrorCode != ErrCodeVerify {
					t.Fatalf("code %q", st.ErrorCode)
				}
			}
		})
	}
}

func TestInstall_CannotInstallHereAndInstallerFailure(t *testing.T) {
	payload := []byte("x")
	rig := newInstallRig(t, payload, sumsLine(payload), nil, "")
	rig.inst.checkErr = ErrCannotInstall
	if err := rig.u.Install(context.Background()); err == nil || len(rig.inst.files) != 0 {
		t.Fatal("installed where it cannot")
	}
	if st := rig.u.Status(context.Background()); st.Available == nil || st.Available.CanInstall {
		t.Fatalf("canInstall should be false after a refused install: %+v", st.Available)
	}
	if st := rig.u.Status(context.Background()); st.ErrorCode != ErrCodeUnsupported {
		t.Fatalf("code %q", st.ErrorCode)
	}

	rig2 := newInstallRig(t, payload, sumsLine(payload), nil, "")
	rig2.inst.err = errors.New("hdiutil: attach failed")
	if err := rig2.u.Install(context.Background()); err == nil || rig2.quit != 0 {
		t.Fatal("a failed install quit the app")
	}
	if st := rig2.u.Status(context.Background()); st.ErrorCode != ErrCodeInstall || st.Phase != PhaseFailed {
		t.Fatalf("status %+v", st)
	}
	if left := rig2.leftovers(t); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

// The installer's Check can touch the disk, so Status must not call it on every progress event.
type countingInstaller struct {
	fakeInstaller
	checks int
}

func (c *countingInstaller) Check(k Kind) error { c.checks++; return c.fakeInstaller.Check(k) }

func TestStatus_CachesCanInstall(t *testing.T) {
	payload := []byte("x")
	rig := newInstallRig(t, payload, sumsLine(payload), nil, "")
	ci := &countingInstaller{}
	rig.u.o.Installer = ci
	rig.u.canCache = nil
	for i := 0; i < 5; i++ {
		_ = rig.u.Status(context.Background())
	}
	if ci.checks != 1 {
		t.Fatalf("Check ran %d times for 5 Status calls", ci.checks)
	}
}

func TestInstall_InstalledButNoRestartDoesNotQuitOrClaimUnchanged(t *testing.T) {
	payload := []byte("x")
	rig := newInstallRig(t, payload, sumsLine(payload), nil, "")
	rig.inst.err = fmt.Errorf("%w: fork failed", ErrInstalledNoRelaunch)
	if err := rig.u.Install(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	st := rig.u.Status(context.Background())
	if rig.quit != 0 || st.ErrorCode != ErrCodeRestart || st.Error != "Cuisle v1.3.0 is installed. Quit and open Cuisle again to finish." {
		t.Fatalf("quit %d, status %+v", rig.quit, st)
	}
	if left := rig.leftovers(t); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestInstall_RefusesPlainHTTPDownloads(t *testing.T) {
	payload := []byte("x")
	rig := newInstallRig(t, payload, sumsLine(payload), nil, "")
	rig.u.o.AllowHTTP = false
	if err := rig.u.Install(context.Background()); err == nil || len(rig.inst.files) != 0 {
		t.Fatal("downloaded over http")
	}
	if st := rig.u.Status(context.Background()); st.ErrorCode != ErrCodeDownload {
		t.Fatalf("code %q", st.ErrorCode)
	}
}

func TestStatus_JSONShape(t *testing.T) {
	b, _ := json.Marshal(Status{Available: &Available{Version: "v1"}})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"channel", "currentVersion", "enabled", "lastCheckedAt", "lastError", "available", "phase", "progress", "error", "errorCode"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
}
