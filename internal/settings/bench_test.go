package settings

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/store"
)

// BenchmarkConfigUpdate is one Configure field save (Store.Update) on a real file database: validate, write, bump the
// revision. Budget (architecture.md section 6.1): <= 10 ms/op.
func BenchmarkConfigUpdate(b *testing.B) {
	ctx := context.Background()
	now := func() time.Time { return time.Date(2026, 11, 23, 9, 30, 0, 0, time.UTC) }
	db, _, err := store.Open(ctx, filepath.Join(b.TempDir(), "cuisle.db"),
		store.Options{Now: now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close(ctx) }()
	st := New(db, now)
	id, err := st.Create(ctx)
	if err != nil {
		b.Fatalf("Create: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		on := i%2 == 0
		if _, err := st.Update(ctx, id, Patch{ShowCommits: &on}); err != nil {
			b.Fatalf("Update: %v", err)
		}
	}
}
