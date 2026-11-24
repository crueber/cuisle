package media

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // decoder registration
	"image/jpeg"
	_ "image/png" // decoder registration
	"io"
	"io/fs"
	"os"
	"path/filepath"

	_ "golang.org/x/image/bmp" // decoder registration (BMP)
	"golang.org/x/image/draw"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
)

// tempFile is a file in the destination directory that is either committed (renamed into place) or discarded. It is
// never left behind: discard removes it unless it was committed.
type tempFile struct {
	f    *os.File
	live bool
}

func newTemp(dir string) (*tempFile, error) {
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return nil, err
	}
	return &tempFile{f: f, live: true}, nil
}

func (t *tempFile) discard() {
	if !t.live {
		return
	}
	t.live = false
	_ = t.f.Close()
	_ = os.Remove(t.f.Name())
}

// commit closes the file and renames it onto final (atomic on the same volume).
func (t *tempFile) commit(final string) error {
	if !t.live {
		return errors.New("media: temp file already committed or discarded")
	}
	t.live = false
	if err := t.f.Close(); err != nil {
		_ = os.Remove(t.f.Name())
		return err
	}
	if err := os.Rename(t.f.Name(), final); err != nil {
		_ = os.Remove(t.f.Name())
		return err
	}
	return nil
}

// copyToTemp streams r into a temp file in dir. A read failure is media_unavailable and leaves nothing behind.
func copyToTemp(dir string, r io.Reader) (*tempFile, int64, error) {
	t, err := newTemp(dir)
	if err != nil {
		return nil, 0, internalErr("The image could not be stored.", err)
	}
	n, err := io.Copy(t.f, r)
	if err != nil {
		t.discard()
		return nil, 0, apperr.Wrap(apperr.MediaUnavailable, "The attachment could not be stored.", err)
	}
	return t, n, nil
}

func isImageMime(mime string) bool {
	for _, m := range ImageMimes {
		if mime == m {
			return true
		}
	}
	return false
}

// decodeConfig reads the header only. It never allocates pixels.
func decodeConfig(f *os.File) (image.Config, bool, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return image.Config{}, false, err
	}
	cfg, _, err := image.DecodeConfig(bufio.NewReader(f))
	if err != nil {
		return image.Config{}, false, nil
	}
	return cfg, true, nil
}

// thumbSize fits w×h inside ThumbW×ThumbH keeping the aspect ratio. Sources that already fit are not upscaled.
func thumbSize(w, h int) (int, int) {
	if w <= ThumbW && h <= ThumbH {
		return w, h
	}
	dw, dh := ThumbW, ThumbH
	if w*ThumbH > h*ThumbW {
		dh = max(1, h*ThumbW/w)
	} else {
		dw = max(1, w*ThumbH/h)
	}
	return dw, dh
}

// makeThumb decodes src (bounded by the header's pixel count) and writes a JPEG thumbnail into a temp file in dir.
// A header or decode failure, or a source over MaxPixels, is not an error: it returns (nil, 0, 0, nil) and the caller
// keeps the original as a file card. Only I/O failures are errors.
func makeThumb(dir string, src *os.File) (*tempFile, int, int, error) {
	cfg, ok, err := decodeConfig(src)
	if err != nil {
		return nil, 0, 0, internalErr("The image could not be read.", err)
	}
	if !ok || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, 0, 0, nil
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, 0, 0, internalErr("The image could not be read.", err)
	}
	img, _, err := image.Decode(bufio.NewReader(src))
	if err != nil {
		return nil, 0, 0, nil
	}
	dw, dh := thumbSize(cfg.Width, cfg.Height)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	// JPEG has no alpha: flatten onto white first.
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)

	t, err := newTemp(dir)
	if err != nil {
		return nil, 0, 0, internalErr("The preview could not be stored.", err)
	}
	if err := jpeg.Encode(t.f, dst, &jpeg.Options{Quality: ThumbQuality}); err != nil {
		t.discard()
		return nil, 0, 0, internalErr("The preview could not be stored.", err)
	}
	return t, cfg.Width, cfg.Height, nil
}

// attachmentDir is <dir>/<connection>/<row id mod 256>, created if needed.
func (c *cache) attachmentDir(connectionID, rowID int64) (string, error) {
	d := filepath.Join(c.dir, fmt.Sprint(connectionID), fmt.Sprint(rowID%256))
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", internalErr("The image cache could not be created.", err)
	}
	return d, nil
}

