package media_test

// Touch, Evict, Clear and Stats (architecture.md §2.4, data-model.md §2.5a attachment).
//
// SPEC-NOTE: eviction order. Originals are evicted one at a time until the sum of cached originals is <= capBytes()
// ("until under the cap"; a total exactly equal to the cap is within it). Rows that were never viewed
// (last_viewed_at NULL) go first, oldest attachment.created_at first (contract.go Evict); then viewed rows, oldest
// last_viewed_at first. Put does not set last_viewed_at: only Touch does. Open is not asserted to touch.
//
// SPEC-NOTE: an evicted row keeps its thumbnail (file and thumb_path), gets file_path NULL, cached_bytes 0 and
// evicted_at = now (ms); the original file is deleted. Evict's freedBytes is the sum of the cached_bytes it released.
//
// SPEC-NOTE: Stats.Bytes counts the cached originals (they equal the sum of cached_bytes), Stats.ThumbBytes the
// thumbnail files, Stats.Files originals + thumbnails on disk, Stats.CapBytes the current capBytes(). Any cached
// original counts against the cap, file cards included (Put's tests assert cached_bytes for them too), but the cap and
// eviction tests use image originals only so the arithmetic is exact.
//
// SPEC-NOTE: Clear removes every original and thumbnail file, nulls file_path and thumb_path, zeroes cached_bytes, and
// never deletes attachment rows (AGENTS invariant 6: nothing synced is deleted).
//
// SPEC-NOTE: Evict-after-Put removes only other rows' originals in these tests: the just-Put row is the newest by
// created_at and never viewed, so it is never first in line.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/store"
	"github.com/crueber/cuisle/internal/store/storetest"
)

func TestTouch_SetsLastViewedAtToNow(t *testing.T) {
	e := newEnv(t)
	e.seedPut(1, 1, "image/png", pngBytes(t, 200, 100, 1))
	e.seedPut(1, 2, "image/png", pngBytes(t, 200, 100, 2))

	if e.row(1).LastViewed.Valid {
		t.Fatal("precondition: Put must not mark an attachment as viewed")
	}

	e.c.Touch(1)
	r := e.row(1)
	if want := store.Ms(e.clock.Now()); !r.LastViewed.Valid || r.LastViewed.Int64 != want {
		t.Errorf("last_viewed_at = %+v, want %d (fake clock now)", r.LastViewed, want)
	}
	if e.row(2).LastViewed.Valid {
		t.Error("Touch(1) changed another row")
	}

	e.clock.Advance(5 * time.Minute)
	e.c.Touch(1)
	if want := store.Ms(e.clock.Now()); e.row(1).LastViewed.Int64 != want {
		t.Errorf("second Touch: last_viewed_at = %d, want %d", e.row(1).LastViewed.Int64, want)
	}

	e.c.Touch(987654) // unknown row: best effort, must not panic or change anything
	if storetest.Count(t, e.db, "attachment", "last_viewed_at IS NOT NULL") != 1 {
		t.Error("Touch of an unknown id changed rows")
	}
}

