// Package media is the attachment cache on disk (architecture.md §2.4, data-model.md §2.5a attachment, ADR 0015):
// originals and thumbnails under <data dir>/media/<connection_id>/<row id mod 256>/<row id>{.orig,.thumb.jpg},
// thumbnails (320×200 max, JPEG q80, golang.org/x/image) always kept, originals evicted least-recently-viewed past the
// cap. It never fetches: bytes are handed to it (sync's media phase, TicketService.EnsureMedia/OpenAttachment).
//
// FROZEN CONTRACT: the types in this file and the exported signatures in media.go. Implementation: WP-T3.
// Imports allowed: model, store, paths, apperr, stdlib, golang.org/x/image.
package media

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	_ "golang.org/x/image/webp" // WebP decoder; also pins golang.org/x/image (WP-T3 adds x/image/bmp and x/image/draw)

	"github.com/crueber/cuisle/internal/model"
)

// Limits (architecture.md §2.4).
const (
	MaxImageBytes = 25 << 20   // larger images are file cards (provider thumbnail fallback)
	MaxPixels     = 60_000_000 // decode bound against decompression bombs
	ThumbW        = 320        // 2× the 160×100 tile
	ThumbH        = 200
	ThumbQuality  = 80
)

// ImageMimes are the mime types rendered inline; everything else (SVG, HEIC, PDF, video, …) is a file card.
var ImageMimes = []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp"}

// Cache stores attachment bytes. Every method is safe for concurrent use. Row ids are attachment.id (model
// Attachment.LocalID); the attachment row must exist before Put. Errors are *apperr.Error (not_found, db,
// media_unavailable).
type Cache interface {
	// Put stores the original and derives the thumbnail; updates the row's file_path, thumb_path, cached_bytes, width,
	// height and clears fetch_error, then evicts if over the cap. Undecodable images keep the original as a file
	// (Entry.Thumb=false).
	Put(ctx context.Context, att model.Attachment, connectionID int64, r io.Reader) (Entry, error)
	// PutThumb stores a provider-made thumbnail (fallback for originals over MaxImageBytes or undecodable).
	PutThumb(ctx context.Context, att model.Attachment, connectionID int64, r io.Reader) error
	// Open opens a cached file for reading; not_found when absent. Never fetches.
	Open(attachmentRowID int64, thumb bool) (f *os.File, mime string, err error)
	// Touch sets last_viewed_at = now (LRU key). Best effort; errors are logged.
	Touch(attachmentRowID int64)
	// Evict removes originals only, least recently viewed first (never-viewed first by oldest created_at), until the
	// cached originals are within the cap. Thumbnails are never evicted.
	Evict(ctx context.Context) (freedBytes int64, err error)
	// Clear is "Clear image cache": removes originals and thumbnails and nulls the path columns.
	Clear(ctx context.Context) error
	// Stats reports the cache size.
	Stats(ctx context.Context) (Stats, error)
}

// Entry is what Put stored.
type Entry struct {
	AttachmentRowID int64
	FilePath        string // relative to the media dir; "" if the original was not kept
	ThumbPath       string // relative to the media dir; "" if no thumbnail could be made
	CachedBytes     int64  // bytes of the original counted against the cap
	Mime            string // sniffed/stored mime
	Width, Height   int    // decoded size; 0 if not decodable
	Thumb           bool   // a thumbnail exists
}

// Stats is the cache size (AppService.MediaCache maps it to MediaCacheInfo).
type Stats struct {
	Bytes      int64 // cached originals (count against the cap)
	ThumbBytes int64 // thumbnails (not counted against the cap)
	Files      int   // originals + thumbnails on disk
	CapBytes   int64 // current cap
}

// Options are optional dependencies of New.
type Options struct {
	Now    func() time.Time // default time.Now
	Logger *slog.Logger     // default slog.Default()
}
