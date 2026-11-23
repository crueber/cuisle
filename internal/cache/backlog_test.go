package cache_test

import (
	"reflect"
	"testing"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
)

// WP-38: a search result with no Meta must not wipe the board's stored source_meta.
func TestUpsertBoards_EmptyMetaKeepsExistingMeta(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdTwoConnections(t, db)
	at := rdTS("2026-03-18T12:00:00Z")
	meta := map[string]any{"filterId": float64(9001), "subQuery": "project = MOB"}

	upsert := func(name string, m map[string]any) {
		t.Helper()
		b := model.Board{ID: "1", Name: name, Kind: "scrum", RawType: "scrum", Meta: m}
		if err := cache.UpsertBoards(rdCtx, db.W, 1, []model.Board{b}, at); err != nil {
			t.Fatalf("UpsertBoards: %v", err)
		}
	}
	read := func() model.Board {
		t.Helper()
		got, err := cache.SearchBoards(rdCtx, db.R, 1, "", "", 10)
		if err != nil || len(got) != 1 {
			t.Fatalf("SearchBoards = %v, %v", got, err)
		}
		return got[0]
	}

	upsert("Service Board", meta)
	upsert("Service Board renamed", nil) // a search result: no Meta
	if b := read(); b.Name != "Service Board renamed" || !reflect.DeepEqual(b.Meta, meta) {
		t.Errorf("after an empty-meta upsert = %+v, want the new name and the old meta %v", b, meta)
	}
	newMeta := map[string]any{"filterId": float64(7)}
	upsert("Service Board renamed", newMeta)
	if b := read(); !reflect.DeepEqual(b.Meta, newMeta) {
		t.Errorf("a non-empty meta must replace the old one: got %v, want %v", b.Meta, newMeta)
	}
}
