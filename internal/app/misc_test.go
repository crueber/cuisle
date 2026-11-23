package app_test

// HistoryService placeholder and the error model as seen through the services (architecture.md section 3.4).

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
)

func TestHistoryService_IsBoundButNotImplementedInV1(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	mark := ta.networkCalls()
	ta.Fake.ResetRequests()

	_, err := ta.Svc.History.Range(ctx, id)
	wantCode(t, err, apperr.NotImplementedCode, "History.Range")
	_, err = ta.Svc.History.ViewAsOf(ctx, id, "2026-11-20")
	wantCode(t, err, apperr.NotImplementedCode, "History.ViewAsOf")
	ta.assertNoNetwork(mark, "HistoryService")
}

func TestErrors_MarshalRoundTripsTheTypedErrorAServiceReturned(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	_, err := ta.Svc.Config.Update(ctx, id, standupPatch{PulseDays: ptr(10)})
	ae := wantCode(t, err, apperr.Validation, "Update")

	raw := apperr.Marshal(err)
	var fields map[string]json.RawMessage
	if jerr := json.Unmarshal(raw, &fields); jerr != nil {
		t.Fatalf("Marshal produced invalid JSON: %v (%s)", jerr, raw)
	}
	for _, k := range []string{"code", "message", "detail", "retryable", "field"} {
		if _, ok := fields[k]; !ok {
			t.Errorf("marshalled error lacks %q: %s", k, raw)
		}
	}
	var back apperr.Error
	noErr(t, json.Unmarshal(raw, &back), "decode")
	if back.Code != apperr.Validation || back.Field != "pulseDays" || back.Message != ae.Message || back.Retryable {
		t.Errorf("round trip = %+v, want validation/pulseDays/%q/not retryable", back, ae.Message)
	}
	if err.Error() != "validation: "+ae.Message {
		t.Errorf("Error() = %q, want only \"code: message\" (Wails logs it)", err.Error())
	}
}

func TestErrors_ServicesReturnTypedErrorsWithoutLeakingStorageDetail(t *testing.T) {
	ta := newTestApp(t)
	calls := map[string]func() error{
		"Standup.View":  func() error { _, err := ta.Svc.Standup.View(ctx, 4242); return err },
		"Config.Get":    func() error { _, err := ta.Svc.Config.Get(ctx, 4242); return err },
		"Config.Update": func() error { _, err := ta.Svc.Config.Update(ctx, 4242, standupPatch{PulseDays: ptr(14)}); return err },
		"Config.Delete": func() error { _, err := ta.Svc.Config.Delete(ctx, 4242); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			var ae *apperr.Error
			if !errors.As(err, &ae) || ae.Code != apperr.NotFound {
				t.Fatalf("error = %#v, want a typed not_found", err)
			}
			low := strings.ToLower(err.Error() + " " + ae.Message)
			for _, leak := range []string{"sql:", "no rows", "sqlite", "select ", "constraint"} {
				if strings.Contains(low, leak) {
					t.Errorf("the user-facing error leaks storage detail (%q): %q", leak, err.Error())
				}
			}
		})
	}
}
