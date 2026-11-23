package buildinfo_test

import (
	"encoding/json"
	"runtime"
	"testing"

	"github.com/crueber/cuisle/internal/buildinfo"
)

// setBuild overrides the ldflags variables for one test and restores them afterwards.
func setBuild(t *testing.T, version, commit, date string) {
	t.Helper()
	ov, oc, od := buildinfo.Version, buildinfo.Commit, buildinfo.Date
	buildinfo.Version, buildinfo.Commit, buildinfo.Date = version, commit, date
	t.Cleanup(func() { buildinfo.Version, buildinfo.Commit, buildinfo.Date = ov, oc, od })
}

func TestGet_ReportsInjectedMetadataAndRuntime(t *testing.T) {
	setBuild(t, "v1.2.3", "abc1234", "2026-11-23T10:00:00Z")
	got := buildinfo.Get()
	want := buildinfo.Info{
		Version:   "v1.2.3",
		Commit:    "abc1234",
		Date:      "2026-11-23T10:00:00Z",
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Dev:       false,
	}
	if got != want {
		t.Errorf("Get() = %+v\nwant   %+v", got, want)
	}
}

func TestGet_DevIsTrueOnlyForDevVersion(t *testing.T) {
	tests := []struct {
		version string
		dev     bool
	}{
		{"dev", true},
		{"v0.1.0", false},
		{"0.1.0-dev", false},
		{"Sofia", false},
	}
	for _, tc := range tests {
		t.Run(tc.version, func(t *testing.T) {
			setBuild(t, tc.version, "", "")
			if got := buildinfo.Get().Dev; got != tc.dev {
				t.Errorf("Dev = %v, want %v", got, tc.dev)
			}
		})
	}
}

func TestGet_DefaultsAreDevWithEmptyCommitAndDate(t *testing.T) {
	// The package-level defaults are the contract: a plain `go build` is a dev build.
	got := buildinfo.Get()
	if buildinfo.Version == "dev" {
		if !got.Dev || got.Commit != "" || got.Date != "" {
			t.Errorf("default build info = %+v", got)
		}
	}
}

func TestInfo_JSONFieldNames(t *testing.T) {
	setBuild(t, "v1.0.0", "c", "d")
	b, err := json.Marshal(buildinfo.Get())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "commit", "date", "goVersion", "os", "arch", "dev"} {
		if _, ok := m[k]; !ok {
			t.Errorf("JSON missing key %q in %s", k, b)
		}
	}
	if len(m) != 7 {
		t.Errorf("JSON has %d keys, want 7: %s", len(m), b)
	}
}

func TestUserAgent_Format(t *testing.T) {
	tests := []struct{ version, want string }{
		{"v1.2.3", "Cuisle/v1.2.3 (" + runtime.GOOS + ")"},
		{"dev", "Cuisle/dev (" + runtime.GOOS + ")"},
	}
	for _, tc := range tests {
		t.Run(tc.version, func(t *testing.T) {
			setBuild(t, tc.version, "", "")
			if got := buildinfo.UserAgent(); got != tc.want {
				t.Errorf("UserAgent() = %q, want %q", got, tc.want)
			}
		})
	}
}
