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

// WP-38B: "Update Cuisle" is advice for a newer database only, and the text never ends in "..".
func TestStartupMessage_UpdateAdviceOnlyForANewerSchema(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"newer schema", apperr.New(apperr.DB, "This data was created by a newer Cuisle"),
			"This data was created by a newer Cuisle. Update Cuisle to open it; nothing was changed."},
		{"newer schema with a full stop", apperr.New(apperr.DB, "This data was created by a newer Cuisle."),
			"This data was created by a newer Cuisle. Update Cuisle to open it; nothing was changed."},
		{"other db error", apperr.New(apperr.DB, "The database file is locked."), "The database file is locked."},
		{"other db error without a full stop", apperr.New(apperr.DB, "The disk is full"), "The disk is full"},
		{"newer text under another code", apperr.New(apperr.Internal, "This data was created by a newer Cuisle"),
			"This data was created by a newer Cuisle"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := startupMessage("cannot open the database", tc.err)
			if got != tc.want {
				t.Errorf("message = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "..") {
				t.Errorf("message %q contains ..", got)
			}
		})
	}
	if got := startupMessage("cannot open the database", errors.New("boom")); strings.Contains(got, "Update Cuisle") {
		t.Errorf("a plain error got the update advice: %q", got)
	}
}

// The save callback is installed under the tracker's lock and a pending debounce uses it.
func TestWindowTracker_SetSaveIsUsedByTheDebounce(t *testing.T) {
	var saves atomic.Int32
	tr := &windowTracker{debounce: 10 * time.Millisecond, haveState: true, state: app.WindowState{X: 1, Y: 2, W: 1280, H: 800}}
	tr.setSave(func(app.WindowState) { saves.Add(1) })
	tr.mu.Lock()
	tr.touchLocked()
	tr.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for saves.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if saves.Load() != 1 {
		t.Fatalf("saves = %d, want 1", saves.Load())
	}
}
