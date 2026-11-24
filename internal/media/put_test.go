package media_test

// Put, PutThumb and Open (architecture.md §2.4, data-model.md §2.5a attachment, contract.go).
//
// SPEC-NOTE: a non-image mime (svg, heic, pdf, video, octet-stream), an image over MaxImageBytes, an image over
// MaxPixels and an undecodable image are NOT rejected. Put returns a nil error, keeps the original on disk as a file
// ("file card": architecture §2.4 "everything else … is a file card"; contract.go Put "Undecodable images keep the
// original as a file (Entry.Thumb=false)"), sets FilePath/CachedBytes, leaves ThumbPath "" and Width/Height 0, and
// leaves the row's thumb_path NULL. No decode is attempted for them. Provider thumbnails for those come through
// PutThumb.
//
// SPEC-NOTE: MaxImageBytes (25 MiB) and MaxPixels (60,000,000) are inclusive limits: exactly 25 MiB and exactly 60 MP
// are still decoded; one byte or one pixel more is not.
//
// SPEC-NOTE: the declared mime governs whether a decode is attempted; tests keep the declared mime and the bytes
// consistent so the sniffed-versus-declared question never arises. Entry.Mime equals the declared mime in those
// cases.
//
// SPEC-NOTE: PutThumb always normalises the provider thumbnail to a JPEG within 320x200 stored as .thumb.jpg (the
// file name and the Open(…, thumb=true) mime image/jpeg promise a JPEG), whatever format it arrives in. A provider
// thumbnail that cannot be decoded is an apperr media_unavailable error and the row is left unchanged.
//
// SPEC-NOTE: Put for an attachment row that does not exist is apperr not_found ("the attachment row must exist before
// Put") and leaves no file behind. A reader that fails part-way is apperr media_unavailable, leaves no file (no
// partial or temp file) and leaves the row unchanged.
//
// SPEC-NOTE: testing.md says a 100 MP image is "rejected without allocating it" while architecture §2.4 says decode
// "failures → file card". Read together: the decode is refused (no pixel buffer is allocated, no thumbnail) and the
// attachment becomes a file card; Put does not return an error. The size check reads the header only
// (image.DecodeConfig), which the fake-header fixture (30000x30000 with no pixel data) also exercises.
//
// SPEC-NOTE: jira-sync §2.14 "If the cache is at its cap the original is still fetched once to make the thumbnail,
// then evicted by LRU": with a cap smaller than the original, Put still makes the thumbnail and the original is
// evicted straight away (file_path NULL, cached_bytes 0, evicted_at set). Entry.FilePath/CachedBytes are not
// asserted for that case (the returned Entry may describe the pre-eviction or post-eviction state).
//
// SPEC-NOTE: Open is judged by the row's file_path; if that column is tampered to point outside the media dir, Open
// must return not_found (or, if the implementation re-derives the path from the ids, the genuine original) and never
// the outside file.
//
// SPEC-NOTE: Put and Touch are synchronous: the row is read immediately after the call returns. Open returns
// not_found when the row says "cached" but the file is gone. File counts are exact: the media dir holds only
// .orig/.thumb.jpg files (no lock, index or leftover temp files) after Put, failed Put and Clear.
//
// SPEC-NOTE: testing.md also lists "orphan files are swept at startup". contract.go gives no API for it (New is the
// only startup hook and its doc says nothing), so it is NOT tested here; the orchestrator should decide where the
// sweep lives and add a test with that WP.
//
// SPEC-NOTE: the spec does not say whether sources smaller than the box are upscaled, so for them only the bounds and
// the aspect ratio are asserted. A just-Put original is never the eviction victim in these tests.

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/media"
	"github.com/crueber/cuisle/internal/model"
)

