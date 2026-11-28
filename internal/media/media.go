package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/store"
)

// cache is the on-disk Cache. ioMu prevents an orphan sweep or Clear from racing temp files written by concurrent Puts
// (Put takes RLock for temp-file I/O and thumbnail scaling; Evict/Clear take Lock). mu serialises commit + DB/evict.
type cache struct {
	dir      string
	db       *store.DB
	capBytes func() int64
	now      func() time.Time
	log      *slog.Logger
	ioMu     sync.RWMutex
	mu       sync.Mutex
}

// New returns the cache rooted at dir (<data dir>/media, created on first Put) over db's attachment rows.
// capBytes is called at every Put/Evict/Stats so a changed app setting media.cache_max_mb applies without a restart
// (the app passes a func reading the setting × 1 MiB).
func New(dir string, db *store.DB, capBytes func() int64, opts Options) Cache {
	c := &cache{dir: dir, db: db, capBytes: capBytes, now: opts.Now, log: opts.Logger}
	if c.now == nil {
		c.now = time.Now
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	return c
}

func (c *cache) capLimit() int64 {
	if c.capBytes == nil {
		return math.MaxInt64
	}
	return c.capBytes()
}

// origRel and thumbRel are the slash-separated paths relative to the media dir (data-model.md §2.5a).
func origRel(connectionID, rowID int64) string {
	return fmt.Sprintf("%d/%d/%d.orig", connectionID, rowID%256, rowID)
}

func thumbRel(connectionID, rowID int64) string {
	return fmt.Sprintf("%d/%d/%d.thumb.jpg", connectionID, rowID%256, rowID)
}

// absPath maps a stored relative path to a path under dir. It refuses anything that would leave dir.
func (c *cache) absPath(rel string) (string, bool) {
	local := filepath.FromSlash(rel)
	if !filepath.IsLocal(local) {
		return "", false
	}
	return filepath.Join(c.dir, local), true
}

func notCached() *apperr.Error {
	return apperr.New(apperr.NotFound, "That image is not cached.")
}

func dbErr(err error) *apperr.Error {
	return apperr.Wrap(apperr.DB, "The data could not be read.", err)
}

func internalErr(msg string, err error) *apperr.Error {
	return apperr.Wrap(apperr.Internal, msg, err)
}

// rowExists reports whether the attachment row id is present.
func (c *cache) rowExists(ctx context.Context, id int64) (bool, error) {
	var one int
	err := c.db.R.QueryRowContext(ctx, `SELECT 1 FROM attachment WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, dbErr(err)
	}
	return true, nil
}

// Open opens a cached file for reading; not_found when absent. Never fetches.
func (c *cache) Open(attachmentRowID int64, thumb bool) (*os.File, string, error) {
	var filePath, thumbPath sql.NullString
	var mime string
	err := c.db.R.QueryRowContext(context.Background(),
		`SELECT file_path, thumb_path, mime FROM attachment WHERE id = ?`, attachmentRowID).
		Scan(&filePath, &thumbPath, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", notCached()
	}
	if err != nil {
		return nil, "", dbErr(err)
	}
	rel := filePath
	if thumb {
		rel = thumbPath
		mime = "image/jpeg"
	}
	if !rel.Valid || rel.String == "" {
		return nil, "", notCached()
	}
	p, ok := c.absPath(rel.String)
	if !ok {
		return nil, "", notCached()
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", notCached()
	}
	if err != nil {
		return nil, "", internalErr("The image could not be opened.", err)
	}
	return f, mime, nil
}

// Touch sets last_viewed_at = now (LRU key). Best effort; errors are logged.
func (c *cache) Touch(attachmentRowID int64) {
	_, err := c.db.W.ExecContext(context.Background(),
		`UPDATE attachment SET last_viewed_at = ? WHERE id = ?`, store.Ms(c.now()), attachmentRowID)
	if err != nil {
		c.log.Warn("media touch failed", "attachment", attachmentRowID, "err", err)
	}
}

// Clear removes every original and thumbnail, nulls the path columns and zeroes cached_bytes. Rows are kept.
func (c *cache) Clear(ctx context.Context) error {
	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE attachment SET file_path = NULL, thumb_path = NULL, cached_bytes = 0
			WHERE file_path IS NOT NULL OR thumb_path IS NOT NULL OR cached_bytes <> 0`)
		return err
	})
	if err != nil {
		return err
	}
	if err := os.RemoveAll(c.dir); err != nil {
		return internalErr("The image cache could not be cleared.", err)
	}
	return nil
}

// Stats reports the cache size. Bytes comes from the rows (what the cap counts); Files and ThumbBytes from disk.
func (c *cache) Stats(ctx context.Context) (Stats, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Stats{CapBytes: c.capLimit()}
	if err := c.db.R.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cached_bytes), 0) FROM attachment WHERE file_path IS NOT NULL`).Scan(&st.Bytes); err != nil {
		return Stats{}, dbErr(err)
	}
	files, err := c.regularFiles()
	if err != nil {
		return Stats{}, internalErr("The image cache could not be read.", err)
	}
	for _, f := range files {
		switch {
		case strings.HasSuffix(f.rel, ".orig"):
			st.Files++
		case strings.HasSuffix(f.rel, ".thumb.jpg"):
			st.Files++
			st.ThumbBytes += f.size
		}
	}
	return st, nil
}

type diskFile struct {
	rel  string // relative to the media dir, OS separators, cleaned
	size int64
}

// regularFiles lists the regular files under the media dir. Symlinks and directories are not returned, so nothing
// outside the media dir is ever reached. A missing media dir is an empty cache.
func (c *cache) regularFiles() ([]diskFile, error) {
	var out []diskFile
	err := filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(c.dir, p)
		if err != nil {
			return err
		}
		out = append(out, diskFile{rel: filepath.Clean(rel), size: info.Size()})
		return nil
	})
	return out, err
}
