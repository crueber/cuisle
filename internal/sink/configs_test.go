package sink_test

// SPEC-NOTE (WP-21 configs):
//  1. An unknown (unregistered) kind is a validation error with Field "kind". An invalid sendOn is a validation error
//     with Field "sendOn"; a bad name is validation with Field "name". The empty-SendOn case is intentionally not tested.
//  2. The kind's Validate error (an *apperr.Error with Field set) is returned unchanged (code and Field preserved), and a
//     failed save writes no sink row and no keychain item.
//  3. Updating an unknown sink id is not_found. Updating keeps created_at and sets updated_at to the DB clock (db.Now).
//  4. A keychain failure while storing a secret surfaces as apperr keychain_unavailable (when the keyring reports
//     secrets.ErrUnavailable); the exact DB state afterwards is not asserted.
//  5. CONTRACT GAP: ListConfigs(ctx, db) has no Keyring, so Config.HasSecret cannot be derived there; only the Config
//     returned by SaveConfig is asserted for HasSecret (true iff the keychain holds "sink:<id>" after the call).
//  6. Outbox returns rows ordered by id descending (newest first) and omits payloads; CreatedAt/DeliveredAt are
//     time.UnixMilli values, DeliveredAt is the zero time when not delivered.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/sink"
	"github.com/crueber/cuisle/internal/store"
	"github.com/crueber/cuisle/internal/store/storetest"
)

type cfgEnv struct {
	t   *testing.T
	db  *store.DB
	kr  *memKeyring
	now time.Time
}

func newCfgEnv(t *testing.T) *cfgEnv {
	t.Helper()
	registerFake(t)
	c := &cfgEnv{t: t, kr: newKeyring(), now: fixedNow}
	c.db = storetest.Open(t, func() time.Time { return c.now })
	ms := fixedNow.UnixMilli()
	storetest.Exec(t, c.db, insertStandupSQL, 1, "Alpha", 1, ms, ms)
	return c
}

func strp(s string) *string { return &s }

func validInput() sink.ConfigInput {
	return sink.ConfigInput{
		Kind: fakeKind, Name: "Nightly", StandupID: 0, Config: map[string]string{"target": "out"},
		SendOn: "manual", Enabled: true,
	}
}

func (c *cfgEnv) save(in sink.ConfigInput) (sink.Config, error) {
	return sink.SaveConfig(context.Background(), c.db, c.kr, in)
}

func (c *cfgEnv) sinkRows() int { return storetest.Count(c.t, c.db, "sink", "") }

func TestSaveConfig_Create_StoresRowAndReturnsConfig(t *testing.T) {
	c := newCfgEnv(t)
	in := validInput()
	in.StandupID = 1
	in.SendOn = "after_sync"
	in.Enabled = false
	got, err := c.save(in)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if got.ID <= 0 || got.Kind != fakeKind || got.Name != "Nightly" || got.StandupID != 1 ||
		got.Config["target"] != "out" || got.SendOn != "after_sync" || got.Enabled || got.HasSecret {
		t.Errorf("Config = %+v", got)
	}
	if !got.CreatedAt.Equal(fixedNow) || !got.UpdatedAt.Equal(fixedNow) {
		t.Errorf("CreatedAt/UpdatedAt = %v/%v, want the DB clock %v", got.CreatedAt, got.UpdatedAt, fixedNow)
	}
	var kind, name, cfg, sendOn string
	var standup, enabled int64
	if err := c.db.W.QueryRow(`SELECT kind,name,config,send_on,standup_id,enabled FROM sink WHERE id=?`, got.ID).
		Scan(&kind, &name, &cfg, &sendOn, &standup, &enabled); err != nil {
		t.Fatal(err)
	}
	if kind != fakeKind || name != "Nightly" || sendOn != "after_sync" || standup != 1 || enabled != 0 ||
		!strings.Contains(cfg, `"target"`) || !strings.Contains(cfg, `"out"`) {
		t.Errorf("stored row = %s %s %s %s %d %d", kind, name, cfg, sendOn, standup, enabled)
	}
}

func TestSaveConfig_StandupZero_StoredAsNull(t *testing.T) {
	c := newCfgEnv(t)
	got, err := c.save(validInput())
	if err != nil {
		t.Fatal(err)
	}
	if got.StandupID != 0 {
		t.Errorf("StandupID = %d, want 0", got.StandupID)
	}
	if n := storetest.Count(t, c.db, "sink", "id = ? AND standup_id IS NULL", got.ID); n != 1 {
		t.Errorf("standup_id must be NULL for 'all standups' (matched %d rows)", n)
	}
}

