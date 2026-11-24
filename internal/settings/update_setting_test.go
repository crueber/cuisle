package settings

import (
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
