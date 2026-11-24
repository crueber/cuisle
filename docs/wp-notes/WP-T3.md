# WP-T3: media cache

Implements `internal/media` (architecture.md §2.4, data-model.md §2.5a, ADR 0015) behind the frozen `contract.go`.

- Files: `media.go` (cache state, Open, Touch, Stats, Clear, disk walk), `put.go` (Put, PutThumb, bounded decode,
  thumbnail scaling, temp-file commit), `evict.go` (Evict, LRU victims, orphan sweep), `sweep_test.go` (new).
- Layout: `<dir>/<connection>/<row id mod 256>/<row id>.orig` and `.thumb.jpg`, slash-separated in the columns.
- Decode: only declared image mimes under `MaxImageBytes`. The header is read first and refused above `MaxPixels`
  (inclusive). Refused, undecodable and non-image files become file cards (no error, no thumbnail).
- Writes: temp file in the destination directory, then rename. A failed read or write leaves no temp file.
- Concurrency: one mutex serialises Put, PutThumb, Evict, Clear and Stats, so a sweep never sees an in-flight temp file.
- Eviction: never-viewed rows first (oldest `created_at`), then by `last_viewed_at`. Thumbnails are never evicted.
- Orphan sweep (decided in F12): inside `Evict`, files under the media dir with no row reference via `file_path` or
  `thumb_path` are removed and counted in `freedBytes`. Only regular files under the media dir are visited.
- Errors: `not_found`, `media_unavailable` (read or decode of provider data), `db`, `internal` (disk I/O).

Setup fixes to the test files: none.
