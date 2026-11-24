package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// tlsRig serves one macOS release from an https test server (the API host). The dmg path can be replaced to
// redirect elsewhere.
type tlsRig struct {
	api   *httptest.Server
	mux   *http.ServeMux
	u     *Updater
	inst  *fakeInstaller
	rel   *fakeReleases
	quit  int
	dmg   string
	bytes []byte
}

func newTLSRig(t *testing.T, dmgHandler http.HandlerFunc) *tlsRig {
	t.Helper()
	r := &tlsRig{mux: http.NewServeMux(), inst: &fakeInstaller{}, dmg: "Cuisle-v1.3.0-macos-universal.dmg", bytes: []byte("dmg bytes")}
	r.api = httptest.NewTLSServer(r.mux)
	t.Cleanup(r.api.Close)
	if dmgHandler == nil {
		dmgHandler = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(r.bytes) }
	}
	r.mux.HandleFunc("/dl/"+r.dmg, dmgHandler)
	r.mux.HandleFunc("/dl/"+SumsName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sumOf(r.bytes) + "  " + r.dmg + "\n"))
	})
	r.rel = &fakeReleases{rs: []Release{{Tag: "v1.3.0", PageURL: r.api.URL + "/page", Assets: []Asset{
		{Name: r.dmg, URL: r.api.URL + "/dl/" + r.dmg, Size: int64(len(r.bytes))},
		{Name: SumsName, URL: r.api.URL + "/dl/" + SumsName},
	}}}}
	r.u = New(Options{
		Channel: ChannelForgejo, ReleaseAPI: r.api.URL + "/api/v1/repos/o/r/releases", CurrentVersion: "v1.2.0",
		GOOS: "darwin", GOARCH: "arm64", Releases: r.rel, HTTP: r.api.Client(), UserAgent: "Cuisle/v1.2.0",
		Installer: r.inst, Quit: func() { r.quit++ }, TempDir: t.TempDir(),
	})
	if _, err := r.u.Check(context.Background(), TriggerManual); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRedirect_HTTPSToHTTPIsRefused(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("dmg bytes")) }))
	defer plain.Close()
	r := newTLSRig(t, func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, plain.URL+"/x.dmg", http.StatusFound)
	})
	if err := r.u.Install(context.Background()); err == nil || len(r.inst.files) != 0 {
		t.Fatalf("followed a redirect to http: %v", err)
	}
	if st := r.u.Status(context.Background()); st.ErrorCode != ErrCodeDownload {
		t.Fatalf("code %q", st.ErrorCode)
	}
}

// GitHub serves assets by redirecting to another https host (objects.githubusercontent.com): that must work.
func TestRedirect_HTTPSToAnotherHTTPSHostSucceeds(t *testing.T) {
	cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("dmg bytes")) }))
	defer cdn.Close()
	r := newTLSRig(t, func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, cdn.URL+"/blob", http.StatusFound)
	})
	if err := r.u.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if string(r.inst.content) != "dmg bytes" || r.quit != 1 {
		t.Fatalf("installed %q, quit %d", r.inst.content, r.quit)
	}
}

func TestRedirect_ChainIsCapped(t *testing.T) {
	var hop func(w http.ResponseWriter, req *http.Request)
	hop = func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, req.URL.Path+"x", http.StatusFound)
	}
	r := newTLSRig(t, hop)
	r.mux.HandleFunc("/dl/", hop)
	if err := r.u.Install(context.Background()); err == nil || len(r.inst.files) != 0 {
		t.Fatal("followed an endless redirect chain")
	}
	policy := RedirectPolicy(false)
	req, _ := http.NewRequest(http.MethodGet, "https://a.test/", nil)
	if policy(req, make([]*http.Request, MaxRedirects)) == nil {
		t.Fatal("no cap")
	}
	if policy(req, make([]*http.Request, MaxRedirects-1)) != nil {
		t.Fatal("cap too low")
	}
}

func TestReleasesList_HTTPSOnly(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("[]")) }))
	defer plain.Close()
	if _, err := NewGitHub(plain.URL+"/repos/o/r/releases", plain.Client(), "Cuisle/v1").List(context.Background()); err == nil {
		t.Fatal("listed over http")
	}
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, plain.URL+"/list", http.StatusFound)
	}))
	defer tls.Close()
	if _, err := NewForgejo(tls.URL+"/api/v1/repos/o/r/releases", tls.Client(), "Cuisle/v1").List(context.Background()); err == nil {
		t.Fatal("followed the list to http")
	}
}

func TestDownload_LongerThanTheReleaseSaysIsRefused(t *testing.T) {
	r := newTLSRig(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("dmg bytes and then some more")) })
	if err := r.u.Install(context.Background()); err == nil || len(r.inst.files) != 0 {
		t.Fatal("accepted an oversized download")
	}
	if New(Options{}).o.MaxDownload != 512<<20 {
		t.Fatal("default cap is not 512 MiB")
	}
}

