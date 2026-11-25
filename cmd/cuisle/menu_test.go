package main

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// F15: the standup screen zooms itself, so the application menu must not carry the webview's own zoom items: on macOS
// their Cmd+plus / Cmd+minus / Cmd+0 key equivalents run before the page sees the key and would zoom the header too.
func TestWithoutWebviewZoom_RemovesTheZoomItemsAndKeepsTheRest(t *testing.T) {
	m := application.NewMenu()
	m.AddRole(application.FileMenu)
	m.AddRole(application.EditMenu)
	m.AddRole(application.ViewMenu)
	m.AddRole(application.WindowMenu)

	for _, r := range []application.Role{application.ZoomIn, application.ZoomOut, application.ResetZoom} {
		if m.FindByRole(r) == nil {
			t.Fatalf("fixture drift: Wails' View menu no longer has role %v, so the test proves nothing", r)
		}
	}

	withoutWebviewZoom(m)

	for _, r := range []application.Role{application.ZoomIn, application.ZoomOut, application.ResetZoom} {
		if it := m.FindByRole(r); it != nil {
			t.Errorf("role %v still in the menu (%q, %s)", r, it.Label(), it.GetAccelerator())
		}
	}
	for _, r := range []application.Role{application.Reload, application.ForceReload, application.ToggleFullscreen, application.Copy, application.Paste, application.SelectAll, application.Minimise} {
		if m.FindByRole(r) == nil {
			t.Errorf("role %v was removed, only the zoom items should go", r)
		}
	}
	// no accelerator of the removed items is left anywhere in the tree
	for _, label := range []string{"Zoom In", "Zoom Out", "Reset Zoom", "Actual Size"} {
		if it := m.FindByLabel(label); it != nil {
			t.Errorf("an item labelled %q is still in the menu", label)
		}
	}
}

func TestWithoutWebviewZoom_LeavesNoDoubledOrDanglingSeparators(t *testing.T) {
	m := application.NewMenu()
	m.AddRole(application.ViewMenu)
	withoutWebviewZoom(m)

	view := m.FindByLabel("View")
	if view == nil || view.GetSubmenu() == nil {
		t.Fatal("no View menu")
	}
	sub := view.GetSubmenu()
	prev := true
	n := 0
	var last *application.MenuItem
	for i := 0; ; i++ {
		it := sub.ItemAt(i)
		if it == nil {
			break
		}
		n++
		if it.IsSeparator() && prev {
			t.Errorf("separator at %d follows another separator or opens the menu", i)
		}
		prev = it.IsSeparator()
		last = it
	}
	if n == 0 {
		t.Fatal("the View menu is empty")
	}
	if last.IsSeparator() {
		t.Error("the View menu ends with a separator")
	}
}
