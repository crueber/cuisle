package media_test

// Orphan sweep (WP-T3, F12 freeze notes): Evict also removes files under the media dir that no attachment row
// references through file_path or thumb_path, and counts their bytes in freedBytes. Files outside the media dir are
// never touched.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEvict_SweepsOrphansOnlyInsideTheMediaDir(t *testing.T) {
	cases := []struct {
		name string
		rel  string // orphan path relative to the media dir
		data []byte
	}{
		{"orphan original", "7/3/999.orig", []byte("orphan original bytes")},
		{"orphan thumbnail", "7/3/999.thumb.jpg", []byte("orphan thumb bytes")},
		{"leftover temp file", "7/3/.tmp-12345", []byte("half written")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			_, ent := e.seedPut(1, 5, "image/png", pngBytes(t, 300, 200, 1))
			thumbBefore := files(t, e.dir)[filepath.ToSlash(ent.ThumbPath)]
			if thumbBefore <= 0 {
				t.Fatalf("precondition: no thumbnail for the referenced row (%+v)", ent)
			}

			orphanPath := filepath.Join(e.dir, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(orphanPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(orphanPath, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			// a stray file next to the media dir: the sweep must never reach it
			stray := filepath.Join(e.root, "stray.orig")
			if err := os.WriteFile(stray, []byte("not ours"), 0o600); err != nil {
				t.Fatal(err)
			}

			freed, err := e.c.Evict(context.Background())
			if err != nil {
				t.Fatalf("Evict: %v", err)
			}
			if want := int64(len(tc.data)); freed != want {
				t.Errorf("freed = %d, want %d (the orphan's bytes)", freed, want)
			}
			if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
				t.Errorf("orphan %s still exists (stat err %v)", tc.rel, err)
			}
			disk := files(t, e.dir)
			if _, ok := disk[origPath(1, 5)]; !ok {
				t.Error("referenced original was removed by the sweep")
			}
			if _, ok := disk[thumbPath(1, 5)]; !ok {
				t.Error("referenced thumbnail was removed by the sweep")
			}
			if got, err := os.ReadFile(stray); err != nil || string(got) != "not ours" {
				t.Errorf("stray file outside the media dir was touched: %q, %v", got, err)
			}
		})
	}
}

func TestEvict_SweepLeavesNoTempFilesBehind(t *testing.T) {
	e := newEnv(t)
	e.seedPut(1, 9, "image/png", pngBytes(t, 120, 90, 2))
	if _, err := e.c.Evict(context.Background()); err != nil {
		t.Fatalf("Evict: %v", err)
	}
	for name := range files(t, e.dir) {
		if filepath.Ext(name) != ".orig" && filepath.Ext(name) != ".jpg" {
			t.Errorf("unexpected file after Evict: %s", name)
		}
	}
}