func TestEvict_LeastRecentlyViewedFirst_NeverThumbnails(t *testing.T) {
	e := newEnv(t)
	data := pngBytes(t, 640, 400, 7)
	s := int64(len(data))

	// ids: A=1 B=2 C=3 D=4. created_at: A 10, B 20, C 100, D 50.
	// B is viewed before A; C and D are never viewed. Expected eviction order: D, C (never viewed, oldest created
	// first, even though A and B are older), then B, then A (least recently viewed first).
	ids := map[string]int64{"A": 1, "B": 2, "C": 3, "D": 4}
	created := map[string]int64{"A": 10, "B": 20, "C": 100, "D": 50}
	for _, name := range []string{"A", "B", "C", "D"} {
		att := e.seed(1, ids[name], "image/png", created[name])
		e.mustPut(att, 1, data)
	}
	e.clock.Advance(time.Hour)
	e.c.Touch(ids["B"])
	e.clock.Advance(time.Hour)
	e.c.Touch(ids["A"])
	viewedA, viewedB := e.row(1).LastViewed, e.row(2).LastViewed

	thumbBytes := func() int64 {
		st, err := e.c.Stats(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return st.ThumbBytes
	}
	thumbsBefore := thumbBytes()
	if thumbsBefore <= 0 {
		t.Fatalf("Stats.ThumbBytes = %d before eviction, want > 0", thumbsBefore)
	}

	steps := []struct {
		name    string
		cap     int64
		freed   int64
		evicted []string // cumulative
	}{
		{"within the cap: nothing happens", 4 * s, 0, nil},
		{"one original too many: the oldest never-viewed goes", 3 * s, s, []string{"D"}},
		{"next never-viewed", 2 * s, s, []string{"D", "C"}},
		{"only viewed rows left: least recently viewed first", s, s, []string{"D", "C", "B"}},
		{"cap below one original: the last one goes", s - 1, s, []string{"D", "C", "B", "A"}},
	}
	for _, st := range steps {
		e.clock.Advance(time.Minute)
		e.setCap(st.cap)
		freed, err := e.c.Evict(context.Background())
		if err != nil {
			t.Fatalf("%s: Evict: %v", st.name, err)
		}
		if freed != st.freed {
			t.Errorf("%s: freed = %d, want %d", st.name, freed, st.freed)
		}
		gone := map[string]bool{}
		for _, n := range st.evicted {
			gone[n] = true
		}
		disk := files(t, e.dir)
		for name, id := range ids {
			r := e.row(id)
			if gone[name] {
				if r.FilePath.Valid || r.CachedBytes != 0 {
					t.Errorf("%s: %s row = %+v, want original evicted (file_path NULL, cached_bytes 0)", st.name, name, r)
				}
				if !r.EvictedAt.Valid {
					t.Errorf("%s: %s evicted_at is NULL", st.name, name)
				}
				if _, ok := disk[origPath(1, id)]; ok {
					t.Errorf("%s: %s original still on disk", st.name, name)
				}
			} else {
				if !r.FilePath.Valid || r.CachedBytes != s {
					t.Errorf("%s: %s row = %+v, want original kept", st.name, name, r)
				}
				if _, ok := disk[origPath(1, id)]; !ok {
					t.Errorf("%s: %s original missing from disk", st.name, name)
				}
			}
			// thumbnails are never evicted
			if !r.ThumbPath.Valid {
				t.Errorf("%s: %s thumb_path became NULL", st.name, name)
			}
			if _, ok := disk[thumbPath(1, id)]; !ok {
				t.Errorf("%s: %s thumbnail missing from disk", st.name, name)
			}
		}
		stats, err := e.c.Stats(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		remaining := int64(4 - len(st.evicted))
		if stats.Bytes != remaining*s || stats.ThumbBytes != thumbsBefore || stats.Files != int(remaining)+4 || stats.CapBytes != st.cap {
			t.Errorf("%s: Stats = %+v, want Bytes %d ThumbBytes %d Files %d CapBytes %d",
				st.name, stats, remaining*s, thumbsBefore, remaining+4, st.cap)
		}
	}

	// the viewed rows' LRU keys survive eviction untouched
	if e.row(1).LastViewed != viewedA || e.row(2).LastViewed != viewedB {
		t.Error("Evict changed last_viewed_at")
	}
	// evicted_at is the time of the eviction that took the row (fake clock): D at step 2, C at step 3, B at 4, A at 5
	if d, a := e.row(4).EvictedAt.Int64, e.row(1).EvictedAt.Int64; d >= a {
		t.Errorf("evicted_at D=%d should be earlier than A=%d", d, a)
	}

	for _, id := range ids {
		requireNotCached(t, e.c, id, false)
		if b, _ := openAll(t, e.c, id, true); len(b) == 0 {
			t.Errorf("thumbnail of %d is empty", id)
		}
	}
}

func TestEvict_AfterPut(t *testing.T) {
	cases := []struct {
		name        string
		touchFirst  bool
		wantEvicted int64 // row id whose original must be gone after the third Put
	}{
		{"oldest never-viewed original goes", false, 1},
		{"a viewed original outlives never-viewed ones", true, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			data := pngBytes(t, 500, 300, 3)
			s := int64(len(data))
			e.setCap(2 * s)

			e.seedPut(1, 1, "image/png", data) // created_at 1001
			e.seedPut(1, 2, "image/png", data) // 1002
			if st, _ := e.c.Stats(context.Background()); st.Bytes != 2*s {
				t.Fatalf("Stats.Bytes = %d after two Puts, want %d (at the cap, nothing evicted)", st.Bytes, 2*s)
			}
			for _, id := range []int64{1, 2} {
				if !e.row(id).FilePath.Valid {
					t.Fatalf("row %d lost its original while within the cap", id)
				}
			}
			if tc.touchFirst {
				e.clock.Advance(time.Minute)
				e.c.Touch(1)
			}
			att := e.seed(1, 3, "image/png", 5000) // newest by created_at
			ent := e.mustPut(att, 1, data)

			if ent.FilePath == "" {
				t.Errorf("the Put that triggered the eviction lost its own original: %+v", ent)
			}
			for _, id := range []int64{1, 2, 3} {
				r := e.row(id)
				if id == tc.wantEvicted {
					if r.FilePath.Valid || !r.EvictedAt.Valid || !r.ThumbPath.Valid {
						t.Errorf("row %d = %+v, want the original evicted and the thumbnail kept", id, r)
					}
				} else if !r.FilePath.Valid {
					t.Errorf("row %d lost its original, only %d should have", id, tc.wantEvicted)
				}
			}
			st, err := e.c.Stats(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if st.Bytes != 2*s || st.Files != 2+3 {
				t.Errorf("Stats = %+v, want Bytes %d (two originals) and Files 5 (two originals + three thumbnails)", st, 2*s)
			}
		})
	}
}

func TestEvict_CapChangeAppliesWithoutRestart(t *testing.T) {
	e := newEnv(t)
	data := pngBytes(t, 500, 300, 4)
	s := int64(len(data))
	for id := int64(1); id <= 3; id++ {
		e.seedPut(1, id, "image/png", data)
	}
	stats := func() (bytes, cap int64) {
		st, err := e.c.Stats(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return st.Bytes, st.CapBytes
	}

	if b, c := stats(); b != 3*s || c != hugeCap {
		t.Fatalf("Stats Bytes/CapBytes = %d/%d, want %d/%d", b, c, 3*s, hugeCap)
	}
	if freed, err := e.c.Evict(context.Background()); err != nil || freed != 0 {
		t.Fatalf("Evict under a huge cap = %d, %v, want 0, nil", freed, err)
	}

	e.setCap(s) // the app setting changed: same cache value, no restart
	if _, c := stats(); c != s {
		t.Errorf("Stats.CapBytes = %d after the cap changed, want %d", c, s)
	}
	freed, err := e.c.Evict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if freed != 2*s {
		t.Errorf("freed = %d, want %d (3 originals down to the 1 the new cap allows)", freed, 2*s)
	}
	if b, _ := stats(); b != s {
		t.Errorf("Stats.Bytes = %d, want %d", b, s)
	}

	e.setCap(hugeCap) // raised again: a new Put is kept
	e.seedPut(1, 4, "image/png", data)
	if b, _ := stats(); b != 2*s {
		t.Errorf("Stats.Bytes = %d after Put under the raised cap, want %d", b, 2*s)
	}
	if !e.row(4).FilePath.Valid {
		t.Error("the new Put lost its original under the raised cap")
	}
}

func TestClear_RemovesOriginalsAndThumbnailsAndResetsCacheState(t *testing.T) {
	e := newEnv(t)
	e.seedPut(1, 1, "image/png", pngBytes(t, 500, 300, 1))
	e.seedPut(2, 300, "image/jpeg", jpegBytes(t, 640, 480))
	e.seedPut(1, 3, "application/pdf", []byte("%PDF-1.4 clear me"))
	thumbOnly := e.seed(1, 4, "image/png", 100)
	if err := e.c.PutThumb(context.Background(), thumbOnly, 1, strings.NewReader(string(jpegBytes(t, 200, 100)))); err != nil {
		t.Fatal(err)
	}
	if n := len(files(t, e.dir)); n != 2+2+1+1 {
		t.Fatalf("precondition: %d files on disk, want 6", n)
	}
	rowsBefore := storetest.Count(t, e.db, "attachment", "")

	if err := e.c.Clear(context.Background()); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	if left := files(t, e.dir); len(left) != 0 {
		t.Errorf("files left after Clear: %v", left)
	}
	if n := storetest.Count(t, e.db, "attachment", ""); n != rowsBefore {
		t.Errorf("attachment rows = %d after Clear, want %d (never delete synced rows)", n, rowsBefore)
	}
	if n := storetest.Count(t, e.db, "attachment", "file_path IS NOT NULL OR thumb_path IS NOT NULL OR cached_bytes <> 0"); n != 0 {
		t.Errorf("%d rows still carry cache state after Clear", n)
	}
	for _, id := range []int64{1, 3, 4, 300} {
		requireNotCached(t, e.c, id, false)
		requireNotCached(t, e.c, id, true)
	}
	st, err := e.c.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Bytes != 0 || st.ThumbBytes != 0 || st.Files != 0 || st.CapBytes != hugeCap {
		t.Errorf("Stats after Clear = %+v, want zeros and CapBytes %d", st, hugeCap)
	}

	// the cache is usable again after a Clear
	att := e.seed(1, 5, "image/png", 200)
	if ent := e.mustPut(att, 1, pngBytes(t, 300, 200, 9)); !ent.Thumb {
		t.Errorf("Put after Clear = %+v, want a thumbnail", ent)
	}
}

func TestStats_CountsOriginalsThumbnailsFilesAndCap(t *testing.T) {
	e := newEnv(t)
	e.setCap(12345678)

	if st, err := e.c.Stats(context.Background()); err != nil || st.Bytes != 0 || st.ThumbBytes != 0 || st.Files != 0 || st.CapBytes != 12345678 {
		t.Fatalf("Stats on an empty cache = %+v, %v, want zeros and CapBytes 12345678", st, err)
	}

	png := pngBytes(t, 640, 400, 1)
	jpg := jpegBytes(t, 800, 600)
	e.seedPut(1, 1, "image/png", png)
	e.seedPut(2, 2, "image/jpeg", jpg)

	disk := files(t, e.dir)
	var wantOrig, wantThumb int64
	for name, size := range disk {
		switch {
		case strings.HasSuffix(name, ".orig"):
			wantOrig += size
		case strings.HasSuffix(name, ".thumb.jpg"):
			wantThumb += size
		default:
			t.Errorf("unexpected file %q", name)
		}
	}
	if wantOrig != int64(len(png)+len(jpg)) || wantThumb <= 0 {
		t.Fatalf("on-disk sizes: originals %d (want %d), thumbnails %d", wantOrig, len(png)+len(jpg), wantThumb)
	}
	st, err := e.c.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Bytes != wantOrig || st.ThumbBytes != wantThumb || st.Files != 4 || st.CapBytes != 12345678 {
		t.Errorf("Stats = %+v, want Bytes %d ThumbBytes %d Files 4 CapBytes 12345678", st, wantOrig, wantThumb)
	}
}
