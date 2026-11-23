package main

import (
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/crueber/cuisle/internal/app"
)

const (
	minWidth, minHeight         = 1024, 640
	defaultWidth, defaultHeight = 1440, 900
	maxRestoredSize             = 10000
)

// Window background before the webview paints (no white flash): the page background of design/tokens.json,
// color.light.bg and color.dark.bg.
var (
	lightBackground = application.NewRGBA(246, 244, 238, 255)
	darkBackground  = application.NewRGBA(18, 20, 24, 255)
)

// backgroundFor maps the theme setting to the window background. "system" asks the OS.
func backgroundFor(theme string, systemIsDark func() bool) application.RGBA {
	switch theme {
	case "dark":
		return darkBackground
	case "light":
		return lightBackground
	}
	if systemIsDark != nil && systemIsDark() {
		return darkBackground
	}
	return lightBackground
}

// windowOptions restores the saved size and position (ui_state), falling back to the default centred window.
func windowOptions(saved *app.WindowState, bg application.RGBA, dev bool) application.WebviewWindowOptions {
	o := application.WebviewWindowOptions{
		Title: "Cuisle", Width: defaultWidth, Height: defaultHeight, MinWidth: minWidth, MinHeight: minHeight,
		URL: "/", BackgroundColour: bg, DevToolsEnabled: dev,
	}
	if saved == nil || saved.W < minWidth || saved.H < minHeight || saved.W > maxRestoredSize || saved.H > maxRestoredSize ||
		saved.X < -maxRestoredSize || saved.X > maxRestoredSize || saved.Y < -maxRestoredSize || saved.Y > maxRestoredSize {
		return o
	}
	o.Width, o.Height = saved.W, saved.H
	o.InitialPosition, o.X, o.Y = application.WindowXY, saved.X, saved.Y
	if saved.Maximised {
		o.StartState = application.WindowStateMaximised
	}
	return o
}

// saveDebounce is how long after the last move/resize the window state is written to ui_state. It is UI-local
// (one timer re-armed per event), not network work; shutdown still writes the final state.
const saveDebounce = time.Second

// windowTracker remembers the window's last normal bounds and maximised state in memory (no database writes while
// the user drags); a debounced save and shutdown write them to ui_state.
//
// Native size reporting and the size requested at creation differ by the platform's window chrome (a macOS title bar
// reports ~30 px more than was asked for) and by screen clamping. calibrate measures that difference once, when the
// window has just appeared at the size we asked for, and snapshot subtracts it, so a restored window does not grow
// on every launch.
type windowTracker struct {
	mu         sync.Mutex
	win        *application.WebviewWindow
	state      app.WindowState
	haveState  bool
	reqW, reqH int
	dw, dh     int
	calibrated bool

	save     func(app.WindowState) // debounced persistence; nil disables it (bench runs)
	debounce time.Duration
	timer    *time.Timer
	stopped  bool
}

func trackWindow(win *application.WebviewWindow, initial *app.WindowState, reqW, reqH int) *windowTracker {
	t := &windowTracker{win: win, reqW: reqW, reqH: reqH, debounce: saveDebounce}
	if initial != nil {
		t.state, t.haveState = *initial, true
	}
	for _, ev := range []events.WindowEventType{
		events.Common.WindowDidMove, events.Common.WindowDidResize, events.Common.WindowMaximise,
		events.Common.WindowUnMaximise, events.Common.WindowRestore,
	} {
		win.OnWindowEvent(ev, func(*application.WindowEvent) { t.refresh() }) // listeners run off the UI thread
	}
	return t
}

// setSave installs the persistence callback under the lock: window events may already be arriving.
func (t *windowTracker) setSave(save func(app.WindowState)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.save = save
}

// calibrate records the chrome/clamping difference; call it once when the window first appears.
func (t *windowTracker) calibrate() {
	if t.win.IsMaximised() {
		return
	}
	b := t.win.Bounds()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.calibrated || b.Width <= 0 || b.Height <= 0 {
		return
	}
	t.dw, t.dh, t.calibrated = b.Width-t.reqW, b.Height-t.reqH, true
	t.state.X, t.state.Y, t.state.W, t.state.H = b.X, b.Y, t.reqW, t.reqH
	t.haveState = true
}

func (t *windowTracker) refresh() {
	maximised := t.win.IsMaximised()
	var b application.Rect
	if !maximised {
		b = t.win.Bounds()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Maximised = maximised
	if !maximised && t.calibrated && b.Width > 0 && b.Height > 0 {
		t.state.X, t.state.Y, t.state.W, t.state.H = b.X, b.Y, b.Width-t.dw, b.Height-t.dh
	}
	t.touchLocked()
}

// touchLocked (re)arms the debounced save. The caller holds t.mu.
func (t *windowTracker) touchLocked() {
	if t.save == nil || t.stopped {
		return
	}
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(t.debounce, func() {
		if st, ok := t.snapshot(); ok {
			t.mu.Lock()
			stopped, save := t.stopped, t.save
			t.mu.Unlock()
			if !stopped && save != nil {
				save(st)
			}
		}
	})
}

// stopSaving cancels a pending debounced save and prevents new ones (called at shutdown before the final write).
func (t *windowTracker) stopSaving() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
	if t.timer != nil {
		t.timer.Stop()
	}
}

// snapshot returns the state to persist, ok=false when there is nothing trustworthy to write.
func (t *windowTracker) snapshot() (app.WindowState, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state, t.haveState && t.state.W >= minWidth && t.state.H >= minHeight
}