func TestSaveConfig_NameBounds(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"one char", "a", false},
		{"eighty chars", strings.Repeat("a", 80), false},
		{"eighty-one chars", strings.Repeat("a", 81), true},
		{"empty", "", true},
		{"whitespace only", "   \t ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCfgEnv(t)
			in := validInput()
			in.Name = tc.in
			_, err := c.save(in)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !apperr.Is(err, apperr.Validation) {
				t.Fatalf("err = %v (code %q), want validation", err, apperr.CodeOf(err))
			}
			if f := apperr.From(err).Field; f != "name" {
				t.Errorf("Field = %q, want name", f)
			}
			if c.sinkRows() != 0 {
				t.Error("a rejected save must not write a row")
			}
		})
	}
}

func TestSaveConfig_InvalidSendOn_ValidationError(t *testing.T) {
	c := newCfgEnv(t)
	in := validInput()
	in.SendOn = "hourly"
	_, err := c.save(in)
	if !apperr.Is(err, apperr.Validation) || apperr.From(err).Field != "sendOn" {
		t.Fatalf("err = %v, want validation on sendOn", err)
	}
	if c.sinkRows() != 0 {
		t.Error("row written for invalid sendOn")
	}
	for _, ok := range []string{"manual", "after_sync"} {
		in.SendOn = ok
		if _, err := c.save(in); err != nil {
			t.Errorf("sendOn %q rejected: %v", ok, err)
		}
	}
}

func TestSaveConfig_UnknownKind_ValidationError(t *testing.T) {
	c := newCfgEnv(t)
	in := validInput()
	in.Kind = "no-such-kind-wp21"
	_, err := c.save(in)
	if !apperr.Is(err, apperr.Validation) || apperr.From(err).Field != "kind" {
		t.Fatalf("err = %v, want validation on kind", err)
	}
	if c.sinkRows() != 0 {
		t.Error("row written for unknown kind")
	}
}

func TestSaveConfig_KindValidateError_PropagatedWithFieldAndNothingWritten(t *testing.T) {
	c := newCfgEnv(t)
	in := validInput()
	in.Config = map[string]string{"target": "  "}
	in.Secret = strp("tok")
	_, err := c.save(in)
	if !apperr.Is(err, apperr.Validation) {
		t.Fatalf("err = %v, want validation", err)
	}
	if f := apperr.From(err).Field; f != "target" {
		t.Errorf("Field = %q, want target (the sink's Validate error unchanged)", f)
	}
	if c.sinkRows() != 0 {
		t.Error("row written despite failed Validate")
	}
	if len(c.kr.items) != 0 {
		t.Errorf("keychain written despite failed Validate: %v", c.kr.items)
	}
}

func TestSaveConfig_Secret_InKeychainUnderSinkIDAndNeverInDB(t *testing.T) {
	c := newCfgEnv(t)
	const secret = "tok-SECRET-9f3a"
	in := validInput()
	in.Secret = strp(secret)
	got, err := c.save(in)
	if err != nil {
		t.Fatal(err)
	}
	account := fmt.Sprintf("sink:%d", got.ID)
	if v, err := c.kr.Get(account); err != nil || v != secret {
		t.Fatalf("keychain %q = %q, %v", account, v, err)
	}
	if !got.HasSecret {
		t.Error("HasSecret = false after storing a secret")
	}
	// Scan every text/blob value in the sink table.
	rows, err := c.db.W.Query(`SELECT kind, name, config, send_on FROM sink`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a, b, cfg, d string
		if err := rows.Scan(&a, &b, &cfg, &d); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(a+b+cfg+d, secret) {
			t.Errorf("secret leaked into the sink table: %q %q %q %q", a, b, cfg, d)
		}
	}
}

