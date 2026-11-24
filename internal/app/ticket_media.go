package app

// Attachments of the ticket modal (WP-T9): EnsureMedia and OpenAttachment (the explicit network triggers), the
// background thumbnails after a Refresh, the sync.MediaStore adapter over media.Cache, the /media asset routes (cache
// only, never a fetch), and the Display › Storage methods of AppService.

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/media"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source"
	syncpkg "github.com/crueber/cuisle/internal/sync"
	"github.com/crueber/cuisle/internal/view"
)

// Settings keys of this feature (architecture.md §6.4). Validated in internal/settings.
const (
	keyMediaCacheMB = "media.cache_max_mb"
	defaultCacheMB  = 1024
)

// maxOpenBytes bounds a file fetched for OpenAttachment.
const maxOpenBytes = 1 << 30

// ── sync.MediaStore over media.Cache ─────────────────────────────────────────────

type mediaStore struct{ m media.Cache }

// NewMediaStore adapts the media cache to the interface the sync media phase needs (sync may not import media).
func NewMediaStore(m media.Cache) syncpkg.MediaStore {
	if m == nil {
		return nil
	}
	return mediaStore{m}
}

func (s mediaStore) Put(ctx context.Context, att model.Attachment, connectionID int64, r io.Reader) (string, error) {
	ent, err := s.m.Put(ctx, att, connectionID, r)
	if err != nil {
		return "none", err
	}
	if ent.Thumb {
		return "full", nil
	}
	return "none", nil // kept as a file: the media phase asks for the provider thumbnail
}

func (s mediaStore) PutThumb(ctx context.Context, att model.Attachment, connectionID int64, r io.Reader) error {
	return s.m.PutThumb(ctx, att, connectionID, r)
}

func (s mediaStore) Evict(ctx context.Context) (int64, error) { return s.m.Evict(ctx) }

// MediaCap returns the cache cap in bytes, read from app setting media.cache_max_mb at every call so a change applies
// without a restart (default 1024 MiB).
func MediaCap(st *settings.Store) func() int64 {
	return func() int64 {
		mb := int64(defaultCacheMB)
		if st != nil {
			if all, err := st.AppSettings(context.Background()); err == nil {
				if n, err := strconv.ParseInt(all[keyMediaCacheMB], 10, 64); err == nil && n > 0 {
					mb = n
				}
			}
		}
		return mb << 20
	}
}

// ── AppService: Display › Storage ────────────────────────────────────────────────

func (d *Deps) capMB() int {
	if all, err := d.Settings.AppSettings(context.Background()); err == nil {
		if n, err := strconv.Atoi(all[keyMediaCacheMB]); err == nil && n > 0 {
			return n
		}
	}
	return defaultCacheMB
}

func (d *Deps) mediaInfo(ctx context.Context) (MediaCacheInfo, error) {
	info := MediaCacheInfo{CapMB: d.capMB()}
	if d.Media == nil {
		return info, nil
	}
	st, err := d.Media.Stats(ctx)
	if err != nil {
		return MediaCacheInfo{}, err
	}
	info.Bytes, info.ThumbBytes, info.Files = st.Bytes, st.ThumbBytes, st.Files
	return info, nil
}

// MediaCache reports the image cache size; the cap is app setting media.cache_max_mb.
func (s *AppService) MediaCache(ctx context.Context) (out MediaCacheInfo, err error) {
	defer s.d.guard("MediaCache", &err)
	return s.d.mediaInfo(ctx)
}

// ClearMediaCache removes originals and thumbnails ("Clear image cache"). Images download again on demand.
func (s *AppService) ClearMediaCache(ctx context.Context) (out MediaCacheInfo, err error) {
	defer s.d.guard("ClearMediaCache", &err)
	if s.d.Media != nil {
		if err := s.d.Media.Clear(ctx); err != nil {
			return MediaCacheInfo{}, err
		}
	}
	return s.d.mediaInfo(ctx)
}

// ── attachment rows ──────────────────────────────────────────────────────────────

type attachmentRow struct {
	IssueID      int64
	ConnectionID int64
	IssueSource  string
	Att          model.Attachment
	IsImage      bool
}

