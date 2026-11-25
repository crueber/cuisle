package settings

import (
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
)

// "Check for updates daily" (ADR 0016): on by default, "on"/"off" only.
func TestUpdateCheckDaily_DefaultsOnAndValidates(t *testing.T) {
	e := newW16(t)
	if !e.s.UpdateCheckDaily(e.ctx) {
		t.Fatal("default must be on")
	}
	if _, err := e.s.SetAppSetting(e.ctx, KeyUpdateCheckDaily, "off"); err != nil {
		t.Fatal(err)
	}
	if e.s.UpdateCheckDaily(e.ctx) {
		t.Fatal("off not honoured")
	}
	if _, err := e.s.SetAppSetting(e.ctx, KeyUpdateCheckDaily, "on"); err != nil {
		t.Fatal(err)
	}
	if !e.s.UpdateCheckDaily(e.ctx) {
		t.Fatal("on not honoured")
	}
	if _, err := e.s.SetAppSetting(e.ctx, KeyUpdateCheckDaily, "weekly"); !apperr.Is(err, apperr.Validation) {
		t.Fatalf("bad value: %v", err)
	}
}

// The palette's recents and command usage (F14, ux-spec §15.7): well-formed JSON of the right shape only.
func TestPaletteSettings_ValidateShape(t *testing.T) {
	e := newW16(t)
	good := map[string][]string{
		KeyPaletteRecentTickets: {"", "[]", "[101,7,9]"},
		KeyPaletteCommandUsage:  {"", "{}", `{"sync.resync":3,"view.group":0}`},
	}
	for k, vs := range good {
		for _, v := range vs {
			if _, err := e.s.SetAppSetting(e.ctx, k, v); err != nil {
				t.Errorf("%s=%q rejected: %v", k, v, err)
			}
		}
	}
	bad := map[string][]string{
		KeyPaletteRecentTickets: {"null", "{}", `["MOB-1"]`, "[0]", "[-3]", "[1.5]", "[1,", "42", "[" + strings.Repeat("1,", 60) + "1]"},
		KeyPaletteCommandUsage:  {"null", "[]", `{"a":"x"}`, `{"a":-1}`, `{"":1}`, `{"a":1.5}`, "{", `{"` + strings.Repeat("k", 81) + `":1}`},
	}
	for k, vs := range bad {
		for _, v := range vs {
			if _, err := e.s.SetAppSetting(e.ctx, k, v); !apperr.Is(err, apperr.Validation) {
				t.Errorf("%s=%q accepted: %v", k, v, err)
			}
		}
	}
}
