package main

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/apperr"
)

func TestStartupMessage(t *testing.T) {
	newer := apperr.New(apperr.DB, "This data was created by a newer Cuisle")
	tests := []struct {
		name string
		what string
		err  error
		want string
	}{
		{"newer schema", "cannot open the database", newer, "This data was created by a newer Cuisle"},
		{"newer schema wrapped", "cannot open the database", errors.Join(errors.New("x"), newer), "This data was created by a newer Cuisle"},
		{"plain error never leaks detail", "cannot open the log", errors.New("open /secret/path: denied"), "Cuisle cannot open the log"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := startupMessage(tc.what, tc.err)
			if !strings.Contains(got, tc.want) {
				t.Errorf("message = %q, want it to contain %q", got, tc.want)
			}
			if strings.Contains(got, "/secret/path") {
				t.Errorf("message %q leaks the raw error", got)
			}
		})
	}
}

func TestFailStartup_DialogUnlessBench(t *testing.T) {
	orig := showErrorDialog
	defer func() { showErrorDialog = orig }()
	var shown atomic.Int32
	showErrorDialog = func(title, message string) { shown.Add(1) }
	err := apperr.New(apperr.DB, "This data was created by a newer Cuisle")

	if code := failStartup(options{}, "cannot open the database", err); code != 1 || shown.Load() != 1 {
		t.Errorf("normal start: code %d, dialogs %d; want 1 and 1", code, shown.Load())
	}
	if code := failStartup(options{benchStartup: true}, "cannot open the database", err); code != 1 || shown.Load() != 1 {
		t.Errorf("--bench-startup: code %d, dialogs %d; want 1 and still 1 (no dialog)", code, shown.Load())
	}
}

func TestWindowTracker_DebouncedSave(t *testing.T) {
	var saves atomic.Int32
	var last atomic.Value
	tr := &windowTracker{
		debounce: 30 * time.Millisecond, haveState: true,
		state: app.WindowState{X: 1, Y: 2, W: 1280, H: 800},
	}
	tr.save = func(ws app.WindowState) { saves.Add(1); last.Store(ws) }

	touch := func() { tr.mu.Lock(); tr.touchLocked(); tr.mu.Unlock() }
	for range 5 { // a burst of move/resize events re-arms one timer
		touch()
		time.Sleep(5 * time.Millisecond)
	}
	if n := saves.Load(); n != 0 {
		t.Fatalf("saved %d times during the burst, want 0", n)
	}
	time.Sleep(120 * time.Millisecond)
	if n := saves.Load(); n != 1 {
		t.Fatalf("saved %d times after the burst, want exactly 1", n)
	}
	if ws, _ := last.Load().(app.WindowState); ws.W != 1280 || ws.H != 800 {
		t.Errorf("saved %+v", ws)
	}

	touch()
	tr.stopSaving()
	time.Sleep(120 * time.Millisecond)
	if n := saves.Load(); n != 1 {
		t.Errorf("a save fired after stopSaving (total %d)", n)
	}

	// An untrusted state (too small) is never written.
	small := &windowTracker{debounce: 10 * time.Millisecond, haveState: true, state: app.WindowState{W: 10, H: 10}}
	small.save = func(app.WindowState) { saves.Add(1) }
	small.mu.Lock()
	small.touchLocked()
	small.mu.Unlock()
	time.Sleep(60 * time.Millisecond)
	if n := saves.Load(); n != 1 {
		t.Errorf("an untrustworthy state was saved (total %d)", n)
	}
}
