package media_test

// Shared fixtures and helpers for the media cache tests (WP-T3 tests).
//
// SPEC-NOTE: every test drives the cache through mustPut (or an equivalent that Fatalf's on error) so that the
// contract-freeze stub, whose Open/Touch/Evict/Stats "succeed" trivially, cannot satisfy a test by accident.
//
// SPEC-NOTE: timestamps in the attachment table are Unix milliseconds UTC (store.Ms), like every other column.
//
// SPEC-NOTE: the cache is rooted at the media dir itself (New's `dir` is <data dir>/media), so Entry.FilePath and the
// file_path/thumb_path columns are "<connection_id>/<row id mod 256>/<row id>.orig" and "….thumb.jpg", relative to
// that dir. They are compared through filepath.ToSlash so the separator in the column does not matter.

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/image/bmp"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/media"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
	"github.com/crueber/cuisle/internal/store/storetest"
)

// fakeClock is the Options.Now source.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

const hugeCap = int64(1) << 40

// env is one cache over a migrated in-memory DB and a temp media dir.
type env struct {
	t     *testing.T
	root  string // temp dir; the media dir is root/media so escapes are visible next to it
	dir   string
	db    *store.DB
	clock *fakeClock
	cap   atomic.Int64
	c     media.Cache
	conns map[int64]bool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, conns: map[int64]bool{}}
	e.clock = &fakeClock{t: time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)}
	e.db = storetest.Open(t, e.clock.Now)
	e.root = t.TempDir()
	e.dir = filepath.Join(e.root, "media")
	e.cap.Store(hugeCap)
	e.c = media.New(e.dir, e.db, func() int64 { return e.cap.Load() }, media.Options{Now: e.clock.Now})
	return e
}

func (e *env) setCap(n int64) { e.cap.Store(n) }

