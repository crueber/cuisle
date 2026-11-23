// Package sink is the outbound adapter contract, registry and outbox dispatcher (architecture.md §2.3, data-model.md
// §2.8, ADR 0012). No timers: failed deliveries wait for the next explicit trigger.
//
// FROZEN CONTRACT: this file (registry implemented by the orchestrator). WP-21 owns dispatcher*.go, configs*.go and
// internal/sink/file. Imports allowed: export, model, secrets, store, apperr, stdlib.
package sink

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/crueber/cuisle/internal/export"
)

type Capabilities struct {
	Current     bool     `json:"current"`
	Range       bool     `json:"range"`
	Formats     []string `json:"formats"`
	NeedsSecret bool     `json:"needsSecret"`
	Idempotent  bool     `json:"idempotent"`
}

type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Help     string   `json:"help"`
	Type     string   `json:"type"` // text|path|url|select|bool|secret
	Options  []string `json:"options"`
	Required bool     `json:"required"`
	Default  string   `json:"default"`
}

type Sink interface {
	Kind() string
	DisplayName() string
	Capabilities() Capabilities
	ConfigSchema() []Field
	Validate(config map[string]string) error // apperr validation with Field set
	Deliver(ctx context.Context, d Delivery) (Receipt, error)
}

type Delivery struct {
	Key        string // idempotency key
	Attempt    int
	Config     map[string]string
	Secret     string
	Document   export.Document
	Raw        []byte // the frozen JSON
	StandupKey string // space key (or "standup-<id>") for filename templates
	Date       string // local YYYY-MM-DD of the snapshot
}

type Receipt struct {
	Ref    string // path, URL, remote id
	Detail string
}

// Request asks for a payload.
type Request struct {
	Kind string `json:"kind"` // current|range
	From string `json:"from"` // local dates, range only
	To   string `json:"to"`
}

// Config is one configured sink (the bound SinkConfig mirrors it).
type Config struct {
	ID        int64
	Kind      string
	Name      string
	StandupID int64 // 0 = all standups
	Config    map[string]string
	HasSecret bool
	SendOn    string // manual|after_sync
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ConfigInput creates (ID 0) or updates a sink. Secret nil = unchanged, "" = delete.
type ConfigInput struct {
	ID        int64
	Kind      string
	Name      string
	StandupID int64
	Config    map[string]string
	Secret    *string
	SendOn    string
	Enabled   bool
}

// OutboxItem is one sink_outbox row (payload omitted).
type OutboxItem struct {
	ID          int64
	SinkID      int64
	StandupID   int64
	Cause       string // manual|after_sync
	PayloadKind string
	Status      string // pending|delivering|delivered|failed|dead
	Attempts    int
	ErrorCode   string
	Error       string
	Receipt     string
	CreatedAt   time.Time
	DeliveredAt time.Time
}

// Event is reported to the app after each delivery job (becomes sink:delivered / sink:failed).
type Event struct {
	Delivered    bool
	OutboxID     int64
	SinkID       int64
	StandupID    int64
	Kind         string // payload kind
	Ref          string
	ErrorCode    string
	ErrorMessage string
	Attempts     int
	WillRetry    bool // false once dead
}

// BuildFunc produces the export document and its frozen JSON for a standup (supplied by the app).
type BuildFunc func(ctx context.Context, standupID int64, req Request) (doc export.Document, raw []byte, standupKey, date string, err error)

type Dispatcher interface {
	// Enqueue freezes the payload in sink_outbox and runs one delivery job (≤ 3 tries, 1 s / 4 s backoff) before
	// returning. cause manual|after_sync; key "" = uuid (manual), after_sync uses "sync:<run>:<sink>".
	Enqueue(ctx context.Context, sinkID, standupID int64, req Request, cause, key string) (outboxID int64, err error)
	// Flush attempts pending/failed rows of a sink (explicit triggers only).
	Flush(ctx context.Context, sinkID int64) error
	// Retry re-attempts one row (also dead rows).
	Retry(ctx context.Context, outboxID int64) error
	// AfterSync enqueues for every enabled after_sync sink covering standupID (flushing its older failed rows first).
	AfterSync(ctx context.Context, standupID, runID int64) error
}

var (
	regMu sync.RWMutex
	reg   = map[string]Sink{}
)

// Register adds a sink kind (cmd/cuisle only).
func Register(s Sink) {
	regMu.Lock()
	defer regMu.Unlock()
	reg[s.Kind()] = s
}

// Get returns a registered sink.
func Get(kind string) (Sink, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	s, ok := reg[kind]
	return s, ok
}

// Kinds returns registered sinks sorted by kind.
func Kinds() []Sink {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Sink, 0, len(reg))
	for _, s := range reg {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind() < out[j].Kind() })
	return out
}
