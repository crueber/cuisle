package app_test

import (
	"testing"

	"github.com/crueber/cuisle/internal/cache"
)

// WP-38: SetBoard calls Reference() on the source; the field ids it discovers are kept in connection.source_meta
// (and the dev-activity state is not disturbed).
func TestSetBoard_PersistsDiscoveredSourceMeta(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()
	before, err := cache.GetConnection(ctx, ta.DB.R)
	noErr(t, err, "GetConnection before")
	if _, ok := before.SourceMeta["fieldsFetchedAt"]; ok {
		t.Fatalf("source_meta already has fieldsFetchedAt before any board pick: %v", before.SourceMeta)
	}

	ta.newStandup(ta.Fx.scrumProject, ta.Fx.scrum) // Create, SetSpace, SetBoard

	after, err := cache.GetConnection(ctx, ta.DB.R)
	noErr(t, err, "GetConnection after")
	if _, ok := after.SourceMeta["fieldsFetchedAt"]; !ok {
		t.Errorf("source_meta = %v, want the discovered field ids (fieldsFetchedAt) saved by SetBoard", after.SourceMeta)
	}
	if after.DevActivityState != before.DevActivityState {
		t.Errorf("dev activity state = %q, want it unchanged (%q)", after.DevActivityState, before.DevActivityState)
	}
}