// update.json is not trusted blindly: a stored release with a URL off the channel's hosts is looked up again.
func TestInstall_StoredURLOffTheChannelHostIsLookedUpAgain(t *testing.T) {
	r := newTLSRig(t, nil)
	r.u.mu.Lock()
	tampered := *r.u.state.Latest
	tampered.Asset.URL = "https://evil.example/x.dmg"
	r.u.state.Latest = &tampered
	r.u.mu.Unlock()
	before := r.rel.n()
	if err := r.u.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.rel.n() != before+1 {
		t.Fatal("did not look the release up again")
	}
	if string(r.inst.content) != "dmg bytes" {
		t.Fatalf("installed %q", r.inst.content)
	}

	// and if the channel itself now lists an off-host URL, nothing is offered or installed
	r2 := newTLSRig(t, nil)
	r2.rel.rs[0].Assets[0].URL = "https://evil.example/x.dmg"
	r2.u.mu.Lock()
	tampered2 := *r2.u.state.Latest
	tampered2.Asset.URL = "https://evil.example/x.dmg"
	r2.u.state.Latest = &tampered2
	r2.u.mu.Unlock()
	if err := r2.u.Install(context.Background()); err == nil || len(r2.inst.files) != 0 {
		t.Fatal("installed from an off-host URL")
	}
}

func TestHostAllowed(t *testing.T) {
	const gh = "https://api.github.com/repos/crueber/cuisle/releases"
	const fj = "https://forge.example/api/v1/repos/crueber/cuisle/releases"
	cases := []struct {
		channel, api, url string
		ok                bool
	}{
		{ChannelGitHub, gh, "https://github.com/crueber/cuisle/releases/download/v1/x.dmg", true},
		{ChannelGitHub, gh, "https://objects.githubusercontent.com/x", true},
		{ChannelGitHub, gh, "https://api.github.com/x", true},
		{ChannelGitHub, gh, "https://github.com.evil.example/x", false},
		{ChannelGitHub, gh, "https://evilgithubusercontent.com/x", false},
		{ChannelForgejo, fj, "https://forge.example/crueber/cuisle/releases/download/v1/x.AppImage", true},
		{ChannelForgejo, fj, "https://github.com/x", false},
		{ChannelForgejo, fj, "https://forge.example:8443/x", false},
		{ChannelForgejo, "", "https://forge.example/x", false},
	}
	for _, c := range cases {
		if got := hostAllowed(c.channel, c.api, c.url); got != c.ok {
			t.Errorf("%s %s: %v", c.channel, c.url, got)
		}
	}
}

func TestVersionInAssetNameMustMatchTheTag(t *testing.T) {
	v, _ := ParseVersion("v1.3.0")
	for name, want := range map[string]bool{
		"Cuisle-v1.3.0-macos-universal.dmg":     true,
		"Cuisle-v1.2.9-macos-universal.dmg":     false,
		"Cuisle-v1.3.0-windows-amd64-setup.exe": true,
		"Cuisle-v2.0.0-windows-amd64-setup.exe": false,
		"cuisle-amd64-installer.exe":            true, // no version in the name
	} {
		kind := KindDMG
		if strings.HasSuffix(name, ".exe") {
			kind = KindNSIS
		}
		if got := nameMatchesVersion(name, kind, v); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}
	if !nameMatchesVersion("cuisle_0.0.1_amd64.deb", KindPage, v) {
		t.Error("the .deb's package version is not the tag and must not be compared")
	}

	// a release whose dmg belongs to another version is not offered
	rel := &fakeReleases{rs: []Release{stable("v1.3.0", "Cuisle-v1.2.9-macos-universal.dmg", SumsName)}}
	u := newGateUpdater(rel, &clock{t: time.Date(2026, 11, 24, 9, 0, 0, 0, utc)}, nil)
	if st, _ := u.Check(context.Background(), TriggerStart); st.Available != nil {
		t.Fatalf("offered %+v", st.Available)
	}
}

func TestParseSums_ConflictingDuplicatesFailClosed(t *testing.T) {
	a, b := sumOf([]byte("a")), sumOf([]byte("b"))
	if _, err := ParseSums([]byte(a + "  x.dmg\n" + b + "  x.dmg\n")); !errors.Is(err, ErrVerify) {
		t.Fatalf("conflicting duplicate accepted: %v", err)
	}
	if _, err := ParseSums([]byte(a + "  x.dmg\n" + a + " *x.dmg\n")); err != nil {
		t.Fatalf("identical duplicate rejected: %v", err)
	}
}
