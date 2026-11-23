package file_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/sink/file"
)

func TestReviewFix_ValidateRequiresAbsoluteDir(t *testing.T) {
	abs := t.TempDir()
	tests := []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{"absolute", abs, false},
		{"relative", "exports", true},
		{"dot relative", "./exports", true},
		{"parent relative", "../exports", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := file.New().Validate(map[string]string{"dir": tc.dir})
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			var ae *apperr.Error
			if err == nil {
				t.Fatal("want a validation error")
			}
			ae, _ = err.(*apperr.Error)
			if ae == nil || ae.Code != apperr.Validation || ae.Field != "dir" || ae.Message != "Choose a full folder path." {
				t.Fatalf("err = %#v", err)
			}
		})
	}
}

func TestReviewFix_DeliveredFileIsWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits")
	}
	dir := t.TempDir()
	d := delivery(`{"a":1}`)
	d.Config = map[string]string{"dir": dir}
	rc, err := file.New().Deliver(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(rc.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %v, want 0644", got)
	}
	if filepath.Dir(rc.Ref) == "" {
		t.Fatal("unexpected ref")
	}
}
