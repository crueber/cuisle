package logx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/crueber/cuisle/internal/apperr"
)

const (
	logFileName    = "cuisle.log"
	defaultMaxSize = 5 << 20
	defaultFiles   = 3
	redactedText   = "[redacted]"
)

var (
	// "Bearer x" / "Basic x" anywhere (headers, JSON, prose).
	schemeRe = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]+`)
	// Key-value pairs whose key ends in token/secret/password, JSON or query/plain style.
	kvRe = regexp.MustCompile(`(?i)([\w-]*(?:token|secret|password)["']?\s*[:=]\s*["']?)[^\s"',;}&]+`)
	// Atlassian API tokens.
	atlassianRe = regexp.MustCompile(`ATATT[A-Za-z0-9_=+/.\-]+`)
)

// New returns a JSON logger writing to the rotating file (and stderr text when Dev), wrapped in the redacting handler.
// The closer flushes and closes the file.
func New(opts Options) (*slog.Logger, io.Closer, error) {
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxSize
	}
	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = defaultFiles
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, nil, apperr.Wrap(apperr.Internal, "cannot create log directory", err)
	}
	w, err := NewRotatingWriter(filepath.Join(opts.Dir, logFileName), maxBytes, maxFiles)
	if err != nil {
		return nil, nil, err
	}
	hopts := &slog.HandlerOptions{Level: opts.Level}
	var h slog.Handler = slog.NewJSONHandler(w, hopts)
	if opts.Dev {
		h = slog.NewMultiHandler(h, slog.NewTextHandler(os.Stderr, hopts))
	}
	return slog.New(RedactingHandler(h)), w, nil
}

// LevelFromEnv maps "debug" → Debug, anything else → Info.
func LevelFromEnv(v string) slog.Level {
	if v == "debug" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// rotatingWriter appends to a file and rotates it when a write would exceed maxBytes.
type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	f        *os.File
	size     int64
}

// NewRotatingWriter opens path for append and rotates to path.1 … path.<keep> when maxBytes would be exceeded.
func NewRotatingWriter(path string, maxBytes int64, keep int) (io.WriteCloser, error) {
	w := &rotatingWriter{path: path, maxBytes: maxBytes, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log %s: %w", w.path, err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("stat log %s: %w", w.path, err)
	}
	w.f, w.size = f, fi.Size()
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate shifts path -> path.1 -> ... -> path.<keep> (dropping the oldest) and reopens path.
func (w *rotatingWriter) rotate() error {
	if w.f != nil {
		f := w.f
		w.f, w.size = nil, 0
		if err := f.Close(); err != nil {
			return fmt.Errorf("close log: %w", err)
		}
	}
	if w.keep < 1 {
		if err := os.Remove(w.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove log: %w", err)
		}
	} else {
		_ = os.Remove(fmt.Sprintf("%s.%d", w.path, w.keep))
		for i := w.keep - 1; i >= 1; i-- {
			from, to := fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1)
			if err := os.Rename(from, to); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("rotate log: %w", err)
			}
		}
		if err := os.Rename(w.path, w.path+".1"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("rotate log: %w", err)
		}
	}
	return w.open()
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	f := w.f
	w.f, w.size = nil, 0
	return f.Close()
}

// Redact removes secrets from s: Authorization header values ("Basic …", "Bearer …"), "token"/"secret"/"password"
// key-value pairs (JSON or query style), and Atlassian API tokens (strings starting "ATATT").
func Redact(s string) string {
	s = schemeRe.ReplaceAllString(s, "$1 "+redactedText)
	s = kvRe.ReplaceAllString(s, "${1}"+redactedText)
	return atlassianRe.ReplaceAllString(s, redactedText)
}

// RedactingHandler wraps h: every string attribute value and the message pass through Redact; attributes whose key
// contains "token", "secret", "password" or "authorization" (case-insensitive) are replaced by "[redacted]".
func RedactingHandler(h slog.Handler) slog.Handler { return &redactingHandler{inner: h} }

type redactingHandler struct {
	inner slog.Handler
}

func (h *redactingHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, Redact(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, nr)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactAttr(a)
	}
	return &redactingHandler{inner: h.inner.WithAttrs(out)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{inner: h.inner.WithGroup(name)}
}

func isSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, s := range []string{"token", "secret", "password", "authorization"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

func redactAttr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, redactedText)
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, Redact(a.Value.String()))
	case slog.KindGroup:
		src := a.Value.Group()
		out := make([]any, 0, len(src))
		for _, ga := range src {
			out = append(out, redactAttr(ga))
		}
		return slog.Group(a.Key, out...)
	case slog.KindAny:
		switch v := a.Value.Any().(type) {
		case error:
			return slog.String(a.Key, Redact(v.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, Redact(v.String()))
		}
	}
	return a
}