func TestSaveConfig_SecretNilKeepsExisting_EmptyDeletes_ValueReplaces(t *testing.T) {
	c := newCfgEnv(t)
	in := validInput()
	in.Secret = strp("one")
	created, err := c.save(in)
	if err != nil {
		t.Fatal(err)
	}
	account := fmt.Sprintf("sink:%d", created.ID)

	upd := validInput()
	upd.ID = created.ID
	upd.Name = "Renamed"
	upd.Secret = nil
	got, err := c.save(upd)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := c.kr.Get(account); v != "one" || !got.HasSecret {
		t.Errorf("nil secret must keep the item: keychain=%q HasSecret=%v", v, got.HasSecret)
	}

	upd.Secret = strp("two")
	got, err = c.save(upd)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := c.kr.Get(account); v != "two" || !got.HasSecret {
		t.Errorf("value must replace: keychain=%q HasSecret=%v", v, got.HasSecret)
	}

	upd.Secret = strp("")
	got, err = c.save(upd)
	if err != nil {
		t.Fatal(err)
	}
	if c.kr.has(account) || got.HasSecret {
		t.Errorf("empty secret must delete the item: has=%v HasSecret=%v", c.kr.has(account), got.HasSecret)
	}
}

func TestSaveConfig_Update_KeepsCreatedAtAndBumpsUpdatedAt(t *testing.T) {
	c := newCfgEnv(t)
	created, err := c.save(validInput())
	if err != nil {
		t.Fatal(err)
	}
	c.now = fixedNow.Add(2 * time.Hour)
	upd := validInput()
	upd.ID = created.ID
	upd.Name = "Renamed"
	upd.SendOn = "after_sync"
	upd.Config = map[string]string{"target": "elsewhere"}
	got, err := c.save(upd)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.Name != "Renamed" || got.SendOn != "after_sync" || got.Config["target"] != "elsewhere" {
		t.Errorf("updated Config = %+v", got)
	}
	if !got.CreatedAt.Equal(fixedNow) || !got.UpdatedAt.Equal(c.now) {
		t.Errorf("CreatedAt/UpdatedAt = %v/%v, want %v/%v", got.CreatedAt, got.UpdatedAt, fixedNow, c.now)
	}
	if c.sinkRows() != 1 {
		t.Errorf("sink rows = %d, want 1 (update, not insert)", c.sinkRows())
	}
}

func TestSaveConfig_UpdateUnknownID_NotFound(t *testing.T) {
	c := newCfgEnv(t)
	in := validInput()
	in.ID = 12345
	if _, err := c.save(in); !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("err = %v (code %q), want not_found", err, apperr.CodeOf(err))
	}
	if c.sinkRows() != 0 {
		t.Error("update of an unknown id must not insert")
	}
}

func TestSaveConfig_KeychainUnavailable_Error(t *testing.T) {
	c := newCfgEnv(t)
	c.kr.err = secrets.ErrUnavailable
	in := validInput()
	in.Secret = strp("tok")
	_, err := c.save(in)
	if !apperr.Is(err, apperr.KeychainUnavailable) {
		t.Fatalf("err = %v (code %q), want keychain_unavailable", err, apperr.CodeOf(err))
	}
}

func TestListConfigs_OrderedByID_WithDecodedFields(t *testing.T) {
	c := newCfgEnv(t)
	a := validInput()
	a.Name = "A"
	b := validInput()
	b.Name = "B"
	b.StandupID = 1
	b.SendOn = "after_sync"
	b.Enabled = false
	b.Config = map[string]string{"target": "bee"}
	for _, in := range []sink.ConfigInput{a, b} {
		if _, err := c.save(in); err != nil {
			t.Fatal(err)
		}
	}
	got, err := sink.ListConfigs(context.Background(), c.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID >= got[1].ID || got[0].Name != "A" || got[1].Name != "B" {
		t.Errorf("order = %+v", got)
	}
	if got[0].StandupID != 0 || got[0].SendOn != "manual" || !got[0].Enabled || got[0].Config["target"] != "out" || got[0].Kind != fakeKind {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].StandupID != 1 || got[1].SendOn != "after_sync" || got[1].Enabled || got[1].Config["target"] != "bee" {
		t.Errorf("second = %+v", got[1])
	}
	if !got[0].CreatedAt.Equal(fixedNow) {
		t.Errorf("CreatedAt = %v", got[0].CreatedAt)
	}
}

func TestListConfigs_Empty_NoError(t *testing.T) {
	c := newCfgEnv(t)
	got, err := sink.ListConfigs(context.Background(), c.db)
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want empty, nil", got, err)
	}
}

