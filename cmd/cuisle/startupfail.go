package main

import (
	"errors"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/crueber/cuisle/internal/apperr"
)

// showErrorDialog is replaceable in tests (a real one needs a GUI session).
var showErrorDialog = showNativeErrorDialog

// exitProcess ends the process after the startup dialog (os.Exit; replaceable for completeness).
var exitProcess = os.Exit

// failStartup reports a failure that happens before the window exists (architecture 6.3). stderr always gets the
// detail. Under --bench-startup it prints the JSON error report and never shows a dialog; otherwise a native error
// dialog tells the user what happened before the process exits 1.
func failStartup(opts options, what string, err error) int {
	code := fail(what, err)
	if opts.benchStartup {
		writeReport(os.Stdout, benchReport{Error: what + ": " + err.Error()})
		return code
	}
	showErrorDialog("Cuisle could not start", startupMessage(what, err))
	return code
}

// newerSchemaMessage is the message store.Migrate gives a database made by a newer Cuisle.
const newerSchemaMessage = "This data was created by a newer Cuisle"

// startupMessage is the dialog text: the app's own user-facing message for typed errors (for example "This data was
// created by a newer Cuisle"), never raw detail.
func startupMessage(what string, err error) string {
	var ae *apperr.Error
	if errors.As(err, &ae) && ae.Message != "" {
		if ae.Code == apperr.DB && strings.TrimRight(ae.Message, ".") == newerSchemaMessage {
			return strings.TrimRight(ae.Message, ".") + ". Update Cuisle to open it; nothing was changed."
		}
		return ae.Message
	}
	return "Cuisle " + what + ". Details were written to the error output and the log."
}

// showNativeErrorDialog starts a minimal application with no services and no window, only to raise the dialog (the
// dialog APIs need a running platform application), then exits the process with status 1 right after the dialog: on
// macOS Quit ends the process with status 0 from inside Run, which would report a failed start as a success.
func showNativeErrorDialog(title, message string) {
	defer func() { _ = recover() }() // a dialog failure must never mask the original exit
	a := application.New(application.Options{
		Name: "Cuisle",
		Mac:  application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
	})
	a.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		a.Dialog.Error().SetTitle(title).SetMessage(message).Show()
		exitProcess(1)
	})
	_ = a.Run()
}
