package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/buildinfo"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/store"
	"github.com/crueber/cuisle/internal/view"
)

// Method sets are FROZEN (architecture.md section 3.2). The bodies of the other services live in services_*.go.
// Every method defers Deps.guard so a panic becomes a logged, typed internal error.

type AppService struct {
	d *Deps

	// LogClientError state: distinct reports seen, and the times of the reports logged in the last minute.
	mu     sync.Mutex
	seen   map[string]struct{}
	logged []time.Time
}

// Client-error log limits (architecture.md section 6.2).
const (
	clientErrorsPerMinute = 20
	clientErrorMaxMessage = 2000
	clientErrorMaxStack   = 8000
	clientErrorMaxSeen    = 500
)

// issueKeyRE is deliberately narrow: whatever passes is appended to the issue URL prefix, so nothing that could
// change the URL (slashes, dots, "?", "#", spaces, schemes) may pass.
var issueKeyRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)

func (s *AppService) Bootstrap(ctx context.Context) (out Bootstrap, err error) {
	defer s.d.guard("Bootstrap", &err)
	return s.d.bootstrap(ctx)
}

func (s *AppService) SetAppSetting(ctx context.Context, key string, value string) (ack SaveAck, err error) {
	defer s.d.guard("SetAppSetting", &err)
	a, err := s.d.Settings.SetAppSetting(ctx, key, value)
	if err != nil {
		return SaveAck{}, err
	}
	return saveAck(a), nil
}

func (s *AppService) SetUIState(ctx context.Context, patch UIStatePatch) (err error) {
	defer s.d.guard("SetUIState", &err)
	return s.d.Settings.SetUIState(ctx, patch)
}

func (s *AppService) OpenIssue(ctx context.Context, issueKey string) (err error) {
	defer s.d.guard("OpenIssue", &err)
	if !issueKeyRE.MatchString(issueKey) {
		return apperr.Validationf("issueKey", "That is not an issue key.")
	}
	row, ok, err := s.d.connRow(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.New(apperr.AuthMissing, "Connect to Jira to open issues.")
	}
	prefix := source.IssueURLPrefix(row.Source, row.BaseURL)
	if prefix == "" {
		return apperr.New(apperr.Unsupported, "This source cannot open issues.")
	}
	if s.d.OpenURL == nil {
		return apperr.New(apperr.Unsupported, "Opening links is not available here.")
	}
	if err := s.d.OpenURL(prefix + issueKey); err != nil {
		return apperr.Wrap(apperr.Internal, "Could not open the browser.", err)
	}
	return nil
}

// OpenURL opens an http, https or mailto link in the OS browser or mail client (links inside ticket text, ux-spec
// §14.4). Anything else is refused; the webview itself never navigates.
func (s *AppService) OpenURL(ctx context.Context, rawURL string) (err error) {
	defer s.d.guard("OpenURL", &err)
	u, perr := url.Parse(strings.TrimSpace(rawURL))
	if perr != nil || len(rawURL) > 4096 {
		return apperr.Validationf("url", "That is not a link.")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" {
			return apperr.Validationf("url", "That is not a link.")
		}
	case "mailto":
		if u.Opaque == "" && u.Path == "" {
			return apperr.Validationf("url", "That is not a link.")
		}
	default:
		return apperr.Validationf("url", "Only web and mail links can be opened.")
	}
	if s.d.OpenURL == nil {
		return apperr.New(apperr.Unsupported, "Opening links is not available here.")
	}
	if err := s.d.OpenURL(u.String()); err != nil {
		return apperr.Wrap(apperr.Internal, "Could not open the link.", err)
	}
	return nil
}

func (s *AppService) Diagnostics(ctx context.Context) (out Diagnostics, err error) {
	defer s.d.guard("Diagnostics", &err)
	d := s.d
	version, err := store.SchemaVersion(ctx, d.DB.R)
	if err != nil {
		return Diagnostics{}, err
	}
	runs, err := cache.RecentRuns(ctx, d.DB.R, 0, 5)
	if err != nil {
		return Diagnostics{}, err
	}
	out = Diagnostics{
		Build: buildinfo.Get(), OS: runtime.GOOS, DataDir: d.Paths.Data, LogDir: d.Paths.Logs,
		DBBytes: dbBytes(d.DB.Path), SchemaVersion: version, RecentRuns: make([]RunInfo, 0, len(runs)),
	}
	for _, r := range runs {
		warnings := r.Warnings
		if warnings == nil {
			warnings = []string{}
		}
		out.RecentRuns = append(out.RecentRuns, RunInfo{
			ID: r.ID, StandupID: r.StandupID, Mode: r.Mode, Trigger: r.Trigger, Status: r.Status,
			StartedAt: store.Ms(r.StartedAt), FinishedAt: store.Ms(r.FinishedAt), Requests: r.Requests,
			ErrorCode: r.ErrorCode, Warnings: warnings,
		})
	}
	return out, nil
}

