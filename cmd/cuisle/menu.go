package main

import "github.com/wailsapp/wails/v3/pkg/application"

// appMenu is Wails' default application menu without the webview's own zoom items (F15, ux-spec §16.2).
//
// Cuisle zooms the standup screen below the header itself (a CSS scale that re-runs the layout rules), on + - 0 and
// Cmd/Ctrl with them. Wails' default View menu carries Zoom In, Zoom Out and Actual Size on Cmd/Ctrl + plus, minus and 0 that
// page-zoom the whole webview (header included). On macOS those key equivalents are dispatched by the menu before the web
// content sees the key, so the page's preventDefault could never stop them: the items have to go. Reload, Full Screen and the
// rest are kept. (Windows' WebView2 zoom control is a window option, ZoomControlEnabled, off by default and set explicitly in
// windowOptions; WKWebView's pinch magnification and WebKitGTK have no keyboard zoom of their own.)
func appMenu() *application.Menu {
	return withoutWebviewZoom(application.DefaultApplicationMenu())
}

// withoutWebviewZoom removes the Zoom In, Zoom Out and Reset Zoom items wherever they sit in the menu tree, and the
// separators that would be left doubled up or dangling.
func withoutWebviewZoom(m *application.Menu) *application.Menu {
	for _, role := range []application.Role{application.ZoomIn, application.ZoomOut, application.ResetZoom} {
		for item := m.FindByRole(role); item != nil; item = m.FindByRole(role) {
			m.RemoveMenuItem(item)
		}
	}
	tidySeparators(m)
	return m
}

// tidySeparators drops a separator that follows another separator or opens a menu, in every submenu.
func tidySeparators(m *application.Menu) {
	prevSeparator := true // a separator at the start of a menu goes too
	var drop []*application.MenuItem
	for i := 0; ; i++ {
		item := m.ItemAt(i)
		if item == nil {
			break
		}
		if item.IsSeparator() {
			if prevSeparator {
				drop = append(drop, item)
			}
			prevSeparator = true
			continue
		}
		prevSeparator = false
		if sub := item.GetSubmenu(); sub != nil {
			tidySeparators(sub)
		}
	}
	for _, d := range drop {
		m.RemoveMenuItem(d)
	}
}
