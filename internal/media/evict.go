package media

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Evict removes originals only, least recently viewed first, until the cached originals are within the cap, and then
// sweeps orphan files (see evictLocked).
func (c *cache) Evict(ctx context.Context) (int64, error) {
	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evictLocked(ctx)
}

type victim struct {
	id    int64
	rel   string
	bytes int64
}

// evictOverCapLocked removes originals only until total cached_bytes is within the cap. The caller holds c.mu.
func (c *cache) evictOverCapLocked(ctx context.Context) (int64, error) {
	limit := c.capLimit()
	var total int64
	if err := c.db.R.QueryRowContext(ctx, `SELECT COALESCE(SUM(cached_bytes), 0) FROM attachment WHERE file_path IS NOT NULL`).Scan(&total); err != nil {
		return 0, dbErr(err)
	}
	if total <= limit {
		return 0, nil
	}

	rows, err := c.db.R.QueryContext(ctx, `SELECT id, file_path, cached_bytes FROM attachment
		WHERE file_path IS NOT NULL
		ORDER BY last_viewed_at IS NOT NULL,
		         CASE WHEN last_viewed_at IS NULL THEN created_at ELSE last_viewed_at END,
		         id`)
	if err != nil {
		return 0, dbErr(err)
	}
	var victims []victim
	for rows.Next() {
		if total <= limit {
			break
		}
		var v victim
		if err := rows.Scan(&v.id, &v.rel, &v.bytes); err != nil {
			_ = rows.Close()
			return 0, dbErr(err)
		}
		victims = append(victims, v)
		total -= v.bytes
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, dbErr(err)
	}
	_ = rows.Close()

	var freed int64
	if len(victims) > 0 {
		now := c.now().UnixMilli()
		err := c.db.WriteTx(ctx, func(tx *sql.Tx) error {
			for _, v := range victims {
				if _, err := tx.ExecContext(ctx, `UPDATE attachment SET file_path = NULL, cached_bytes = 0, evicted_at = ?
					WHERE id = ?`, now, v.id); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
		for _, v := range victims {
			// A failed removal leaves an unreferenced file; the sweep retries it and counts it then.
			if err := c.removeRel(v.rel); err != nil {
				c.log.Warn("media: evicted original not removed", "attachment", v.id, "err", err)
				continue
			}
			freed += v.bytes
		}
	}
	return freed, nil
}

// evictLocked runs cap eviction followed by the orphan sweep. The caller holds c.ioMu and c.mu.
func (c *cache) evictLocked(ctx context.Context) (int64, error) {
	freed, err := c.evictOverCapLocked(ctx)
	if err != nil {
		return freed, err
	}
	swept, err := c.sweepLocked(ctx)
	if err != nil {
		return freed, err
	}
	return freed + swept, nil
}

// sweepLocked removes files under the media dir that no attachment row references through file_path or thumb_path
// (orphans from a crash or a failed commit, and temp files). Files outside the media dir are never visited. It must
// run with mu held. Returns the bytes removed.
func (c *cache) sweepLocked(ctx context.Context) (int64, error) {
	referenced := map[string]bool{}
	rows, err := c.db.R.QueryContext(ctx, `SELECT file_path, thumb_path FROM attachment
		WHERE file_path IS NOT NULL OR thumb_path IS NOT NULL`)
	if err != nil {
		return 0, dbErr(err)
	}
	for rows.Next() {
		var fp, tp sql.NullString
		if err := rows.Scan(&fp, &tp); err != nil {
			_ = rows.Close()
			return 0, dbErr(err)
		}
		for _, s := range []sql.NullString{fp, tp} {
			if s.Valid && s.String != "" {
				referenced[filepath.Clean(filepath.FromSlash(s.String))] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, dbErr(err)
	}
	_ = rows.Close()

	files, err := c.regularFiles()
	if err != nil {
		return 0, internalErr("The image cache could not be read.", err)
	}
	var freed int64
	for _, f := range files {
		if referenced[f.rel] {
			continue
		}
		if err := c.removeRel(f.rel); err != nil {
			c.log.Warn("media: orphan not removed", "path", f.rel, "err", err)
			continue
		}
		freed += f.size
	}
	return freed, nil
}

// removeRel deletes one file under the media dir. A missing file is not an error.
func (c *cache) removeRel(rel string) error {
	p := filepath.Join(c.dir, filepath.FromSlash(rel))
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
