package app_test

// SinkService: configured destinations over the sink registry, manual sends through the outbox, retries, and the rule
// that sink secrets live only in the keychain under sink:<id> (AGENTS.md section 3 invariant 5).
//
// Two sink kinds are used: the real "file" sink (writes into t.TempDir) and a test-registered "recorder" kind that
// needs a secret, records what it was asked to deliver and can be told to fail (harness_test.go).
//
// SPEC-NOTE: (Send) SinkService.Send enqueues in the outbox and runs one delivery job before returning, then returns
// the outbox id. A delivery that fails (after the dispatcher's own retries) is NOT an error of Send: the row is
// "failed" in the outbox for Retry or the next explicit trigger. The tests read the outcome from Outbox.
//
// SPEC-NOTE: (Send validation) only kind "current" is supported in v1 (file sink Capabilities.Range is false), so
// "range" is unsupported (validation accepted as the alternative code); an unknown sink or standup is not_found; and
// the file sink's "dir" setting is required (validation with Field "dir", straight from sink.Validate).
//
// SPEC-NOTE: (secrets) SinkInput.Secret nil keeps the stored secret, a non-nil value replaces it, "" removes it;
// SinkConfig.HasSecret reflects the keychain item; deleting a sink deletes its keychain item and its outbox rows.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/export"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/store/storetest"
)

func TestSinkKinds_ListsRegisteredAdaptersWithSchemaAndCapabilities(t *testing.T) {
	ta := newTestApp(t)
	kinds, err := ta.Svc.Sink.Kinds(ctx)
	noErr(t, err, "Kinds")
	var fileKind *app.SinkKind
	for i := range kinds {
		if kinds[i].Kind == "file" {
			fileKind = &kinds[i]
		}
	}
	if fileKind == nil {
		t.Fatalf("Kinds = %+v, want the file sink", kinds)
	}
	if fileKind.DisplayName == "" || !fileKind.Capabilities.Current || fileKind.Capabilities.NeedsSecret {
		t.Errorf("file kind = %+v, want a display name, current=true, needsSecret=false", *fileKind)
	}
	var dir bool
	for _, f := range fileKind.Schema {
		if f.Key == "dir" && f.Required {
			dir = true
		}
	}
	if !dir {
		t.Errorf("file schema = %+v, want a required dir field the UI can render", fileKind.Schema)
	}
	for _, k := range kinds {
		if k.Schema == nil {
			t.Errorf("kind %q has a nil schema; lists are never nil", k.Kind)
		}
	}
}

func TestSinkList_EmptyIsANonNilSlice(t *testing.T) {
	ta := newTestApp(t)
	list, err := ta.Svc.Sink.List(ctx)
	noErr(t, err, "List")
	if list == nil || len(list) != 0 {
		t.Errorf("List = %#v, want a non-nil empty slice", list)
	}
}

func TestSinkSave_CreatesAndUpdatesAFileSink(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	sc, err := ta.Svc.Sink.Save(ctx, app.SinkInput{
		Kind: "file", Name: "Standup folder", StandupID: id, Config: map[string]string{"dir": ta.SinkDir}, SendOn: "manual", Enabled: true,
	})
	noErr(t, err, "Save(create)")
	if sc.ID <= 0 || sc.Kind != "file" || sc.Name != "Standup folder" || sc.StandupID != id || sc.Config["dir"] != ta.SinkDir ||
		sc.HasSecret || sc.SendOn != "manual" || !sc.Enabled || sc.LastDelivery != nil {
		t.Errorf("created sink = %+v", sc)
	}
	upd, err := ta.Svc.Sink.Save(ctx, app.SinkInput{
		ID: sc.ID, Kind: "file", Name: "Renamed", StandupID: 0, Config: map[string]string{"dir": ta.SinkDir}, SendOn: "after_sync", Enabled: false,
	})
	noErr(t, err, "Save(update)")
	if upd.ID != sc.ID || upd.Name != "Renamed" || upd.StandupID != 0 || upd.SendOn != "after_sync" || upd.Enabled {
		t.Errorf("updated sink = %+v", upd)
	}
	list, _ := ta.Svc.Sink.List(ctx)
	if len(list) != 1 || list[0].ID != sc.ID || list[0].Name != "Renamed" {
		t.Errorf("List = %+v, want the one updated sink (an update must not duplicate)", list)
	}
}