func TestDeleteConfig_RemovesRowOutboxCascadeAndKeychainItem(t *testing.T) {
	c := newCfgEnv(t)
	in := validInput()
	in.Secret = strp("tok")
	keep, err := c.save(validInput())
	if err != nil {
		t.Fatal(err)
	}
	doomed, err := c.save(in)
	if err != nil {
		t.Fatal(err)
	}
	ms := fixedNow.UnixMilli()
	payload := `{"schema":"cuisle.export.v1"}`
	storetest.Exec(t, c.db, insertOutboxSQL, 1, doomed.ID, 1, "manual", "current", "k-doomed", payload, "failed", 3, ms, ms)
	storetest.Exec(t, c.db, insertOutboxSQL, 2, keep.ID, 1, "manual", "current", "k-keep", payload, "failed", 3, ms, ms)
	account := fmt.Sprintf("sink:%d", doomed.ID)
	if !c.kr.has(account) {
		t.Fatal("setup: secret missing")
	}

	if err := sink.DeleteConfig(context.Background(), c.db, c.kr, doomed.ID); err != nil {
		t.Fatal(err)
	}
	if n := storetest.Count(t, c.db, "sink", "id = ?", doomed.ID); n != 0 {
		t.Error("sink row still present")
	}
	if n := storetest.Count(t, c.db, "sink_outbox", "sink_id = ?", doomed.ID); n != 0 {
		t.Error("outbox rows must cascade")
	}
	if c.kr.has(account) {
		t.Error("keychain item must be removed")
	}
	if storetest.Count(t, c.db, "sink", "id = ?", keep.ID) != 1 || storetest.Count(t, c.db, "sink_outbox", "sink_id = ?", keep.ID) != 1 {
		t.Error("other sinks and their outbox rows must be untouched")
	}
}

func TestOutbox_NewestFirstWithLimitAndFieldMapping(t *testing.T) {
	c := newCfgEnv(t)
	a, _ := c.save(validInput())
	b, _ := c.save(validInput())
	ms := fixedNow.UnixMilli()
	payload := `{"schema":"cuisle.export.v1"}`
	// ids ascend with created_at; sink b's row must not leak into a's listing.
	storetest.Exec(t, c.db, insertOutboxSQL, 1, a.ID, 1, "manual", "current", "k1", payload, "failed", 3, ms-3000, ms-3000)
	storetest.Exec(t, c.db, insertOutboxSQL, 2, a.ID, 1, "after_sync", "current", "k2", payload, "pending", 0, ms-2000, ms-2000)
	storetest.Exec(t, c.db, insertOutboxSQL, 3, b.ID, 1, "manual", "current", "k3", payload, "pending", 0, ms-1500, ms-1500)
	storetest.Exec(t, c.db, insertOutboxSQL, 4, a.ID, 1, "manual", "range", "k4", payload, "pending", 0, ms-1000, ms-1000)
	storetest.Exec(t, c.db, `UPDATE sink_outbox SET status='delivered', attempts=1, receipt='/tmp/x.json', delivered_at=? WHERE id=4`, ms-900)
	storetest.Exec(t, c.db, `UPDATE sink_outbox SET last_error_code='sink_failed', last_error='nope' WHERE id=1`)

	got, err := sink.Outbox(context.Background(), c.db, a.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 4 || got[1].ID != 2 {
		t.Fatalf("ids = %+v, want [4 2] (newest first, limit 2, sink-scoped)", got)
	}
	d := got[0]
	if d.SinkID != a.ID || d.StandupID != 1 || d.Cause != "manual" || d.PayloadKind != "range" || d.Status != "delivered" ||
		d.Attempts != 1 || d.Receipt != "/tmp/x.json" {
		t.Errorf("delivered item = %+v", d)
	}
	if !d.CreatedAt.Equal(time.UnixMilli(ms-1000)) || !d.DeliveredAt.Equal(time.UnixMilli(ms-900)) {
		t.Errorf("times = %v / %v", d.CreatedAt, d.DeliveredAt)
	}
	if got[1].Cause != "after_sync" || !got[1].DeliveredAt.IsZero() {
		t.Errorf("pending item = %+v", got[1])
	}

	all, err := sink.Outbox(context.Background(), c.db, a.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[2].ID != 1 || all[2].ErrorCode != "sink_failed" || all[2].Error != "nope" {
		t.Errorf("all = %+v", all)
	}
}

func TestOutbox_NoRows_EmptyNoError(t *testing.T) {
	c := newCfgEnv(t)
	got, err := sink.Outbox(context.Background(), c.db, 1, 10)
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}
