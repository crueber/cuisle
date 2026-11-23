package store_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/crueber/cuisle/internal/store"
)

// BenchmarkOpenExisting is the database part of a cold start: open an existing, already migrated file database
// (pragmas, migration check, quick check) and close it. It is the floor of BenchmarkBootstrap in internal/app, whose
// budget (architecture.md section 6.1) is <= 60 ms for open + Bootstrap.
func BenchmarkOpenExisting(b *testing.B) {
	ctx := context.Background()
	path := filepath.Join(b.TempDir(), "cuisle.db")
	opts := store.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	db, _, err := store.Open(ctx, path, opts)
	if err != nil {
		b.Fatalf("first open: %v", err)
	}
	if err := db.Close(ctx); err != nil {
		b.Fatalf("close: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db, _, err := store.Open(ctx, path, opts)
		if err != nil {
			b.Fatalf("open: %v", err)
		}
		b.StopTimer()
		_ = db.Close(ctx)
		b.StartTimer()
	}
}