func TestSinkSave_ValidatesAgainstTheKindSchema(t *testing.T) {
	ta := newTestApp(t)
	good := app.SinkInput{Kind: "file", Name: "Folder", Config: map[string]string{"dir": ta.SinkDir}, SendOn: "manual", Enabled: true}

	t.Run("missing dir", func(t *testing.T) {
		in := good
		in.Config = map[string]string{}
		ae := wantCode(t, saveErr(ta, in), apperr.Validation, "Save(no dir)")
		if ae.Field != "dir" {
			t.Errorf("Field = %q, want dir", ae.Field)
		}
	})
	t.Run("recorder rejects its bad label", func(t *testing.T) {
		in := app.SinkInput{Kind: recorderKind, Name: "R", Config: map[string]string{"label": "bad"}, SendOn: "manual", Enabled: true}
		ae := wantCode(t, saveErr(ta, in), apperr.Validation, "Save(bad label)")
		if ae.Field != "label" {
			t.Errorf("Field = %q, want label", ae.Field)
		}
	})
	t.Run("send on", func(t *testing.T) {
		in := good
		in.SendOn = "hourly"
		wantCode(t, saveErr(ta, in), apperr.Validation, "Save(bad sendOn)")
	})
	t.Run("empty name", func(t *testing.T) {
		in := good
		in.Name = "  "
		wantCode(t, saveErr(ta, in), apperr.Validation, "Save(empty name)")
	})
	t.Run("long name", func(t *testing.T) {
		in := good
		in.Name = strings.Repeat("n", 81)
		wantCode(t, saveErr(ta, in), apperr.Validation, "Save(81-char name)")
	})
	t.Run("unknown kind", func(t *testing.T) {
		in := good
		in.Kind = "carrier-pigeon"
		wantCodeOneOf(t, saveErr(ta, in), "Save(unknown kind)", apperr.Validation, apperr.NotFound)
	})
	if list, _ := ta.Svc.Sink.List(ctx); len(list) != 0 {
		t.Errorf("rejected saves created sinks: %+v", list)
	}
}

func saveErr(ta *testApp, in app.SinkInput) error {
	_, err := ta.Svc.Sink.Save(ctx, in)
	return err
}

func TestSinkSecret_LivesOnlyInTheKeychainUnderSinkID(t *testing.T) {
	ta := newTestApp(t, withFileDB())
	const secret = "sink-secret-4f9a1c"
	sc, err := ta.Svc.Sink.Save(ctx, app.SinkInput{
		Kind: recorderKind, Name: "R", Config: map[string]string{"label": "ok"}, Secret: ptr(secret), SendOn: "manual", Enabled: true,
	})
	noErr(t, err, "Save(with secret)")
	if !sc.HasSecret {
		t.Errorf("HasSecret = false after saving a secret")
	}
	account := secrets.SinkAccount(sc.ID)
	if got, err := ta.KR.Get(account); err != nil || got != secret {
		t.Errorf("keychain %q = %q, %v; want the secret", account, got, err)
	}
	scanDBForText(t, ta.DB, secret)
	scanFileBytes(t, ta.DB, secret)
	if strings.Contains(fmtJSON(sc), secret) {
		t.Errorf("the SinkConfig returned to the frontend contains the secret")
	}
	if strings.Contains(ta.Log.String(), secret) {
		t.Errorf("the log contains the sink secret")
	}

	// nil keeps it
	sc2, err := ta.Svc.Sink.Save(ctx, app.SinkInput{ID: sc.ID, Kind: recorderKind, Name: "R2", Config: map[string]string{"label": "ok"}, SendOn: "manual", Enabled: true})
	noErr(t, err, "Save(secret nil)")
	if got, _ := ta.KR.Get(account); got != secret || !sc2.HasSecret {
		t.Errorf("a nil Secret must leave the stored secret alone (kept %q, HasSecret %v)", got, sc2.HasSecret)
	}
	// "" removes it
	sc3, err := ta.Svc.Sink.Save(ctx, app.SinkInput{ID: sc.ID, Kind: recorderKind, Name: "R2", Config: map[string]string{"label": "ok"}, Secret: ptr(""), SendOn: "manual", Enabled: true})
	noErr(t, err, "Save(secret empty)")
	if _, err := ta.KR.Get(account); err == nil || sc3.HasSecret {
		t.Errorf("an empty Secret must delete the keychain item (HasSecret %v)", sc3.HasSecret)
	}
}