func (d *Deps) attachmentRow(ctx context.Context, id int64) (attachmentRow, error) {
	var r attachmentRow
	var created int64
	var img int
	err := d.DB.R.QueryRowContext(ctx, `
		SELECT a.issue_id, i.connection_id, i.source_id, a.source_id, COALESCE(a.media_file_id, ''), a.name, a.mime, a.bytes,
		       COALESCE(a.author_account_id, ''), a.created_at, a.is_image, COALESCE(a.width, 0), COALESCE(a.height, 0)
		  FROM attachment a JOIN issue i ON i.id = a.issue_id
		 WHERE a.id = ? AND a.removed_at IS NULL`, id).Scan(
		&r.IssueID, &r.ConnectionID, &r.IssueSource, &r.Att.ID, &r.Att.MediaFileID, &r.Att.Name, &r.Att.Mime, &r.Att.Bytes,
		&r.Att.AuthorID, &created, &img, &r.Att.Width, &r.Att.Height)
	if err == sql.ErrNoRows {
		return attachmentRow{}, apperr.New(apperr.NotFound, "That attachment is no longer on the ticket.")
	}
	if err != nil {
		return attachmentRow{}, apperr.Wrap(apperr.DB, "The attachment could not be read.", err)
	}
	r.Att.LocalID, r.Att.CreatedAt, r.IsImage = id, time.UnixMilli(created), img != 0
	return r, nil
}

// cachedOriginal opens the cached original; ok=false on a miss.
func (d *Deps) cachedOriginal(id int64) (*os.File, bool) {
	if d.Media == nil {
		return nil, false
	}
	f, _, err := d.Media.Open(id, false)
	if err != nil {
		return nil, false
	}
	return f, true
}

// fetchOriginal downloads the original into the media cache (network). It records the media id the redirect taught.
func (d *Deps) fetchOriginal(ctx context.Context, ar attachmentRow) error {
	if d.Media == nil {
		return apperr.New(apperr.MediaUnavailable, "Images are not stored in this build.")
	}
	row, ok, err := d.connRow(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.New(apperr.AuthMissing, "Connect to Jira to load images.")
	}
	if err := authError(row.AuthState); err != nil {
		return err
	}
	src, err := d.openSource(row, false)
	if err != nil {
		return err
	}
	blob, err := src.FetchMedia(source.WithInteractive(ctx), ar.Att, false)
	if err != nil {
		d.noteNet(err)
		d.noteSourceError(ctx, row, err)
		return apperr.From(err)
	}
	d.noteNet(nil)
	defer blob.Body.Close()
	if blob.MediaFileID != "" {
		_ = cache.SetAttachmentMediaID(context.WithoutCancel(ctx), d.DB.W, ar.Att.LocalID, blob.MediaFileID)
	}
	if _, err := d.Media.Put(ctx, ar.Att, ar.ConnectionID, blob.Body); err != nil {
		return apperr.From(err)
	}
	return nil
}

// EnsureMedia is a NETWORK call on a cache miss (lightbox / "load image"); touches the LRU.
func (s *TicketService) EnsureMedia(ctx context.Context, attachmentID int64) (out view.AttachmentView, err error) {
	defer s.d.guard("EnsureMedia", &err)
	d := s.d
	ar, err := d.attachmentRow(ctx, attachmentID)
	if err != nil {
		return view.AttachmentView{}, err
	}
	if !ar.IsImage {
		return view.AttachmentView{}, apperr.Validationf("attachmentId", "That attachment is not an image.")
	}
	if f, ok := d.cachedOriginal(attachmentID); ok {
		_ = f.Close()
	} else {
		if err := d.fetchOriginal(ctx, ar); err != nil {
			return view.AttachmentView{}, err
		}
		d.emit(syncpkg.EvMediaReady, syncpkg.MediaReady{AttachmentID: attachmentID, IssueID: ar.IssueID, Cached: "full"})
	}
	d.Media.Touch(attachmentID)
	return d.attachmentView(ctx, ar)
}

// attachmentView is the tile of an attachment with its current cache state.
func (d *Deps) attachmentView(ctx context.Context, ar attachmentRow) (view.AttachmentView, error) {
	tk, err := cache.LoadTicket(ctx, d.DB.R, ar.IssueID)
	if err != nil {
		return view.AttachmentView{}, err
	}
	for _, a := range tk.Attachments {
		if a.LocalID != ar.Att.LocalID {
			continue
		}
		who := ""
		if p, ok := tk.People[a.AuthorID]; ok {
			who = p.DisplayName
		}
		cached := "none"
		switch {
		case a.HasFile:
			cached = "full"
		case a.HasThumb:
			cached = "thumb"
		}
		id := strconv.FormatInt(a.LocalID, 10)
		return view.AttachmentView{
			ID: a.LocalID, Name: a.Name, Ext: strings.ToUpper(strings.TrimPrefix(path.Ext(a.Name), ".")), Mime: a.Mime, Bytes: a.Bytes,
			AuthorInitials: view.Initials(who), AuthorName: who, CreatedAt: a.CreatedAt.UnixMilli(), Image: a.IsImage, Width: a.Width,
			Height: a.Height, Cached: cached, URL: "/media/" + id, ThumbURL: "/media/" + id + "/thumb",
		}, nil
	}
	return view.AttachmentView{}, apperr.New(apperr.NotFound, "That attachment is no longer on the ticket.")
}

