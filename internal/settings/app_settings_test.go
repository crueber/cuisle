package settings

// WP-16 tests: app settings (architecture.md §6.4).
// Run only this WP's tests with:
//   go test -tags purego ./internal/settings -run 'TestStandup|TestEffective|TestAppSettings|TestSetAppSetting|TestUIState'
//
// SPEC-NOTE: SetAppSetting validation errors carry Field = the setting key for a bad value, and Field = "key" for an
// unknown key (the contract only says "validation"; the patch-field-name rule is applied to the parameter names).
// SPEC-NOTE: history_retention_days is reserved (no UI, architecture §6.4); it is returned by AppSettings with its default
// but SetAppSetting's behaviour for it is not tested.

import (
	"reflect"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/store"
)

func TestAppSettings_FreshDatabaseReturnsDefaults(t *testing.T) {
	e := newW16(t)
	got, err := e.s.AppSettings(e.ctx)
	if err != nil {
		t.Fatalf("AppSettings: %v", err)
	}
	want := map[string]string{
		"theme": "system", "text_size": "standard", "sync_on_open": "stale", "sync_stale_hours": "6",
		"history_retention_days": "0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AppSettings = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(got, AppDefaults) {
		t.Errorf("AppSettings = %v, want AppDefaults %v", got, AppDefaults)
	}
}

func TestAppSettings_StoredValuesOverrideOnlyTheirOwnKey(t *testing.T) {
	e := newW16(t)
	if _, err := e.s.SetAppSetting(e.ctx, KeyTheme, "dark"); err != nil {
		t.Fatal(err)
	}
	got, err := e.s.AppSettings(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"theme": "dark", "text_size": "standard", "sync_on_open": "stale", "sync_stale_hours": "6",
		"history_retention_days": "0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AppSettings = %v, want %v", got, want)
	}
}

func TestAppSettings_ReturnsAMapTheCallerMayMutate(t *testing.T) {
	e := newW16(t)
	got, err := e.s.AppSettings(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	got[KeyTheme] = "mutated"
	if AppDefaults[KeyTheme] != "system" {
		t.Error("AppSettings must return a copy, not the AppDefaults map itself")
	}
}

func TestSetAppSetting_AcceptsEveryValidValue(t *testing.T) {
	cases := []struct{ key, value string }{
		{KeyTheme, "light"}, {KeyTheme, "dark"}, {KeyTheme, "system"},
		{KeyTextSize, "standard"}, {KeyTextSize, "large"},
		{KeySyncOnOpen, "stale"}, {KeySyncOnOpen, "always"}, {KeySyncOnOpen, "never"},
		{KeySyncStaleHours, "1"}, {KeySyncStaleHours, "6"}, {KeySyncStaleHours, "24"}, {KeySyncStaleHours, "48"},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			e := newW16(t)
			ack, err := e.s.SetAppSetting(e.ctx, tc.key, tc.value)
			if err != nil {
				t.Fatalf("SetAppSetting: %v", err)
			}
			if ack.Rev != 0 || ack.NeedsSync {
				t.Errorf("Ack = %+v, want Rev 0 and NeedsSync false", ack)
			}
			if !ack.SavedAt.Equal(e.clk.Now()) {
				t.Errorf("Ack.SavedAt = %v, want %v", ack.SavedAt, e.clk.Now())
			}
			got, err := e.s.AppSettings(e.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got[tc.key] != tc.value {
				t.Errorf("stored %s = %q, want %q", tc.key, got[tc.key], tc.value)
			}
		})
	}
}

func TestSetAppSetting_RejectsInvalidValues(t *testing.T) {
	cases := []struct{ key, value, field string }{
		{KeyTheme, "blue", KeyTheme},
		{KeyTheme, "", KeyTheme},
		{KeyTextSize, "huge", KeyTextSize},
		{KeyTextSize, "", KeyTextSize},
		{KeySyncOnOpen, "sometimes", KeySyncOnOpen},
		{KeySyncOnOpen, "", KeySyncOnOpen},
		{KeySyncStaleHours, "0", KeySyncStaleHours},
		{KeySyncStaleHours, "49", KeySyncStaleHours},
		{KeySyncStaleHours, "-1", KeySyncStaleHours},
		{KeySyncStaleHours, "abc", KeySyncStaleHours},
		{KeySyncStaleHours, "", KeySyncStaleHours},
		{KeySyncStaleHours, "1.5", KeySyncStaleHours},
		{KeySyncStaleHours, "6h", KeySyncStaleHours},
		{"nonsense_key", "x", "key"},
		{"", "x", "key"},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			e := newW16(t)
			_, err := e.s.SetAppSetting(e.ctx, tc.key, tc.value)
			w16RequireValidation(t, err, tc.field)
			if n := e.int(`SELECT count(*) FROM app_setting`); n != 0 {
				t.Errorf("a rejected value must not be written (app_setting rows: %d)", n)
			}
		})
	}
}

func TestSetAppSetting_UpsertsOneRowPerKey(t *testing.T) {
	e := newW16(t)
	if _, err := e.s.SetAppSetting(e.ctx, KeyTheme, "dark"); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(time.Hour)
	if _, err := e.s.SetAppSetting(e.ctx, KeyTheme, "light"); err != nil {
		t.Fatal(err)
	}
	if n := e.int(`SELECT count(*) FROM app_setting WHERE key = 'theme'`); n != 1 {
		t.Errorf("theme rows = %d, want 1", n)
	}
	if got := e.str(`SELECT value FROM app_setting WHERE key = 'theme'`); got != "light" {
		t.Errorf("theme = %q, want light", got)
	}
	if got := e.int(`SELECT updated_at FROM app_setting WHERE key = 'theme'`); got != store.Ms(e.clk.Now()) {
		t.Errorf("updated_at = %d, want %d", got, store.Ms(e.clk.Now()))
	}
}

func TestSetAppSetting_InvalidAfterValidKeepsPreviousValue(t *testing.T) {
	e := newW16(t)
	if _, err := e.s.SetAppSetting(e.ctx, KeySyncStaleHours, "12"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SetAppSetting(e.ctx, KeySyncStaleHours, "99"); !apperr.Is(err, apperr.Validation) {
		t.Fatalf("want validation, got %v", err)
	}
	got, _ := e.s.AppSettings(e.ctx)
	if got[KeySyncStaleHours] != "12" {
		t.Errorf("sync_stale_hours = %q, want the earlier valid 12", got[KeySyncStaleHours])
	}
}

func TestSetAppSetting_DoesNotTouchAnyStandupRev(t *testing.T) {
	e := newW16(t)
	id := e.create()
	before := e.rev(id)
	if _, err := e.s.SetAppSetting(e.ctx, KeyTheme, "dark"); err != nil {
		t.Fatal(err)
	}
	if e.rev(id) != before {
		t.Error("app settings are not standup config; standup.rev must not change")
	}
}