func TestSinkSend_FileSinkWritesTheExportIntoTheFolderAndRecordsTheDelivery(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	sc := ta.saveFileSink(id, "manual")

	outboxID, err := ta.Svc.Sink.Send(ctx, sc.ID, id, app.SendRequest{Kind: "current"})
	noErr(t, err, "Send")
	if outboxID <= 0 {
		t.Fatalf("outbox id = %d, want > 0", outboxID)
	}
	items, err := ta.Svc.Sink.Outbox(ctx, sc.ID, 10)
	noErr(t, err, "Outbox")
	if len(items) != 1 {
		t.Fatalf("Outbox = %+v, want the one delivery", items)
	}
	it := items[0]
	if it.ID != outboxID || it.SinkID != sc.ID || it.StandupID != id || it.Cause != "manual" || it.PayloadKind != "current" ||
		it.Status != "delivered" || it.Attempts < 1 || it.DeliveredAt == 0 || it.Receipt == "" || it.ErrorCode != "" {
		t.Errorf("outbox item = %+v, want a delivered manual/current row with a receipt", it)
	}
	files := ta.dirEntries()
	if len(files) != 1 {
		t.Fatalf("sink folder = %v, want exactly one file (no temp leftovers)", files)
	}
	if want := filepath.Join(ta.SinkDir, files[0]); it.Receipt != want {
		t.Errorf("Receipt = %q, want the absolute file path %q", it.Receipt, want)
	}
	raw, err := os.ReadFile(filepath.Join(ta.SinkDir, files[0]))
	noErr(t, err, "read export")
	var doc export.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the exported file is not JSON: %v", err)
	}
	if doc.Schema != export.Schema || doc.Kind != "current" || doc.Standup.ID != id || len(doc.Snapshots) != 1 || len(doc.Snapshots[0].Tickets) == 0 {
		t.Errorf("document = schema %q kind %q standup %d snapshots %d, want a current cuisle.export.v1 of the standup with tickets", doc.Schema, doc.Kind, doc.Standup.ID, len(doc.Snapshots))
	}
	for _, secret := range []string{fakeToken(), testAccountBasicAuth(), testAccount} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the export contains %q (exports carry no secrets or emails)", secret)
		}
	}
	list, _ := ta.Svc.Sink.List(ctx)
	if len(list) != 1 || list[0].LastDelivery == nil || list[0].LastDelivery.ID != outboxID || list[0].LastDelivery.Status != "delivered" {
		t.Errorf("List()[0].LastDelivery = %+v, want the delivered item", list)
	}
}

func TestSinkSend_RejectsWhatTheSinkCannotDo(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	sc := ta.saveFileSink(id, "manual")

	_, err := ta.Svc.Sink.Send(ctx, sc.ID, id, app.SendRequest{Kind: "range", From: "2026-11-01", To: "2026-11-23"})
	wantCodeOneOf(t, err, "Send(range)", apperr.Unsupported, apperr.Validation)
	_, err = ta.Svc.Sink.Send(ctx, 4242, id, app.SendRequest{Kind: "current"})
	wantCode(t, err, apperr.NotFound, "Send(unknown sink)")
	_, err = ta.Svc.Sink.Send(ctx, sc.ID, 4242, app.SendRequest{Kind: "current"})
	wantCode(t, err, apperr.NotFound, "Send(unknown standup)")
	if files := ta.dirEntries(); len(files) != 0 {
		t.Errorf("rejected sends wrote files: %v", files)
	}
}