func TestPut_StoresOriginalAtDocumentedPath(t *testing.T) {
	cases := []struct {
		name     string
		conn, id int64
	}{
		{"small id", 1, 5},
		{"id past 255 wraps the bucket", 1, 300},
		{"same id, other connection", 2, 300},
		{"id exactly 256", 7, 256},
		{"id 255", 3, 255},
		{"large id", 4, 1_000_003},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			data := pngBytes(t, 640, 400, 1)
			_, ent := e.seedPut(tc.conn, tc.id, "image/png", data)

			wantOrig, wantThumb := origPath(tc.conn, tc.id), thumbPath(tc.conn, tc.id)
			if got := filepath.ToSlash(ent.FilePath); got != wantOrig {
				t.Errorf("Entry.FilePath = %q, want %q", got, wantOrig)
			}
			if got := filepath.ToSlash(ent.ThumbPath); got != wantThumb {
				t.Errorf("Entry.ThumbPath = %q, want %q", got, wantThumb)
			}
			if ent.AttachmentRowID != tc.id {
				t.Errorf("Entry.AttachmentRowID = %d, want %d", ent.AttachmentRowID, tc.id)
			}
			onDisk, err := os.ReadFile(filepath.Join(e.dir, filepath.FromSlash(wantOrig)))
			if err != nil {
				t.Fatalf("original is not at the documented path: %v", err)
			}
			if !bytes.Equal(onDisk, data) {
				t.Errorf("stored original differs from the input (%d vs %d bytes)", len(onDisk), len(data))
			}
			if _, err := os.Stat(filepath.Join(e.dir, filepath.FromSlash(wantThumb))); err != nil {
				t.Errorf("thumbnail is not at the documented path: %v", err)
			}
			if got := files(t, e.dir); len(got) != 2 {
				t.Errorf("files on disk = %v, want exactly the original and the thumbnail", got)
			}
		})
	}
}

func TestPut_UpdatesAttachmentRowCacheState(t *testing.T) {
	e := newEnv(t)
	att := e.seed(1, 300, "image/png", 5000)
	if _, err := e.db.W.ExecContext(context.Background(), `UPDATE attachment SET fetch_error = 'timeout' WHERE id = 300`); err != nil {
		t.Fatal(err)
	}
	if r := e.row(300); !r.FetchError.Valid || r.FilePath.Valid {
		t.Fatalf("precondition: %+v", r)
	}

	data := pngBytes(t, 800, 500, 2)
	ent := e.mustPut(att, 1, data)

	r := e.row(300)
	if !r.FilePath.Valid || filepath.ToSlash(r.FilePath.String) != origPath(1, 300) {
		t.Errorf("file_path = %+v, want %q", r.FilePath, origPath(1, 300))
	}
	if !r.ThumbPath.Valid || filepath.ToSlash(r.ThumbPath.String) != thumbPath(1, 300) {
		t.Errorf("thumb_path = %+v, want %q", r.ThumbPath, thumbPath(1, 300))
	}
	if r.CachedBytes != int64(len(data)) {
		t.Errorf("cached_bytes = %d, want %d", r.CachedBytes, len(data))
	}
	if !r.Width.Valid || r.Width.Int64 != 800 || !r.Height.Valid || r.Height.Int64 != 500 {
		t.Errorf("width/height = %+v/%+v, want 800/500", r.Width, r.Height)
	}
	if r.FetchError.Valid {
		t.Errorf("fetch_error = %q, want NULL after a successful Put", r.FetchError.String)
	}
	if ent.CachedBytes != int64(len(data)) || ent.Width != 800 || ent.Height != 500 || !ent.Thumb || ent.Mime != "image/png" {
		t.Errorf("Entry = %+v, want CachedBytes %d, 800x500, Thumb, image/png", ent, len(data))
	}
}

func TestPut_ThumbnailIsJPEGWithinBoundsPreservingAspect(t *testing.T) {
	cases := []struct {
		name string
		mime string
		w, h int
		data func(t *testing.T) []byte
	}{
		{"png wide 800x400", "image/png", 800, 400, func(t *testing.T) []byte { return pngBytes(t, 800, 400, 1) }},
		{"png tall 400x800", "image/png", 400, 800, func(t *testing.T) []byte { return pngBytes(t, 400, 800, 1) }},
		{"png very wide 1280x200", "image/png", 1280, 200, func(t *testing.T) []byte { return pngBytes(t, 1280, 200, 1) }},
		{"png square 1000x1000", "image/png", 1000, 1000, func(t *testing.T) []byte { return pngBytes(t, 1000, 1000, 1) }},
		{"png exactly the box 320x200", "image/png", 320, 200, func(t *testing.T) []byte { return pngBytes(t, 320, 200, 1) }},
		{"png smaller than the box 100x50", "image/png", 100, 50, func(t *testing.T) []byte { return pngBytes(t, 100, 50, 1) }},
		{"jpeg 1024x768", "image/jpeg", 1024, 768, func(t *testing.T) []byte { return jpegBytes(t, 1024, 768) }},
		{"gif 500x300", "image/gif", 500, 300, func(t *testing.T) []byte { return gifBytes(t, 500, 300) }},
		{"bmp 640x480", "image/bmp", 640, 480, func(t *testing.T) []byte { return bmpBytes(t, 640, 480) }},
		{"webp 640x400 (fixture)", "image/webp", 640, 400, func(t *testing.T) []byte { return fixture(t, "gradient-640x400.webp") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			_, ent := e.seedPut(1, 42, tc.mime, tc.data(t))
			if !ent.Thumb || ent.ThumbPath == "" {
				t.Fatalf("Entry = %+v, want a thumbnail", ent)
			}
			if ent.Width != tc.w || ent.Height != tc.h {
				t.Errorf("Entry size = %dx%d, want %dx%d", ent.Width, ent.Height, tc.w, tc.h)
			}
			if ent.Mime != tc.mime {
				t.Errorf("Entry.Mime = %q, want %q", ent.Mime, tc.mime)
			}
			requireThumbFits(t, e.c, 42, tc.w, tc.h)
		})
	}
}