// seed inserts connection connID (once), its issue, and the attachment row id, then returns the model attachment.
// Call it from the test goroutine only.
func (e *env) seed(connID, id int64, mime string, createdMs int64) model.Attachment {
	e.t.Helper()
	if !e.conns[connID] {
		storetest.Exec(e.t, e.db, `INSERT INTO connection(id, base_url, account, created_at) VALUES (?, ?, 'dev@example.com', 0)`,
			connID, fmt.Sprintf("https://site%d.example.net", connID))
		storetest.Exec(e.t, e.db, `INSERT INTO issue(id, connection_id, source_id, key, space_id, summary, issue_type_id, status_id,
			created_at, updated_at, hydrated_at, last_seen_at) VALUES (?, ?, ?, ?, '1', 's', 't', 's', 0, 0, 0, 0)`,
			connID, connID, fmt.Sprintf("%d", 9000+connID), fmt.Sprintf("T-%d", connID))
		e.conns[connID] = true
	}
	srcID := fmt.Sprintf("src-%d", id)
	storetest.Exec(e.t, e.db, `INSERT INTO attachment(id, issue_id, source_id, name, mime, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, connID, srcID, fmt.Sprintf("file-%d", id), mime, createdMs)
	return model.Attachment{
		LocalID:   id,
		ID:        srcID,
		Name:      fmt.Sprintf("file-%d", id),
		Mime:      mime,
		CreatedAt: time.UnixMilli(createdMs).UTC(),
	}
}

// mustPut stores data for an already seeded attachment and fails the test on error.
func (e *env) mustPut(att model.Attachment, connID int64, data []byte) media.Entry {
	e.t.Helper()
	att.Bytes = int64(len(data))
	ent, err := e.c.Put(context.Background(), att, connID, bytes.NewReader(data))
	if err != nil {
		e.t.Fatalf("Put(att %d): %v", att.LocalID, err)
	}
	return ent
}

// seedPut = seed (created_at = 1000+id) + mustPut.
func (e *env) seedPut(connID, id int64, mime string, data []byte) (model.Attachment, media.Entry) {
	e.t.Helper()
	att := e.seed(connID, id, mime, 1000+id)
	return att, e.mustPut(att, connID, data)
}

type row struct {
	FilePath, ThumbPath, FetchError sql.NullString
	Width, Height                   sql.NullInt64
	CachedBytes                     int64
	LastViewed, EvictedAt           sql.NullInt64
}

func (e *env) row(id int64) row {
	e.t.Helper()
	var r row
	err := e.db.W.QueryRowContext(context.Background(),
		`SELECT file_path, thumb_path, fetch_error, width, height, cached_bytes, last_viewed_at, evicted_at FROM attachment WHERE id = ?`, id).
		Scan(&r.FilePath, &r.ThumbPath, &r.FetchError, &r.Width, &r.Height, &r.CachedBytes, &r.LastViewed, &r.EvictedAt)
	if err != nil {
		e.t.Fatalf("read attachment row %d: %v", id, err)
	}
	return r
}

// files lists every regular file under root as slash-separated relative path -> size.
func files(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			out[filepath.ToSlash(rel)] = info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

func origPath(conn, id int64) string  { return fmt.Sprintf("%d/%d/%d.orig", conn, id%256, id) }
func thumbPath(conn, id int64) string { return fmt.Sprintf("%d/%d/%d.thumb.jpg", conn, id%256, id) }

func requireCode(t *testing.T, err error, want apperr.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want apperr code %q", want)
	}
	if got := apperr.CodeOf(err); got != want {
		t.Fatalf("error code = %q (%v), want %q", got, err, want)
	}
}

// openAll opens (id, thumb), reads everything and closes the file before returning (Windows-safe).
func openAll(t *testing.T, c media.Cache, id int64, thumb bool) ([]byte, string) {
	t.Helper()
	f, mime, err := c.Open(id, thumb)
	if err != nil {
		t.Fatalf("Open(%d, thumb=%v): %v", id, thumb, err)
	}
	if f == nil {
		t.Fatalf("Open(%d, thumb=%v): nil file with nil error", id, thumb)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read opened file: %v", err)
	}
	return b, mime
}

// requireNotCached asserts Open fails with not_found and returns no file.
func requireNotCached(t *testing.T, c media.Cache, id int64, thumb bool) {
	t.Helper()
	f, _, err := c.Open(id, thumb)
	if f != nil {
		f.Close()
		t.Fatalf("Open(%d, thumb=%v) returned a file, want not_found", id, thumb)
	}
	requireCode(t, err, apperr.NotFound)
}

// ---- image builders ----

// testImage is a deterministic, poorly compressible picture.
func testImage(w, h int, seed uint32) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	s := seed*2654435761 + 12345
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s = s*1664525 + 1013904223
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 255 / max(w, 1)), G: uint8(y * 255 / max(h, 1)), B: uint8(s >> 24), A: 255})
		}
	}
	return img
}

func pngBytes(t testing.TB, w, h int, seed uint32) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, testImage(w, h, seed)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegBytes(t testing.TB, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, testImage(w, h, 1), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func gifBytes(t testing.TB, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := gif.Encode(&b, testImage(w, h, 1), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func bmpBytes(t testing.TB, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := bmp.Encode(&b, testImage(w, h, 1)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeConfig(t *testing.T, b []byte) (image.Config, string) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("thumbnail is not a decodable image: %v", err)
	}
	return cfg, format
}

// requireThumbFits asserts that the cached thumbnail of id is a JPEG within ThumbW x ThumbH that keeps the aspect
// ratio of srcW x srcH (+-1 px) and, for sources larger than the box, touches the box on one side.
func requireThumbFits(t *testing.T, c media.Cache, id int64, srcW, srcH int) {
	t.Helper()
	b, mime := openAll(t, c, id, true)
	if mime != "image/jpeg" {
		t.Errorf("thumb mime = %q, want image/jpeg", mime)
	}
	cfg, format := decodeConfig(t, b)
	if format != "jpeg" {
		t.Fatalf("thumb format = %q, want jpeg", format)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > media.ThumbW || cfg.Height > media.ThumbH {
		t.Fatalf("thumb is %dx%d, want within %dx%d", cfg.Width, cfg.Height, media.ThumbW, media.ThumbH)
	}
	// aspect: cross-multiplied difference within one pixel on either axis
	if d := cfg.Width*srcH - cfg.Height*srcW; d < -(srcW+srcH) || d > srcW+srcH {
		t.Errorf("thumb %dx%d does not preserve the aspect of %dx%d", cfg.Width, cfg.Height, srcW, srcH)
	}
	if srcW > media.ThumbW || srcH > media.ThumbH {
		if cfg.Width < media.ThumbW-1 && cfg.Height < media.ThumbH-1 {
			t.Errorf("thumb %dx%d of a larger %dx%d source should reach the %dx%d box on one side",
				cfg.Width, cfg.Height, srcW, srcH, media.ThumbW, media.ThumbH)
		}
	}
}

// errReader yields data, then fails.
type errReader struct {
	data []byte
	err  error
}

func (r *errReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
