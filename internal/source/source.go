// Package source is the inbound adapter contract (architecture.md §2.2, ADR 0012).
// FROZEN CONTRACT (Wave 0, implemented in full by the orchestrator, including the registry).
package source

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
)

type Capabilities struct {
	HasSprints         bool     `json:"hasSprints"`
	HasBacklogRank     bool     `json:"hasBacklogRank"`
	HasFlagging        bool     `json:"hasFlagging"`
	HasDevActivity     bool     `json:"hasDevActivity"`
	HasExtraFilter     bool     `json:"hasExtraFilter"`
	HasAssigneeHistory bool     `json:"hasAssigneeHistory"`
	FilterLanguage     string   `json:"filterLanguage"` // "JQL"
	AuthFields         []string `json:"authFields"`     // e.g. ["baseUrl","account","secret"]
}

// Progress phases reported by adapters during Hydrate (architecture.md §3.3 sync:progress).
const (
	PhaseIssues    = "issues"
	PhaseChangelog = "changelog"
	PhaseComments  = "comments"
	PhaseDev       = "dev"
)

// ProgressFunc receives incremental progress from long adapter calls. May be nil.
type ProgressFunc func(phase string, done, total int)

type Source interface {
	Kind() string
	Capabilities() Capabilities
	Test(ctx context.Context) (model.Account, error)
	SearchSpaces(ctx context.Context, query string) ([]model.Space, error)
	SearchBoards(ctx context.Context, spaceKey, query string) ([]model.Board, error)
	BoardConfig(ctx context.Context, boardID string) (model.BoardConfig, error)
	ActiveSprints(ctx context.Context, boardID string) ([]model.Sprint, error) // empty when !HasSprints
	Reference(ctx context.Context) (statuses []model.Status, types []model.IssueType, err error)
	SearchUsers(ctx context.Context, spaceKey, query string) ([]model.Person, error)
	ValidateFilter(ctx context.Context, expr string) (ok bool, errs []string, err error)
	Plan(ctx context.Context, in PlanInput) ([]Query, error)
	Scan(ctx context.Context, q Query) (ScanResult, error)
	Hydrate(ctx context.Context, sourceIDs []string, progress ProgressFunc) ([]model.IssueDetail, error)
	DevActivity(ctx context.Context, sourceID, prevFingerprint string) (model.DevActivity, error) // ErrUnsupported when !HasDevActivity
	IssueURL(key string) string
	// Meta returns provider bookkeeping to persist in connection.source_meta after a run (Jira: field ids).
	Meta() map[string]any
	// Requests returns the number of HTTP requests and rate-limit responses since the Source was opened.
	Requests() (requests, throttled int)
}

type PlanInput struct {
	Board         model.BoardConfig
	Scope         string // "sprint"|"board"
	ExtraFilter   string
	ActiveSprints []model.Sprint
	StatusLanes   map[string]string // status id → lane
	BacklogLimit  int
}

type Query struct {
	Name  string // "sprint"|"open"|"done"|"backlog"
	Role  string // "board"|"backlog"
	Limit int    // 0 = no limit
	Expr  string // provider query, opaque to the core
	Count bool   // also request an approximate total (backlog)
}

type ScanItem struct {
	SourceID, Key string
	UpdatedAt     time.Time
}

type ScanResult struct {
	Items       []ScanItem
	ApproxTotal int // -1 unknown
}

// Options are shared runtime dependencies for an adapter.
type Options struct {
	HTTP      *http.Client
	Logger    *slog.Logger
	Now       func() time.Time
	UserAgent string
	Meta      map[string]any // connection.source_meta as last persisted
	Full      bool           // the Source serves a full sync: refresh cached provider reference data (Jira: field ids)
}

type Factory func(conn model.Connection, secret string, opts Options) (Source, error)

// URLFunc builds an issue URL without credentials (used by view snapshots).
type URLFunc func(baseURL, key string) string

var ErrUnsupported = apperr.New(apperr.Unsupported, "This source does not support that.")

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
	urlFuncs  = map[string]URLFunc{}
)

// Register adds an adapter kind. Called from cmd/cuisle only. Re-registering replaces.
func Register(kind string, f Factory, u URLFunc) {
	mu.Lock()
	defer mu.Unlock()
	factories[kind] = f
	urlFuncs[kind] = u
}

// Open constructs a Source for conn. Unknown kind → apperr not_found.
func Open(conn model.Connection, secret string, opts Options) (Source, error) {
	mu.RLock()
	f, ok := factories[conn.Source]
	mu.RUnlock()
	if !ok {
		return nil, apperr.New(apperr.NotFound, "Unknown source kind \""+conn.Source+"\".")
	}
	return f(conn, secret, opts)
}

// IssueURLPrefix returns the URL prefix for a kind (URL = prefix + key); "" for an unknown kind.
func IssueURLPrefix(kind, baseURL string) string {
	mu.RLock()
	u, ok := urlFuncs[kind]
	mu.RUnlock()
	if !ok || u == nil {
		return ""
	}
	return u(baseURL, "")
}

// Kinds lists registered kinds, sorted.
func Kinds() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(factories))
	for k := range factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// IsUnsupported reports whether err is ErrUnsupported or carries code unsupported.
func IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupported) || apperr.Is(err, apperr.Unsupported)
}