// openableExt is the allowlist of file types OpenAttachment hands to the OS: documents, plain text, images, audio, video
// and archives that a viewer shows rather than runs. Everything else (programs, scripts, shortcuts, HTML, SVG, installers,
// anything unknown or without an extension) is refused: opening it could run it.
var openableExt = map[string]bool{
	".pdf": true, ".txt": true, ".md": true, ".csv": true, ".log": true, ".json": true, ".png": true, ".jpg": true,
	".jpeg": true, ".gif": true, ".webp": true, ".bmp": true, ".heic": true, ".mp4": true, ".mov": true, ".mp3": true,
	".wav": true, ".docx": true, ".xlsx": true, ".pptx": true, ".odt": true, ".ods": true, ".odp": true, ".rtf": true,
	".zip": true,
}

// maxOpenNameBytes bounds an attachment file name; longer names are refused (a name the file system cannot hold must not
// surface as an internal error).
const maxOpenNameBytes = 200

// windowsDevice matches the names Windows treats as devices whatever their extension (CON.txt is the console).
var windowsDevice = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)

func errNotOpenable() error {
	return apperr.Validationf("attachmentId", "For safety Cuisle won't open this type of file. Open it in Jira instead.")
}

// openName turns an attachment name into the file name used in the temp folder, or refuses it. Path separators, the
// characters Windows reserves, control characters and the bidirectional overrides that spoof an extension are removed
// before the extension is checked against the allowlist, so what is checked is what is written. A Windows device name
// (CON, NUL, COM1, ... with any extension) is prefixed with an underscore.
func openName(name string) (string, error) {
	name = strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_").Replace(name)
	name = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			return -1
		case r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0xfeff:
			return -1
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" || len(name) > maxOpenNameBytes {
		return "", errNotOpenable()
	}
	ext := strings.ToLower(path.Ext(name))
	if !openableExt[ext] {
		return "", errNotOpenable()
	}
	if windowsDevice.MatchString(strings.SplitN(name, ".", 2)[0]) {
		name = "_" + name
	}
	return name, nil
}

// sweepTempAttachments removes folders OpenAttachment left in the OS temp dir more than a day ago.
func sweepTempAttachments(now time.Time) {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "cuisle-attachment-") {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > 24*time.Hour {
			_ = os.RemoveAll(filepath.Join(os.TempDir(), e.Name()))
		}
	}
}