func TestSinkRetry_RedeliversAFailedRowOnAnExplicitRequest(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	sc, err := ta.Svc.Sink.Save(ctx, app.SinkInput{
		Kind: recorderKind, Name: "R", StandupID: id, Config: map[string]string{"label": "ok"}, Secret: ptr("recorder-secret"), SendOn: "manual", Enabled: true,
	})
	noErr(t, err, "Save")

	theRecorder.setFail(true)
	outboxID, sendErr := ta.Svc.Sink.Send(ctx, sc.ID, id, app.SendRequest{Kind: "current"})
	if sendErr != nil {
		t.Logf("Send reported the failure directly: %v (allowed, the row must still exist)", sendErr)
	}
	items, err := ta.Svc.Sink.Outbox(ctx, sc.ID, 10)
	noErr(t, err, "Outbox")
	if len(items) != 1 {
		t.Fatalf("Outbox = %+v, want the failed row", items)
	}
	if items[0].Status != "failed" || items[0].ErrorCode != "sink_failed" || items[0].Error == "" || items[0].Attempts < 1 || items[0].DeliveredAt != 0 {
		t.Errorf("failed item = %+v, want status failed, errorCode sink_failed, a message, attempts >= 1", items[0])
	}
	outboxID = items[0].ID

	theRecorder.setFail(false)
	noErr(t, ta.Svc.Sink.Retry(ctx, outboxID), "Retry")
	items, _ = ta.Svc.Sink.Outbox(ctx, sc.ID, 10)
	if len(items) != 1 || items[0].Status != "delivered" || items[0].Receipt == "" || items[0].DeliveredAt == 0 {
		t.Errorf("after Retry = %+v, want one delivered row (same row, not a new one)", items)
	}
	got := theRecorder.deliveries()
	if len(got) != 1 || got[0].Secret != "recorder-secret" || got[0].Document.Schema != export.Schema {
		t.Errorf("recorder deliveries = %d (first %+v), want one with the keychain secret and a cuisle.export.v1 document", len(got), got)
	}
	wantCode(t, ta.Svc.Sink.Retry(ctx, 4242), apperr.NotFound, "Retry(unknown)")
}

func TestSinkOutbox_NewestFirstLimitedAndNeverNil(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	sc := ta.saveFileSink(id, "manual")
	empty, err := ta.Svc.Sink.Outbox(ctx, sc.ID, 10)
	noErr(t, err, "Outbox(empty)")
	if empty == nil || len(empty) != 0 {
		t.Errorf("empty outbox = %#v, want a non-nil empty slice", empty)
	}
	var ids []int64
	for i := 0; i < 3; i++ {
		ta.Clock.Advance(1_000_000_000) // distinct created_at
		oid, err := ta.Svc.Sink.Send(ctx, sc.ID, id, app.SendRequest{Kind: "current"})
		noErr(t, err, "Send")
		ids = append(ids, oid)
	}
	two, err := ta.Svc.Sink.Outbox(ctx, sc.ID, 2)
	noErr(t, err, "Outbox(2)")
	if len(two) != 2 || two[0].ID != ids[2] || two[1].ID != ids[1] {
		t.Errorf("Outbox(limit 2) = %+v, want the newest two [%d %d]", two, ids[2], ids[1])
	}
}

func TestSinkDelete_RemovesTheSinkItsOutboxAndItsKeychainItem(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	sc, err := ta.Svc.Sink.Save(ctx, app.SinkInput{
		Kind: recorderKind, Name: "R", StandupID: id, Config: map[string]string{"label": "ok"}, Secret: ptr("s3cret-del"), SendOn: "manual", Enabled: true,
	})
	noErr(t, err, "Save")
	_, err = ta.Svc.Sink.Send(ctx, sc.ID, id, app.SendRequest{Kind: "current"})
	noErr(t, err, "Send")
	if storetest.Count(t, ta.DB, "sink_outbox", "") != 1 {
		t.Fatalf("expected one outbox row before Delete")
	}

	noErr(t, ta.Svc.Sink.Delete(ctx, sc.ID), "Delete")
	if list, _ := ta.Svc.Sink.List(ctx); len(list) != 0 {
		t.Errorf("List after Delete = %+v", list)
	}
	if n := storetest.Count(t, ta.DB, "sink_outbox", ""); n != 0 {
		t.Errorf("outbox rows after Delete = %d, want 0 (cascade)", n)
	}
	if _, err := ta.KR.Get(secrets.SinkAccount(sc.ID)); err == nil {
		t.Errorf("the sink's keychain item survived Delete")
	}
}