// dbBytes is the size of the database file plus its WAL (0 for an in-memory database).
func dbBytes(path string) int64 {
	if path == "" {
		return 0
	}
	var n int64
	for _, p := range []string{path, path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			n += fi.Size()
		}
	}
	return n
}

func (s *AppService) ReportPerf(ctx context.Context, marks map[string]float64) (err error) {
	defer s.d.guard("ReportPerf", &err)
	if s.d.PerfSink != nil {
		s.d.PerfSink(marks)
	}
	return nil
}

func (s *AppService) LogClientError(ctx context.Context, message string, stack string) (err error) {
	defer s.d.guard("LogClientError", &err)
	// LogClientError logs a frontend error: identical message+stack pairs once, at most 20 distinct per rolling minute
	// (measured on Deps.Clock). Being limited is never an error to the caller.
	message, stack = truncate(message, clientErrorMaxMessage), truncate(stack, clientErrorMaxStack)
	sum := sha256.Sum256([]byte(message + "\x00" + stack))
	key := hex.EncodeToString(sum[:])
	now := s.d.now()
	if s.d.Clock != nil {
		now = s.d.Clock.Now()
	}

	s.mu.Lock()
	if s.seen == nil {
		s.seen = map[string]struct{}{}
	}
	if _, dup := s.seen[key]; dup {
		s.mu.Unlock()
		return nil
	}
	cut := now.Add(-time.Minute)
	kept := s.logged[:0]
	for _, t := range s.logged {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.logged = kept
	if len(s.logged) >= clientErrorsPerMinute {
		s.mu.Unlock()
		return nil
	}
	if len(s.seen) >= clientErrorMaxSeen {
		s.seen = map[string]struct{}{}
	}
	s.seen[key] = struct{}{}
	s.logged = append(s.logged, now)
	s.mu.Unlock()

	s.d.log().Error("client error", "message", message, "stack", stack)
	return nil
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "..."
}

type StandupService struct{ d *Deps }

func (s *StandupService) List(ctx context.Context) (out []view.StandupTab, err error) {
	defer s.d.guard("List", &err)
	return s.d.tabs(ctx)
}

func (s *StandupService) View(ctx context.Context, standupID int64) (out view.StandupView, err error) {
	defer s.d.guard("View", &err)
	return s.d.buildView(ctx, standupID)
}

// HistoryService is a PLACEHOLDER: bound, no v1 UI, returns not_implemented until the history milestone.
type HistoryService struct{ d *Deps }

func (s *HistoryService) Range(ctx context.Context, standupID int64) (HistoryRange, error) {
	return HistoryRange{}, apperr.NotImplemented
}

func (s *HistoryService) ViewAsOf(ctx context.Context, standupID int64, date string) (view.StandupView, error) {
	return view.StandupView{}, apperr.NotImplemented
}

// bootstrap assembles the Bootstrap payload from the database only (no network).
func (d *Deps) bootstrap(ctx context.Context) (Bootstrap, error) {
	b := Bootstrap{Build: buildinfo.Get(), Recovered: d.Recovered}
	var err error
	if b.Settings, err = d.Settings.AppSettings(ctx); err != nil {
		return Bootstrap{}, err
	}
	if b.Connection, err = d.connection(ctx); err != nil {
		return Bootstrap{}, err
	}
	if b.Standups, err = d.tabs(ctx); err != nil {
		return Bootstrap{}, err
	}
	if b.UI, err = d.Settings.UIState(ctx); err != nil {
		return Bootstrap{}, err
	}
	if b.UI.LastPerson == nil {
		b.UI.LastPerson = map[int64]string{}
	}
	if b.Sync, err = d.syncStatuses(ctx); err != nil {
		return Bootstrap{}, err
	}
	if id := visibleStandup(b.Standups, b.UI.LastStandupID); id != 0 {
		v, err := d.buildView(ctx, id)
		if err != nil {
			return Bootstrap{}, err
		}
		b.View = &v
	}
	return b, nil
}

// visibleStandup is ui.lastStandupId when that standup is live and configured, else the first configured standup
// in tab order, else 0.
func visibleStandup(tabs []view.StandupTab, last int64) int64 {
	first := int64(0)
	for _, t := range tabs {
		if !t.Configured {
			continue
		}
		if t.ID == last {
			return last
		}
		if first == 0 {
			first = t.ID
		}
	}
	return first
}