// OpenAttachment is a NETWORK call on a miss; writes to the OS temp dir and opens it with the default app.
func (s *TicketService) OpenAttachment(ctx context.Context, attachmentID int64) (err error) {
	defer s.d.guard("OpenAttachment", &err)
	d := s.d
	ar, err := d.attachmentRow(ctx, attachmentID)
	if err != nil {
		return err
	}
	name, err := openName(ar.Att.Name)
	if err != nil {
		return err
	}
	if d.OpenFile == nil {
		return apperr.New(apperr.Unsupported, "Opening files is not available here.")
	}
	var body io.Reader
	if f, ok := d.cachedOriginal(attachmentID); ok {
		defer f.Close()
		d.Media.Touch(attachmentID)
		body = f
	} else if ar.IsImage {
		if err := d.fetchOriginal(ctx, ar); err != nil { // images go through the cache so they are kept
			return err
		}
		f, ok := d.cachedOriginal(attachmentID)
		if !ok {
			return apperr.New(apperr.MediaUnavailable, "The attachment could not be stored.")
		}
		defer f.Close()
		d.emit(syncpkg.EvMediaReady, syncpkg.MediaReady{AttachmentID: attachmentID, IssueID: ar.IssueID, Cached: "full"})
		body = f
	} else {
		row, ok, err := d.connRow(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return apperr.New(apperr.AuthMissing, "Connect to Jira to open files.")
		}
		if err := authError(row.AuthState); err != nil {
			return err
		}
		src, err := d.openSource(row, false)
		if err != nil {
			return err
		}
		blob, err := src.FetchMedia(source.WithInteractive(ctx), ar.Att, false)
		if err != nil {
			d.noteNet(err)
			d.noteSourceError(ctx, row, err)
			return apperr.From(err)
		}
		d.noteNet(nil)
		defer blob.Body.Close()
		body = blob.Body
	}

	sweepTempAttachments(d.now())
	dir, err := os.MkdirTemp("", "cuisle-attachment-*")
	if err != nil {
		return apperr.Wrap(apperr.Internal, "The file could not be saved.", err)
	}
	target := filepath.Join(dir, name)
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return apperr.Wrap(apperr.Internal, "The file could not be saved.", err)
	}
	n, err := io.Copy(out, io.LimitReader(body, maxOpenBytes+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxOpenBytes {
		err = apperr.New(apperr.MediaUnavailable, "That file is too large to open from here.")
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return apperr.From(err)
	}
	if err := d.OpenFile(target); err != nil {
		return apperr.Wrap(apperr.Internal, "Could not open the file.", err)
	}
	return nil
}

// ── background thumbnails after a Refresh ────────────────────────────────────────

func (d *Deps) background() context.Context {
	d.ticketMu.Lock()
	defer d.ticketMu.Unlock()
	if d.bgCtx == nil {
		d.bgCtx, d.bgCancel = context.WithCancel(context.Background())
	}
	return d.bgCtx
}

// fetchThumbsAsync downloads the thumbnails of the issue's new image attachments (the media lane of the limiter) after
// a Refresh, and emits media:ready for each. It never blocks the Refresh and never reports failure.
func (d *Deps) fetchThumbsAsync(src source.Source, row cache.ConnRow, ref issueRef) {
	if d.Media == nil || !src.Capabilities().Content {
		return
	}
	ctx := d.background()
	d.bg.Add(1)
	go func() {
		defer d.bg.Done()
		defer func() {
			if r := recover(); r != nil {
				d.log().Error("thumbnail fetch panicked", "panic", r)
			}
		}()
		targets, err := cache.MediaTargets(ctx, d.DB.R, row.ID, []string{ref.SourceID})
		if err != nil {
			return
		}
		for _, t := range targets {
			if ctx.Err() != nil {
				return
			}
			blob, err := src.FetchMedia(ctx, t.Att, false)
			if err != nil {
				_ = cache.SetAttachmentFetchError(context.WithoutCancel(ctx), d.DB.W, t.Att.LocalID, string(apperr.From(err).Code))
				continue
			}
			ent, perr := d.Media.Put(ctx, t.Att, row.ID, blob.Body)
			_ = blob.Body.Close()
			if blob.MediaFileID != "" {
				_ = cache.SetAttachmentMediaID(context.WithoutCancel(ctx), d.DB.W, t.Att.LocalID, blob.MediaFileID)
			}
			cached := "full"
			if perr != nil || !ent.Thumb {
				cached = d.thumbFallback(ctx, src, row, t)
				if cached == "" {
					continue
				}
			}
			d.emit(syncpkg.EvMediaReady, syncpkg.MediaReady{AttachmentID: t.Att.LocalID, IssueID: t.IssueID, Cached: cached})
		}
	}()
}

func (d *Deps) thumbFallback(ctx context.Context, src source.Source, row cache.ConnRow, t cache.MediaTarget) string {
	blob, err := src.FetchMedia(ctx, t.Att, true)
	if err != nil {
		return ""
	}
	defer blob.Body.Close()
	if err := d.Media.PutThumb(ctx, t.Att, row.ID, blob.Body); err != nil {
		return ""
	}
	return "thumb"
}

// WaitBackground blocks until background work (thumbnails after a Refresh) has finished. Tests use it.
func (s *Services) WaitBackground() { s.App.d.bg.Wait() }

// Shutdown cancels background work and waits for it, up to ctx.
func (s *Services) Shutdown(ctx context.Context) error {
	d := s.App.d
	d.ticketMu.Lock()
	cancel := d.bgCancel
	d.ticketMu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() { d.bg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ── /media routes ────────────────────────────────────────────────────────────────

// MediaMiddleware serves GET /media/{attachmentRowId} and /media/{attachmentRowId}/thumb from the cache only: the stored
// mime type, nosniff, a private immutable Cache-Control, and 404 on a miss. It never fetches (architecture.md §2.4).
func MediaMiddleware(s *Services) application.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rest, ok := strings.CutPrefix(r.URL.Path, "/media/")
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			s.serveMedia(w, r, rest)
		})
	}
}

// AssetMiddleware is what cmd/cuisle installs: /boot.js and /media/*, everything else passes through.
func AssetMiddleware(s *Services) application.Middleware {
	boot, med := BootMiddleware(s), MediaMiddleware(s)
	return func(next http.Handler) http.Handler { return boot(med(next)) }
}

func (s *Services) serveMedia(w http.ResponseWriter, r *http.Request, rest string) {
	d := s.App.d
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	thumb := false
	if idPart, ok := strings.CutSuffix(rest, "/thumb"); ok {
		rest, thumb = idPart, true
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != rest || d.Media == nil {
		http.NotFound(w, r)
		return
	}
	f, mime, err := d.Media.Open(id, thumb)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ok := false
	for _, m := range media.ImageMimes {
		ok = ok || m == mime
	}
	if !ok {
		mime = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", mime)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if !thumb {
		d.Media.Touch(id)
	}
	http.ServeContent(w, r, "", info.ModTime(), f)
}