func TestPut_NotDecoded_KeptAsFileCard(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`)
	pdf := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("0 obj\n"), 50)...)
	heic := append([]byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic"), bytes.Repeat([]byte{7}, 200)...)
	mp4 := append([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00isommp42"), bytes.Repeat([]byte{9}, 300)...)
	garbageImage := []byte("this is definitely not a png, whatever the attachment says")
	truncatedPNG := pngBytes(t, 400, 300, 3)
	truncatedPNG = truncatedPNG[:len(truncatedPNG)/2]

	cases := []struct {
		name string
		mime string
		data []byte
	}{
		{"svg", "image/svg+xml", svg},
		{"heic", "image/heic", heic},
		{"pdf", "application/pdf", pdf},
		{"video", "video/mp4", mp4},
		{"octet-stream", "application/octet-stream", []byte{1, 2, 3, 4, 5, 6, 7, 8}},
		{"undecodable image/png", "image/png", garbageImage},
		{"truncated png", "image/png", truncatedPNG},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			_, ent := e.seedPut(1, 77, tc.mime, tc.data)

			if ent.Thumb || ent.ThumbPath != "" {
				t.Errorf("Entry = %+v, want no thumbnail", ent)
			}
			if ent.Width != 0 || ent.Height != 0 {
				t.Errorf("Entry size = %dx%d, want 0x0 (not decoded)", ent.Width, ent.Height)
			}
			if filepath.ToSlash(ent.FilePath) != origPath(1, 77) {
				t.Errorf("Entry.FilePath = %q, want the original kept at %q", ent.FilePath, origPath(1, 77))
			}
			if ent.CachedBytes != int64(len(tc.data)) {
				t.Errorf("Entry.CachedBytes = %d, want %d", ent.CachedBytes, len(tc.data))
			}
			if ent.Mime == "" {
				t.Errorf("Entry.Mime is empty")
			}
			r := e.row(77)
			if r.ThumbPath.Valid {
				t.Errorf("thumb_path = %q, want NULL", r.ThumbPath.String)
			}
			if !r.FilePath.Valid || r.CachedBytes != int64(len(tc.data)) {
				t.Errorf("row file_path/cached_bytes = %+v/%d, want the original kept", r.FilePath, r.CachedBytes)
			}
			got, mime := openAll(t, e.c, 77, false)
			if !bytes.Equal(got, tc.data) {
				t.Errorf("stored original differs from input")
			}
			if tc.mime != "image/png" && mime != tc.mime {
				t.Errorf("Open mime = %q, want the stored %q", mime, tc.mime)
			}
			requireNotCached(t, e.c, 77, true)
			for name := range files(t, e.dir) {
				if strings.HasSuffix(name, ".jpg") {
					t.Errorf("unexpected thumbnail file %s for a non-decoded attachment", name)
				}
			}
		})
	}
}

func TestPut_ImageSizeLimit(t *testing.T) {
	base := pngBytes(t, 640, 400, 9)
	cases := []struct {
		name      string
		total     int64
		wantThumb bool
	}{
		{"exactly MaxImageBytes is decoded", media.MaxImageBytes, true},
		{"one byte over MaxImageBytes is a file card", media.MaxImageBytes + 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			att := e.seed(1, 11, "image/png", 100)
			att.Bytes = tc.total
			// a valid PNG followed by zero padding: decoders ignore the tail, so only a size guard stops the decode
			r := io.MultiReader(bytes.NewReader(base), io.LimitReader(zeroReader{}, tc.total-int64(len(base))))
			ent, err := e.c.Put(context.Background(), att, 1, r)
			if err != nil {
				t.Fatalf("Put: %v", err)
			}
			if ent.Thumb != tc.wantThumb {
				t.Errorf("Entry.Thumb = %v, want %v", ent.Thumb, tc.wantThumb)
			}
			if ent.FilePath == "" || ent.CachedBytes != tc.total {
				t.Errorf("Entry = %+v, want the original kept (%d bytes)", ent, tc.total)
			}
			st, err := os.Stat(filepath.Join(e.dir, filepath.FromSlash(origPath(1, 11))))
			if err != nil || st.Size() != tc.total {
				t.Errorf("stored original: err %v, want %d bytes", err, tc.total)
			}
			if !tc.wantThumb {
				if row := e.row(11); row.ThumbPath.Valid {
					t.Errorf("thumb_path = %q, want NULL", row.ThumbPath.String)
				}
			}
		})
	}
}

func TestPut_PixelBound(t *testing.T) {
	cases := []struct {
		name      string
		file      string
		w, h      int
		wantThumb bool
	}{
		{"exactly 60 MP is decoded", "limit-10000x6000.png", 10000, 6000, true},
		{"one row over 60 MP is not decoded", "bomb-10000x6001.png", 10000, 6001, false},
		{"fake header claiming 900 MP is not decoded", "fakeheader-30000x30000.png", 30000, 30000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			data := fixture(t, tc.file)
			_, ent := e.seedPut(1, 21, "image/png", data)
			if ent.Thumb != tc.wantThumb {
				t.Fatalf("Entry.Thumb = %v, want %v (%+v)", ent.Thumb, tc.wantThumb, ent)
			}
			if ent.FilePath == "" || ent.CachedBytes != int64(len(data)) {
				t.Errorf("Entry = %+v, want the original kept as a file", ent)
			}
			if tc.wantThumb {
				requireThumbFits(t, e.c, 21, tc.w, tc.h)
				return
			}
			if ent.ThumbPath != "" || ent.Width != 0 || ent.Height != 0 {
				t.Errorf("Entry = %+v, want no thumbnail and no decoded size", ent)
			}
			if r := e.row(21); r.ThumbPath.Valid {
				t.Errorf("thumb_path = %q, want NULL", r.ThumbPath.String)
			}
			requireNotCached(t, e.c, 21, true)
		})
	}
}

func TestPut_MissingAttachmentRow_NotFoundAndNothingWritten(t *testing.T) {
	e := newEnv(t)
	data := pngBytes(t, 100, 100, 1)
	ent := e.mustPut(e.seed(1, 2, "image/png", 100), 1, data) // control: Put works for a real row
	if ent.FilePath == "" {
		t.Fatal("control Put stored nothing")
	}
	before := files(t, e.dir)

	ghost := model.Attachment{LocalID: 999, ID: "ghost", Name: "ghost.png", Mime: "image/png", Bytes: int64(len(data))}
	_, err := e.c.Put(context.Background(), ghost, 1, bytes.NewReader(data))
	requireCode(t, err, apperr.NotFound)
	if after := files(t, e.dir); len(after) != len(before) {
		t.Errorf("files after a failed Put = %v, want unchanged %v", after, before)
	}
}

func TestPut_ReaderError_MediaUnavailableAndNothingLeftBehind(t *testing.T) {
	e := newEnv(t)
	att := e.seed(1, 5, "image/png", 100)
	e.mustPut(e.seed(1, 6, "image/png", 100), 1, pngBytes(t, 50, 50, 1)) // control: Put works at all
	before := files(t, e.dir)

	data := pngBytes(t, 300, 300, 4)
	r := &errReader{data: data[:len(data)/2], err: io.ErrUnexpectedEOF}
	_, err := e.c.Put(context.Background(), att, 1, r)
	requireCode(t, err, apperr.MediaUnavailable)

	if after := files(t, e.dir); len(after) != len(before) {
		t.Errorf("files after a failed Put = %v, want unchanged %v (no partial or temp file)", after, before)
	}
	if row := e.row(5); row.FilePath.Valid || row.ThumbPath.Valid || row.CachedBytes != 0 {
		t.Errorf("row after a failed Put = %+v, want unchanged", row)
	}
}

func TestPut_Twice_ReplacesTheOriginalWithoutDoubleCounting(t *testing.T) {
	e := newEnv(t)
	att, _ := e.seedPut(1, 8, "image/png", pngBytes(t, 400, 300, 1))
	second := pngBytes(t, 900, 700, 2)
	ent := e.mustPut(att, 1, second)

	if ent.CachedBytes != int64(len(second)) || ent.Width != 900 || ent.Height != 700 {
		t.Errorf("Entry = %+v, want the second image", ent)
	}
	if r := e.row(8); r.CachedBytes != int64(len(second)) {
		t.Errorf("cached_bytes = %d, want %d", r.CachedBytes, len(second))
	}
	got, _ := openAll(t, e.c, 8, false)
	if !bytes.Equal(got, second) {
		t.Error("Open returns the first image after a second Put")
	}
	st, err := e.c.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Bytes != int64(len(second)) || st.Files != 2 {
		t.Errorf("Stats = %+v, want Bytes %d and Files 2", st, len(second))
	}
	requireThumbFits(t, e.c, 8, 900, 700)
}

func TestPut_HostileNamesAndIdsStayInsideTheMediaDir(t *testing.T) {
	e := newEnv(t)
	att := e.seed(1, 31, "image/png", 100)
	att.Name = "../../../evil.png"
	att.ID = "../../x"
	att.MediaFileID = "../.."
	ent := e.mustPut(att, 1, pngBytes(t, 200, 100, 1))

	if filepath.ToSlash(ent.FilePath) != origPath(1, 31) || filepath.ToSlash(ent.ThumbPath) != thumbPath(1, 31) {
		t.Errorf("paths = %q / %q, want the id-derived layout (names never reach the path)", ent.FilePath, ent.ThumbPath)
	}
	rootFiles := files(t, e.root)
	if len(rootFiles) != 2 {
		t.Errorf("files under the temp root = %v, want only the 2 inside the media dir", rootFiles)
	}
	for name := range rootFiles {
		if !strings.HasPrefix(name, "media/") {
			t.Errorf("file %q was written outside the media dir", name)
		}
	}
}

func TestPut_NegativeIDsStayInsideTheMediaDir(t *testing.T) {
	cases := []struct {
		name     string
		conn, id int64
	}{
		{"negative row id", 1, -5},
		{"negative connection id", -3, 12},
		{"both negative", -1, -300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.mustPut(e.seed(1, 1, "image/png", 100), 1, pngBytes(t, 60, 60, 1)) // control: Put works at all
			att := e.seed(1, tc.id, "image/png", 100)
			ent, err := e.c.Put(context.Background(), att, tc.conn, bytes.NewReader(pngBytes(t, 90, 60, 2)))
			if err == nil {
				for _, p := range []string{ent.FilePath, ent.ThumbPath} {
					if p == "" {
						continue
					}
					if filepath.IsAbs(p) || strings.HasPrefix(filepath.ToSlash(p), "..") || strings.Contains(filepath.ToSlash(p), "/../") {
						t.Errorf("path %q is not inside the media dir", p)
					}
				}
			}
			for name := range files(t, e.root) {
				if !strings.HasPrefix(name, "media/") {
					t.Errorf("file %q was written outside the media dir", name)
				}
			}
		})
	}
}

func TestPut_ConcurrentPutsAndTouchesAreSafe(t *testing.T) {
	e := newEnv(t)
	const n = 8
	data := pngBytes(t, 200, 120, 1)
	atts := make([]model.Attachment, 0, n)
	for i := int64(1); i <= n; i++ {
		att := e.seed(1, i, "image/png", 100+i)
		att.Bytes = int64(len(data))
		atts = append(atts, att)
	}
	errs := make(chan error, n)
	for _, att := range atts {
		go func(att model.Attachment) {
			_, err := e.c.Put(context.Background(), att, 1, bytes.NewReader(data))
			e.c.Touch(att.LocalID)
			errs <- err
		}(att)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Put: %v", err)
		}
	}
	st, err := e.c.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 2*n || st.Bytes != int64(n*len(data)) {
		t.Errorf("Stats = %+v, want %d files and %d original bytes", st, 2*n, n*len(data))
	}
}

// ---- PutThumb ----

func TestPutThumb_StoresAJPEGThumbnailWithinBounds(t *testing.T) {
	cases := []struct {
		name string
		w, h int
		data func(t *testing.T) []byte
	}{
		{"jpeg provider thumb 300x150", 300, 150, func(t *testing.T) []byte { return jpegBytes(t, 300, 150) }},
		{"png provider thumb 300x150", 300, 150, func(t *testing.T) []byte { return pngBytes(t, 300, 150, 1) }},
		{"oversize provider thumb 800x500 is shrunk", 800, 500, func(t *testing.T) []byte { return pngBytes(t, 800, 500, 1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.mustPut(e.seed(1, 2, "image/png", 100), 1, pngBytes(t, 40, 40, 1)) // control: the cache works
			att := e.seed(1, 300, "image/png", 200)
			if err := e.c.PutThumb(context.Background(), att, 1, bytes.NewReader(tc.data(t))); err != nil {
				t.Fatalf("PutThumb: %v", err)
			}
			r := e.row(300)
			if !r.ThumbPath.Valid || filepath.ToSlash(r.ThumbPath.String) != thumbPath(1, 300) {
				t.Errorf("thumb_path = %+v, want %q", r.ThumbPath, thumbPath(1, 300))
			}
			if r.FilePath.Valid || r.CachedBytes != 0 {
				t.Errorf("file_path/cached_bytes = %+v/%d, want the original to stay uncached", r.FilePath, r.CachedBytes)
			}
			if _, err := os.Stat(filepath.Join(e.dir, filepath.FromSlash(thumbPath(1, 300)))); err != nil {
				t.Errorf("thumbnail file: %v", err)
			}
			requireThumbFits(t, e.c, 300, tc.w, tc.h)
			requireNotCached(t, e.c, 300, false)
			st, err := e.c.Stats(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if st.ThumbBytes <= 0 {
				t.Errorf("Stats.ThumbBytes = %d, want > 0", st.ThumbBytes)
			}
		})
	}
}

func TestPutThumb_UndecodableProviderThumb_MediaUnavailableRowUnchanged(t *testing.T) {
	e := newEnv(t)
	e.mustPut(e.seed(1, 2, "image/png", 100), 1, pngBytes(t, 40, 40, 1)) // control: the cache works
	att := e.seed(1, 3, "image/png", 100)
	err := e.c.PutThumb(context.Background(), att, 1, bytes.NewReader([]byte("not an image")))
	requireCode(t, err, apperr.MediaUnavailable)
	if r := e.row(3); r.ThumbPath.Valid {
		t.Errorf("thumb_path = %q, want NULL", r.ThumbPath.String)
	}
	if _, ok := files(t, e.dir)[thumbPath(1, 3)]; ok {
		t.Error("a thumbnail file was left behind")
	}
}

func TestPutThumb_AfterOversizeOriginal_KeepsTheFileCard(t *testing.T) {
	e := newEnv(t)
	big := fixture(t, "bomb-10000x6001.png")
	att, ent := e.seedPut(1, 90, "image/png", big)
	if ent.Thumb {
		t.Fatal("precondition: the bomb must not produce a thumbnail")
	}
	if err := e.c.PutThumb(context.Background(), att, 1, bytes.NewReader(jpegBytes(t, 280, 160))); err != nil {
		t.Fatalf("PutThumb: %v", err)
	}
	r := e.row(90)
	if !r.ThumbPath.Valid || !r.FilePath.Valid || r.CachedBytes != int64(len(big)) {
		t.Errorf("row = %+v, want both the provider thumbnail and the cached original", r)
	}
	requireThumbFits(t, e.c, 90, 280, 160)
	got, _ := openAll(t, e.c, 90, false)
	if !bytes.Equal(got, big) {
		t.Error("PutThumb disturbed the stored original")
	}
}

// ---- Open ----

func TestOpen_ReturnsFileAndMime(t *testing.T) {
	e := newEnv(t)
	jpg := jpegBytes(t, 700, 450)
	png := pngBytes(t, 700, 450, 3)
	e.seedPut(1, 60, "image/jpeg", jpg)
	e.seedPut(1, 61, "image/png", png)

	got, mime := openAll(t, e.c, 60, false)
	if !bytes.Equal(got, jpg) || mime != "image/jpeg" {
		t.Errorf("jpeg original: mime %q, bytes equal %v", mime, bytes.Equal(got, jpg))
	}
	got, mime = openAll(t, e.c, 61, false)
	if !bytes.Equal(got, png) || mime != "image/png" {
		t.Errorf("png original: mime %q, bytes equal %v", mime, bytes.Equal(got, png))
	}
	for _, id := range []int64{60, 61} {
		_, thumbMime := openAll(t, e.c, id, true)
		if thumbMime != "image/jpeg" {
			t.Errorf("thumb %d mime = %q, want image/jpeg", id, thumbMime)
		}
	}
}

func TestOpen_NotCached_NotFound(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, e *env) (id int64, thumb bool)
	}{
		{"row id that does not exist", func(t *testing.T, e *env) (int64, bool) { return 424242, false }},
		{"row exists but was never fetched (original)", func(t *testing.T, e *env) (int64, bool) {
			e.seed(1, 50, "image/png", 100)
			return 50, false
		}},
		{"row exists but was never fetched (thumb)", func(t *testing.T, e *env) (int64, bool) {
			e.seed(1, 50, "image/png", 100)
			return 50, true
		}},
		{"original evicted", func(t *testing.T, e *env) (int64, bool) {
			e.seedPut(1, 51, "image/png", pngBytes(t, 300, 200, 1))
			e.setCap(1)
			if _, err := e.c.Evict(context.Background()); err != nil {
				t.Fatal(err)
			}
			return 51, false
		}},
		{"file removed from disk behind the row", func(t *testing.T, e *env) (int64, bool) {
			e.seedPut(1, 52, "image/png", pngBytes(t, 300, 200, 1))
			if err := os.Remove(filepath.Join(e.dir, filepath.FromSlash(origPath(1, 52)))); err != nil {
				t.Fatal(err)
			}
			return 52, false
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			// control: a healthy entry opens, so a cache that cannot store anything fails here
			e.seedPut(1, 1, "image/png", pngBytes(t, 120, 80, 5))
			openAll(t, e.c, 1, false)

			id, thumb := tc.setup(t, e)
			requireNotCached(t, e.c, id, thumb)
		})
	}
}

func TestOpen_TamperedRowPathNeverServesAFileOutsideTheMediaDir(t *testing.T) {
	e := newEnv(t)
	orig := pngBytes(t, 300, 200, 1)
	e.seedPut(1, 53, "image/png", orig)
	if err := os.WriteFile(filepath.Join(e.root, "outside.orig"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.W.ExecContext(context.Background(), `UPDATE attachment SET file_path = '../outside.orig' WHERE id = 53`); err != nil {
		t.Fatal(err)
	}

	f, _, err := e.c.Open(53, false)
	if err != nil {
		if f != nil {
			f.Close()
			t.Fatal("Open returned both a file and an error")
		}
		requireCode(t, err, apperr.NotFound)
		return
	}
	defer f.Close()
	got, rerr := io.ReadAll(f)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if bytes.Equal(got, []byte("secret")) {
		t.Fatal("Open served a file outside the media dir")
	}
	if !bytes.Equal(got, orig) {
		t.Errorf("Open returned %d unexpected bytes, want not_found or the genuine original", len(got))
	}
}

func TestPut_CapBelowOneOriginal_StillMakesTheThumbnailThenEvictsTheOriginal(t *testing.T) {
	e := newEnv(t)
	e.setCap(1)
	att := e.seed(1, 9, "image/png", 100)
	ent := e.mustPut(att, 1, pngBytes(t, 640, 400, 1))

	if !ent.Thumb {
		t.Errorf("Entry = %+v, want a thumbnail even though the cap cannot hold the original", ent)
	}
	r := e.row(9)
	if !r.ThumbPath.Valid || r.FilePath.Valid || r.CachedBytes != 0 || !r.EvictedAt.Valid {
		t.Errorf("row = %+v, want thumbnail kept, original evicted (file_path NULL, cached_bytes 0, evicted_at set)", r)
	}
	disk := files(t, e.dir)
	if _, ok := disk[origPath(1, 9)]; ok || len(disk) != 1 {
		t.Errorf("files on disk = %v, want only the thumbnail", disk)
	}
	requireThumbFits(t, e.c, 9, 640, 400)
	requireNotCached(t, e.c, 9, false)
}