// Put stores the original and derives the thumbnail (see Cache.Put).
func (c *cache) Put(ctx context.Context, att model.Attachment, connectionID int64, r io.Reader) (Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	exists, err := c.rowExists(ctx, att.LocalID)
	if err != nil {
		return Entry{}, err
	}
	if !exists {
		return Entry{}, apperr.New(apperr.NotFound, "That attachment is no longer on the ticket.")
	}
	dir, err := c.attachmentDir(connectionID, att.LocalID)
	if err != nil {
		return Entry{}, err
	}
	orig, size, err := copyToTemp(dir, r)
	if err != nil {
		return Entry{}, err
	}
	defer orig.discard()

	// Only declared images under the size limit are decoded; everything else is a file card.
	var thumb *tempFile
	var w, h int
	if isImageMime(att.Mime) && size <= MaxImageBytes {
		thumb, w, h, err = makeThumb(dir, orig.f)
		if err != nil {
			return Entry{}, err
		}
		if thumb != nil {
			defer thumb.discard()
		}
	}

	relOrig, relThumb := origRel(connectionID, att.LocalID), thumbRel(connectionID, att.LocalID)
	if err := orig.commit(filepath.Join(c.dir, filepath.FromSlash(relOrig))); err != nil {
		return Entry{}, internalErr("The image could not be stored.", err)
	}
	thumbPath := filepath.Join(c.dir, filepath.FromSlash(relThumb))
	if thumb != nil {
		if err := thumb.commit(thumbPath); err != nil {
			return Entry{}, internalErr("The preview could not be stored.", err)
		}
	} else if err := os.Remove(thumbPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		c.log.Warn("media: stale thumbnail not removed", "attachment", att.LocalID, "err", err)
	}

	hasThumb := thumb != nil
	err = c.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE attachment SET file_path = ?, thumb_path = ?, cached_bytes = ?,
				width = ?, height = ?, fetch_error = NULL WHERE id = ?`,
			relOrig, nullString(hasThumb, relThumb), size, nullInt(hasThumb, w), nullInt(hasThumb, h), att.LocalID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return apperr.New(apperr.NotFound, "That attachment is no longer on the ticket.")
		}
		return nil
	})
	if err != nil {
		return Entry{}, err
	}

	if _, err := c.evictLocked(ctx); err != nil {
		c.log.Warn("media: eviction after put failed", "attachment", att.LocalID, "err", err)
	}

	ent := Entry{AttachmentRowID: att.LocalID, FilePath: relOrig, CachedBytes: size, Mime: att.Mime, Thumb: hasThumb}
	if hasThumb {
		ent.ThumbPath = relThumb
		ent.Width, ent.Height = w, h
	}
	if ent.Mime == "" {
		ent.Mime = "application/octet-stream"
	}
	return ent, nil
}

// PutThumb stores a provider-made thumbnail as a JPEG within the box (see Cache.PutThumb).
func (c *cache) PutThumb(ctx context.Context, att model.Attachment, connectionID int64, r io.Reader) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	exists, err := c.rowExists(ctx, att.LocalID)
	if err != nil {
		return err
	}
	if !exists {
		return apperr.New(apperr.NotFound, "That attachment is no longer on the ticket.")
	}
	dir, err := c.attachmentDir(connectionID, att.LocalID)
	if err != nil {
		return err
	}
	src, _, err := copyToTemp(dir, r)
	if err != nil {
		return err
	}
	defer src.discard()

	thumb, _, _, err := makeThumb(dir, src.f)
	if err != nil {
		return err
	}
	if thumb == nil {
		return apperr.New(apperr.MediaUnavailable, "The preview could not be read.")
	}
	defer thumb.discard()

	relThumb := thumbRel(connectionID, att.LocalID)
	if err := thumb.commit(filepath.Join(c.dir, filepath.FromSlash(relThumb))); err != nil {
		return internalErr("The preview could not be stored.", err)
	}
	return c.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE attachment SET thumb_path = ? WHERE id = ?`, relThumb, att.LocalID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return apperr.New(apperr.NotFound, "That attachment is no longer on the ticket.")
		}
		return nil
	})
}

func nullString(ok bool, s string) sql.NullString {
	return sql.NullString{String: s, Valid: ok}
}

func nullInt(ok bool, v int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(v), Valid: ok}
}
