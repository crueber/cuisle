package main

import (
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/app"
)

func TestNextLocalMidnight(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"mid-day", time.Date(2026, 11, 23, 9, 30, 0, 0, time.UTC), time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)},
		{"exactly midnight rolls to the next one", time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)},
		{"end of month", time.Date(2026, 11, 30, 23, 59, 59, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
		{"spring forward day is 23 hours", time.Date(2026, 3, 8, 12, 0, 0, 0, ny), time.Date(2026, 3, 9, 0, 0, 0, 0, ny)},
		{"fall back day is 25 hours", time.Date(2026, 11, 1, 12, 0, 0, 0, ny), time.Date(2026, 11, 2, 0, 0, 0, 0, ny)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextLocalMidnight(tc.now); !got.Equal(tc.want) {
				t.Errorf("nextLocalMidnight(%v) = %v, want %v", tc.now, got, tc.want)
			}
		})
	}
}

func TestParseOptions(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "CUISLE_FAKE_JIRA" {
				return v
			}
			return ""
		}
	}
	tests := []struct {
		name  string
		args  []string
		env   string
		fake  string
		bench bool
	}{
		{"nothing", nil, "", "", false},
		{"bare flag", []string{"--fake-jira"}, "", fakeDefault, false},
		{"flag with a path", []string{"--fake-jira=/tmp/f.json"}, "", "/tmp/f.json", false},
		{"explicitly off", []string{"--fake-jira=false"}, "", "", false},
		{"environment default", nil, "default", fakeDefault, false},
		{"environment path", nil, "/tmp/g.json", "/tmp/g.json", false},
		{"the flag wins over the environment", []string{"--fake-jira=/tmp/f.json"}, "default", "/tmp/f.json", false},
		{"bench", []string{"--bench-startup"}, "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOptions(tc.args, env(tc.env))
			if err != nil {
				t.Fatalf("parseOptions: %v", err)
			}
			if got.fakeJira != tc.fake || got.benchStartup != tc.bench {
				t.Errorf("options = %+v, want fake %q bench %v", got, tc.fake, tc.bench)
			}
		})
	}
}

func TestWindowOptions_RestoresASaneSavedWindowAndFallsBackOtherwise(t *testing.T) {
	bg := backgroundFor("light", nil)
	def := windowOptions(nil, bg, false)
	if def.Width != defaultWidth || def.Height != defaultHeight || def.MinWidth != minWidth || def.MinHeight != minHeight {
		t.Errorf("default window = %dx%d (min %dx%d)", def.Width, def.Height, def.MinWidth, def.MinHeight)
	}
	saved := &app.WindowState{X: 40, Y: 60, W: 1280, H: 800, Maximised: true}
	got := windowOptions(saved, bg, false)
	if got.Width != 1280 || got.Height != 800 || got.X != 40 || got.Y != 60 || got.StartState == 0 {
		t.Errorf("restored window = %+v", got)
	}
	for name, bad := range map[string]*app.WindowState{
		"too small": {W: 300, H: 200}, "absurd": {W: 90000, H: 800}, "off in space": {X: 99999, Y: 0, W: 1280, H: 800},
	} {
		if o := windowOptions(bad, bg, false); o.Width != defaultWidth || o.Height != defaultHeight {
			t.Errorf("%s: window = %dx%d, want the default", name, o.Width, o.Height)
		}
	}
}

func TestBackgroundFor_FollowsTheThemeAndAsksTheOSForSystem(t *testing.T) {
	dark, light := func() bool { return true }, func() bool { return false }
	tests := []struct {
		theme string
		sys   func() bool
		want  bool // dark
	}{
		{"dark", light, true}, {"light", dark, false}, {"system", dark, true}, {"system", light, false}, {"system", nil, false},
		{"", nil, false},
	}
	for _, tc := range tests {
		if got := backgroundFor(tc.theme, tc.sys) == darkBackground; got != tc.want {
			t.Errorf("backgroundFor(%q) dark = %v, want %v", tc.theme, got, tc.want)
		}
	}
}
