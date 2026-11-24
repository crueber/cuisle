# Cuisle — Architecture

Status: accepted for milestone 1. Companion docs: `data-model.md` (schema), `jira-sync.md` (Jira I/O), `adr/` (decisions).
Conventions used below: lane ids are `backlog | todo | prog | review | qa | done | hidden`; all timestamps are
`INTEGER` Unix milliseconds UTC; "local" means the OS timezone (`time.Local`).

## 1. System overview

```
┌───────────────────────────────── cuisle (single OS process, Go) ─────────────────────────────────┐
│  cmd/cuisle/main.go — wires everything; registers source adapters (jira) and sinks (file)        │
│                                                                                                  │
│  ┌─────────── internal/app (Wails v3 services = the only API the UI sees) ────────────────────┐  │
│  │ AppService  StandupService  ConfigService  ConnectionService  SyncService                  │  │
│  │ TicketService  SinkService  HistoryService (placeholder, no v1 UI)                         │  │
│  └────┬────────────┬──────────────┬───────────────┬───────────────┬──────────────┬────────────┘  │
│       ▼            ▼              ▼               ▼               ▼              ▼               │
│   settings       view ◄── pulse   history       sync ──────► source.Source     sink.Sink        │
│   (config)    (view models,     (as-of loader)  (orchestrator)  ▲ interface      ▲ interface     │
│       │        asOf-param.)        │               │            │                │               │
│       │            ▲               │               │      source/jira      sink/file (v1)        │
│       │            │ model.BoardSnapshot           │      (REST adapter)   webhook, linear…      │
│       │          cache ◄───────────┘───────────────┘            │          (later)  ▲            │
│       │   (neutral record store I/O)                            │                  export        │
│       ▼            ▼                                            │           (cuisle.export.v1)   │
│  ┌──────────────── internal/store (SQLite, modernc, WAL) ──────┼─────────────────────────────┐   │
│  │ writer pool (1 conn, BEGIN IMMEDIATE)      reader pool (4 conns, query_only)              │   │
│  └───────────────────────────┬─────────────────────────────────┼─────────────────────────────┘   │
│                              ▼                                 │        secrets (OS keychain)    │
│                   <data dir>/cuisle.db (+ -wal, -shm)          │        model (neutral types,    │
│                                                                │         imported by all above)  │
│  Wails v3 runtime: asset server (embedded frontend/dist + /boot.js middleware), IPC bridge       │
└───────────────┬────────────────────────────────────────────────┼─────────────────────────────────┘
                │ IPC (bound method calls, events)                │ HTTPS (Basic auth)
                ▼                                                ▼
   OS webview (WKWebView / WebView2 / WebKitGTK)        https://<site>.atlassian.net
   Preact + signals UI (frontend/)                      (or --fake-jira on 127.0.0.1)
```

The core (sync, cache, view, pulse, history, export) is **provider-neutral**: it speaks `internal/model` types only.
Jira enters through one inbound adapter (`internal/source/jira`); data leaves through outbound adapters (`internal/sink/*`).
See ADR 0012. Synced facts are kept forever and every view is built "as of" a time, so a historical view is a reuse of
the same builder (ADR 0011).

### Process model

- One Go process, one window. The OS webview runs in its own helper process(es) owned by the OS.
- Single instance (`application.SingleInstanceOptions`, unique id = bundle id); a second launch focuses the first.
- Goroutines: Wails main loop; one goroutine per bound call (Wails); at most **one sync run per standup**, at most
  **two standups syncing concurrently**; one HTTP worker pool shared by all runs (max 4 in-flight requests);
  one "midnight" timer (re-emits `day:rolled`). No tickers, no polling.
- No network I/O happens unless the user (a) presses Resync, (b) opens the app with a sync-on-open policy that
  applies, (c) changes a standup's source in Configure, (d) types in a source-backed combobox / connects, or
  (e) a sink delivers on an explicit trigger (manual Send, or after a successful sync when opted in), or
  (f) acts on a ticket (ADR 0015): opens the Status combobox in the ticket modal (fetches transitions), sends a light
  write (transition, assign, flag, comment — each followed by a one-issue refresh), or explicitly opens an attachment /
  full-size image that is not in the media cache. **Opening the ticket modal itself is never a trigger.**

## 2. Go module layout

Module path: `github.com/crueber/cuisle` (decided; referred to as `MODULE` below).
Go ≥ 1.24. `go.mod` pins exact versions of `github.com/wailsapp/wails/v3`, `modernc.org/sqlite`,
`github.com/zalando/go-keyring`, `golang.org/x/image` (thumbnail scaling and WebP/BMP/TIFF decoding in `internal/media`;
pure Go, the only added Go dependency for ADR 0015). `frontend/package.json` pins the same exact `@wailsio/runtime` version as the Go module.

```
cuisle/
├── cmd/
│   ├── cuisle/            main(): flags, paths, logger, store open+migrate, services, window, run
│   └── fixturegen/        dev tool: design/reference/sample-data.json → internal/source/jira/jiratest/testdata/fixture.json
├── internal/
│   ├── app/               Wails services (thin): arg validation, call domain packages, map errors, emit events
│   ├── apperr/            typed error {Code, Message, Detail, Retryable}; JSON marshalling for the bridge
│   ├── paths/             per-OS data/log dirs, CUISLE_DATA_DIR override
│   ├── logx/              slog setup, size-rotating file writer, secret redaction
│   ├── store/             DB open (pragmas, pools), migrations runner, tx helpers; NO business rules
│   │   └── migrations/    NNNN_name.sql (embedded)
│   ├── settings/          app settings + standup config CRUD (standups, lanes, mapping, speakers, ui state)
│   ├── secrets/           Keyring interface + go-keyring impl + in-memory impl (tests, fake mode)
│   ├── model/             provider-neutral domain types (Person, Issue, StatusChange, Board, Sprint, BoardSnapshot…); stdlib only
│   ├── source/            inbound adapter contract: Source interface, Capabilities, registry
│   │   └── jira/          Jira Cloud adapter: REST client (auth, pagination, rate-limit/backoff, DTOs) + mapping to model
│   │       └── jiratest/  fake Jira HTTP server (fixture-backed, fault injection) for dev and tests
│   ├── cache/             read/write of the neutral record store; LoadCurrent(standupID) → model.BoardSnapshot; apply; trim
│   ├── history/           as-of loader: LoadAsOf(standupID, t) → model.BoardSnapshot from spans + change tables (STUB in v1)
│   ├── sync/              sync orchestrator over source.Source: plan → fetch (memory) → atomic apply; progress; cancellation
│   ├── pulse/             pure functions: status segments, day buckets, days-in-lane, blocked spans, speaker order
│   ├── view/              builds StandupView and TicketDetail (from a BoardSnapshot + asOf) / StandupConfig / Bootstrap
│   ├── adf/               rich text: parse/normalise stored ADF → typed node tree; resolve media, mentions, inline cards
│   ├── media/             attachment cache on disk: store, thumbnails (golang.org/x/image), LRU eviction under a cap, clear
│   ├── export/            cuisle.export.v1 document: build from StandupView(s), JSON encoding (CSV/Markdown renderers later)
│   ├── sink/              outbound adapter contract: Sink interface, registry, outbox dispatcher
│   │   └── file/          reference sink (v1): writes the export JSON to a file
│   └── buildinfo/         version, commit, date (ldflags)
├── frontend/              see §4
├── build/                 Wails v3 build assets (Taskfiles, icons, Info.plist, NSIS, nfpm, AppImage)
├── design/                mockups, reference, tokens.json (source of CSS tokens)
├── docs/
├── Taskfile.yml           dev, dev:real, bindings, tokens, build, package, test, lint, bench, size
├── .forgejo/workflows/    ci.yml (Linux: test + package + Forgejo release) · ci-image.yml · publish-github.yml
├── build/ci/              linux.Dockerfile (CI/build image)
└── scripts/               publish-to-github.sh · publish/{ci,release}.yml (GitHub workflow templates) · layercheck/
```

Dependency rule (enforced with a `depguard`/`go list` check in CI):

| Package | May import (internal) |
|---|---|
| `model`, `pulse`, `apperr`, `buildinfo`, `paths`, `logx` | nothing (`pulse` may import `model`; `paths` and `logx` may import the leaf `apperr`) |
| `store` | `apperr` |
| `settings`, `cache` | `store`, `model`, `apperr` |
| `source` | `model`, `apperr` |
| `source/jira` | `source`, `model`, `apperr`, `adf` (comment bodies, F12) (never `cache`, `store`, `sync`) |
| `sync` | `source` (interface only — **never `source/jira`**), `cache`, `settings`, `secrets`, `model` |
| `history` | `cache`, `store`, `settings`, `model` |
| `adf` | `model` |
| `media` | `model`, `store`, `paths`, `apperr` (never a source: bytes are handed to it) |
| `view` | `model`, `cache`, `settings`, `pulse`, `adf` (takes snapshots; does not import `history`) |
| `export` | `view`, `model` |
| `sink`, `sink/*` | `export`, `model`, `secrets`, `store` (outbox), `apperr` |
| `app` | everything above except concrete adapters |
| `cmd/cuisle` | everything; the only place `source/jira` and `sink/file` are imported and registered |

Nothing imports `app`. Only `internal/app` and `cmd/cuisle` import Wails. The string "jira" appears in the core only as
data (`connection.source`).

Key interfaces (so tests can substitute):

```go
// internal/secrets — keychain service name "crueber.cuisle"
type Keyring interface {
    Get(account string) (secret string, err error)   // ErrNotFound when absent
    Set(account, secret string) error
    Delete(account string) error
}
func ConnectionAccount(source, host, account string) string // "<source>:<host>:<account>"  e.g. "jira:acme.atlassian.net:me@acme.com"
func SinkAccount(sinkID int64) string                       // "sink:<id>"

// internal/sync
type Emitter interface { Emit(name string, payload any) }    // implemented in app over Wails events
type Clock   interface { Now() time.Time; Location() *time.Location }
```

### 2.1 Neutral model (`internal/model`)

Plain structs, no behaviour beyond small helpers, no provider vocabulary. IDs are the provider's ids as strings.
**`internal/model/model.go` is authoritative** (frozen in Wave 0). It adds a few fields to the sketch below:
`Board.RawType`, `Status.BlockedHint`, `IssueDetail.Sprints`, and the full shapes of `EffectiveConfig`, `Member`,
`IssueHistory` and `BoardSnapshot` (including `Statuses`, `BacklogTotal`, `IssueURLPrefix`, `DevActivity`,
`LastSyncedAt`, `Warnings`).

```go
type Connection struct { ID int64; Source, BaseURL, Account string }
type Account    struct { ID, DisplayName string }                       // the authenticated user
type Person     struct { AccountID, DisplayName string; Active bool; Kind string /* "user"|"app"|"customer" */ }
type Space      struct { ID, Key, Name string; Meta map[string]any }
type Board      struct { ID, Name string; Kind string /* "scrum"|"kanban" */; SpaceID string; Meta map[string]any }
type Column     struct { Name string; Position int; StatusIDs []string }
type Status     struct { ID, Name string; Category string /* "new"|"indeterminate"|"done"|"undefined" */ }
type IssueType  struct { ID, Name string; Kind string /* story|bug|task|spike|subtask|epic|other */ }
type Sprint     struct { ID, BoardID, Name, State string; StartAt, EndAt, CompleteAt time.Time }
type BoardConfig struct { Board Board; Columns []Column }

type Issue struct {
    SourceID, Key, SpaceID, Summary, TypeID, StatusID string
    AssigneeID string            // "" = unassigned
    Flagged    bool
    Rank       string
    ParentKey  string
    SprintIDs  []string
    CreatedAt, UpdatedAt, ResolvedAt time.Time
    Meta       map[string]any
}
type StatusChange   struct { ID string; At time.Time; From, To string }           // status ids
type AssigneeChange struct { ID string; At time.Time; From, To string }           // account ids, "" = unassigned
type FlagChange     struct { ID string; At time.Time; Flagged bool }
type Comment        struct { ID string; At, UpdatedAt time.Time; AuthorID string
                             Body RichText                     // stored locally (ADR 0015); never exported or logged
                             Visibility *Visibility }          // nil = everyone; {Type "role"|"group", Value "Developers"}
type DevEvent       struct { Kind string /* "commit"|"pr" */; ID, Repo string; At time.Time; Status string /* pr: OPEN|MERGED|DECLINED */; Merge bool }

type IssueDetail struct {     // what Hydrate returns for one issue
    Issue; Type IssueType; Status Status; Assignee *Person
    StatusChanges []StatusChange; AssigneeChanges []AssigneeChange; FlagChanges []FlagChange
    Comments []Comment; CommentTotal int; People []Person      // Comments: complete list when Capabilities.Content
    Content  *IssueContent                                      // nil when !Capabilities.Content
}

// ── Ticket content (ADR 0015) ──
type RichText struct { Format string /* "adf" (the neutral rich-text format; other sources convert to it) */; Raw json.RawMessage }
type Attachment struct { ID, Name, Mime string; Bytes int64; AuthorID string; CreatedAt time.Time
                         MediaFileID string /* id used by rich-text media nodes; "" if unknown */; Width, Height int }
type FieldDef   struct { ID, Name string; Kind string; Custom bool; CustomType string; Schema json.RawMessage }
type FieldValue struct {
    FieldID string
    Kind    string     // text|paragraph|number|date|datetime|url|option|options|cascade|user|users|group|groups|
                       // version|versions|component|components|labels|project|team|unknown
    Text    string     // text, option, url, date ("YYYY-MM-DD"), datetime (RFC 3339), group/project/team/version/component name
    Number  *float64
    Items   []string   // options, labels, versions, components, groups; cascade = [parent, child]
    Users   []string   // account ids (user, users)
    Rich    *RichText  // paragraph
    Raw     json.RawMessage // unknown: provider value verbatim
}
type IssueLink  struct { ID, TypeName, Label string /* "is blocked by" */; Outward bool; OtherSourceID, OtherKey, OtherSummary, OtherStatusID, OtherTypeID string }
type IssueRef   struct { SourceID, Key, Summary, StatusID, TypeID, AssigneeID string }
type IssueContent struct {
    Description, Environment *RichText
    Attachments []Attachment
    Fields      []FieldValue      // every non-empty field value except those modelled on Issue; empties are implied by FieldDef
    Links       []IssueLink
    Subtasks    []IssueRef
    Parent      *IssueRef
    Watchers    int               // -1 unknown
    Hash        string            // content hash; unchanged hash → no rewrite
}
type Transition struct { ID, Name, ToStatusID, ToStatusName string; NeedsScreen bool }
type NewComment struct { Text string; Mentions []Mention /* {Start, End int (rune offsets into Text); AccountID, Name} */ }
type DevActivity struct { Fingerprint string; Changed bool; Events []DevEvent }

// What the view builder consumes — produced by cache.LoadCurrent or history.LoadAsOf.
type BoardSnapshot struct {
    StandupID int64; AsOf time.Time; Live bool
    Config    EffectiveConfig          // scope, lanes+limits, status→lane map, speakers, pulse/done/backlog options
    Sprints   []Sprint                 // active as of AsOf
    Members   []Member                 // {Issue (state as of AsOf), TypeKind, Role "board"|"backlog", RankIndex}
    History   map[string]IssueHistory  // by Issue.SourceID: status/assignee/flag changes, comments, dev events (≤ AsOf)
    People    map[string]Person
}
```

### 2.2 Inbound adapters (`internal/source`)

```go
type Capabilities struct {
    HasSprints, HasBacklogRank, HasFlagging, HasDevActivity, HasExtraFilter, HasAssigneeHistory bool
    Content bool            // Hydrate returns IssueContent + full comments; Fields/FetchMedia work (ADR 0015)
    Writes  Writes          // {Transition, Assign, Flag, Comment bool}; all false = read-only source
    FilterLanguage string   // "JQL"; shown as the Extra filter hint
    AuthFields     []string // credential fields the Connect form needs, e.g. ["baseUrl","account","secret"]
}

type Source interface {
    Kind() string                                   // "jira"
    Capabilities() Capabilities
    Test(ctx context.Context) (model.Account, error)                                     // auth check
    SearchSpaces(ctx context.Context, query string) ([]model.Space, error)
    SearchBoards(ctx context.Context, spaceKey, query string) ([]model.Board, error)
    BoardConfig(ctx context.Context, boardID string) (model.BoardConfig, error)
    ActiveSprints(ctx context.Context, boardID string) ([]model.Sprint, error)           // empty when !HasSprints
    Reference(ctx context.Context) (statuses []model.Status, types []model.IssueType, err error)
    SearchUsers(ctx context.Context, spaceKey, query string) ([]model.Person, error)
    ValidateFilter(ctx context.Context, expr string) (ok bool, errs []string, err error)
    Plan(ctx context.Context, in PlanInput) ([]Query, error)                             // membership queries (opaque to the core)
    Scan(ctx context.Context, q Query) (ScanResult, error)                               // ids + updated, in rank order
    Hydrate(ctx context.Context, sourceIDs []string, progress ProgressFunc) ([]model.IssueDetail, error) // fields + full change history + comments (+ content when Capabilities.Content); progress per phase
    DevActivity(ctx context.Context, sourceID, prevFingerprint string) (model.DevActivity, error) // ErrUnsupported when !HasDevActivity
    IssueURL(key string) string
    Meta() map[string]any                  // provider bookkeeping to persist in connection.source_meta (Jira: field ids)
    Requests() (requests, throttled int)   // counters for sync_run

    // Content (Capabilities.Content) — ADR 0015
    Fields(ctx context.Context) ([]model.FieldDef, error)                                // field registry incl. custom fields, with Kind
    FetchMedia(ctx context.Context, att model.Attachment, thumb bool) (MediaBlob, error) // {Body io.ReadCloser; Mime string; Bytes int64; MediaFileID string}
    GetIssue(ctx context.Context, sourceID string) (model.IssueDetail, error)            // single-issue refresh = Hydrate of one (nil progress), always complete

    // Writes (Capabilities.Writes) — explicit user actions only; each returns after the provider accepted the change
    Transitions(ctx context.Context, sourceID string) ([]model.Transition, error)
    Transition(ctx context.Context, sourceID, transitionID string) error
    Assign(ctx context.Context, sourceID, accountID string) error                        // "" = unassign
    SetFlag(ctx context.Context, sourceID string, flagged bool) error
    AddComment(ctx context.Context, sourceID string, c model.NewComment) (commentID string, err error)
}

type PlanInput struct {
    Board model.BoardConfig; Scope string /* "sprint"|"board" */; ExtraFilter string
    ActiveSprints []model.Sprint; StatusLanes map[string]string /* status id → lane */; BacklogLimit int
}
type Query      struct { Name string /* "sprint"|"open"|"done"|"backlog" */; Role string /* "board"|"backlog" */; Limit int; Expr string /* provider query, opaque */; Count bool /* also fetch an approximate total */ }
type ScanItem   struct { SourceID, Key string; UpdatedAt time.Time; DevFingerprint string /* opaque; "" = unknown (jira-sync §2.11) */ }
type ScanResult struct { Items []ScanItem; ApproxTotal int /* -1 unknown; used for the backlog count */ }

type Factory func(conn model.Connection, secret string, opts Options) (Source, error)   // Options: HTTP client, logger, clock, UA, persisted meta
type URLFunc func(baseURL, key string) string                                          // issue URL without credentials
// source.Options also carries Full: the Source serves a full sync and must refresh cached reference data (Jira: field ids).
func Register(kind string, f Factory, u URLFunc)
func Open(conn model.Connection, secret string, opts Options) (Source, error)           // apperr not_found for an unknown kind
func IssueURLPrefix(kind, baseURL string) string                                       // view snapshots: URL = prefix + key
```

Request policy (concurrency, rate-limit handling, retries) is the adapter's responsibility; the orchestrator only bounds
how many `Hydrate`/`DevActivity` calls it issues at once. The Jira adapter is specified in `jira-sync.md`.

### 2.3 Outbound adapters (`internal/sink`, `internal/export`)

```go
type Capabilities struct {
    Current, Range bool      // can deliver a current snapshot / a historical range
    Formats        []string  // e.g. ["json"] (file v1); later "csv","markdown"
    NeedsSecret    bool
    Idempotent     bool      // honours Delivery.Key (webhook header, remote upsert); else at-least-once
}
type Field struct { Key, Label, Help string; Type string /* "text"|"path"|"url"|"select"|"bool"|"secret" */; Options []string; Required bool; Default string }

type Sink interface {
    Kind() string                                  // "file"
    DisplayName() string
    Capabilities() Capabilities
    ConfigSchema() []Field                         // drives the generic config form; stored as sink.config JSON
    Validate(config map[string]string) error       // apperr validation with Field set
    Deliver(ctx context.Context, d Delivery) (Receipt, error)
}
type Delivery struct { Key string /* idempotency key */; Attempt int; Config map[string]string; Secret string; Document export.Document; Raw []byte /* the frozen JSON */; StandupKey, Date string /* filename templates */ }
type Receipt  struct { Ref string /* path, URL, remote id */; Detail string }

func Register(s Sink)
func Get(kind string) (Sink, bool)
func Kinds() []Sink

// Dispatcher: enqueue (freeze payload in sink_outbox) → deliver → mark; see data-model.md §2.8 for retry rules.
type Dispatcher interface {
    Enqueue(ctx context.Context, sinkID, standupID int64, req Request, cause, key string) (outboxID int64, err error)
    Flush(ctx context.Context, sinkID int64) error            // attempt pending/failed rows for a sink (explicit triggers only)
    Retry(ctx context.Context, outboxID int64) error          // manual Retry (also dead rows)
    AfterSync(ctx context.Context, standupID, runID int64) error // enqueue for after_sync sinks covering the standup
}
type Request struct { Kind string /* "current"|"range" */; From, To string /* local dates, range only */ }
```

`sink/file` (v1 reference): config `{dir (path, required), filename (text, default "cuisle-{standupKey}-{date}.json"),
overwrite (bool, default true)}`; writes atomically (temp file + rename); `Receipt.Ref` = absolute path.
Planned kinds (not v1): `webhook` (POST JSON, `Idempotency-Key` header, optional bearer secret), `file` formats CSV and
Markdown standup notes, `linear`, `github-projects`, `asana`, `notion`, `azure-boards`.

### 2.4 Rich text and media (`internal/adf`, `internal/media`) — ADR 0015

Raw ADF is stored as received; it is parsed **at view-build time** (so renderer fixes apply to already-synced data).
The frontend never sees raw ADF and never uses `innerHTML`.

```go
// internal/adf
type Doc  struct { Nodes []Node `json:"c"`; Unsupported int `json:"u,omitempty"` }
type Node struct {
    T     string  `json:"t"`             // normalised type: p, h, text, br, ul, ol, li, task, taskItem, decision, quote, rule, code,
                                         // panel, table, tr, th, td, expand, status, mention, date, emoji, card, media, file, unknown
    Text  string  `json:"x,omitempty"`
    Marks []Mark  `json:"m,omitempty"`   // {T: strong|em|strike|underline|sub|sup|code|link|color; V: href or #rrggbb}
    A     *Attrs  `json:"a,omitempty"`
    C     []Node  `json:"c,omitempty"`
}
type Attrs struct {   // flat, all optional; only the fields relevant to T are set
    Level int; Lang, Panel /* info|note|success|warning|error */, Title string; Done, Collapsed bool
    Order int; Colspan, Rowspan int; Colwidth []int; Header bool
    Lozenge /* todo|prog|review|qa|done|alert */, Label string
    AccountID string; Self bool                          // mention (Self = the connected user)
    Date string; Past bool                               // date node, local 'YYYY-MM-DD'; Past relative to asOf
    Emoji, ShortName string
    URL, Host, Path string                               // card to a non-synced URL
    IssueID int64; Key, Summary, TypeKind, StatusName, StatusLozenge string   // card resolved to a synced issue
    AttachmentID string; Name, Mime string; Bytes int64; Width, Height int; Layout, Caption string
    Cached string /* "full"|"thumb"|"none" */; Missing bool                   // media / file
    Block bool                                           // blockCard, mediaSingle vs inline
}
type Resolver interface {    // implemented in view over the loaded ticket
    Attachment(mediaFileID, alt string) (model.Attachment, cached string, ok bool)
    Issue(url string) (ref ResolvedIssue, ok bool)       // synced issue for an inline/block card URL or key
    Person(accountID string) (name string, self bool)
    Now() time.Time
}
func Parse(raw model.RichText, r Resolver) Doc           // never fails: malformed input → one `unknown` node with the plain text
func PlainText(raw model.RichText) string                // used for aria labels and tests
func FromPlainText(c model.NewComment) model.RichText    // composer text + mentions → ADF (paragraphs, hardBreaks, mention nodes)

// internal/media
type Cache interface {
    Put(ctx context.Context, att model.Attachment, connectionID int64, r io.Reader) (Entry, error) // stores original, derives thumb
    PutThumb(ctx context.Context, att model.Attachment, connectionID int64, r io.Reader) error      // provider-made thumb (fallback)
    Open(attachmentRowID int64, thumb bool) (f *os.File, mime string, err error)                     // not_found when absent; never fetches
    Touch(attachmentRowID int64)                                // last_viewed_at = now (LRU)
    Evict(ctx context.Context) (freedBytes int64, err error)    // originals only, least recently viewed first, until under the cap
    Clear(ctx context.Context) error                            // "Clear image cache": removes originals AND thumbs
    Stats(ctx context.Context) (Stats, error)                   // {Bytes, ThumbBytes, Files, CapBytes}
}
```

- **Unknown nodes** become `{T:"unknown", Text:<concatenated text content>}` and increment `Doc.Unsupported`; unknown marks
  are dropped. Node/mark coverage = the table in ux-spec §14.4 (golden-tested per node).
- **Media rules**: images are `image/png|jpeg|gif|webp|bmp` up to 25 MB; everything else (incl. SVG, HEIC, PDF, video) is a
  file card and is never rendered inline. Layout on disk: `<data dir>/media/<connection_id>/<id mod 256>/<attachment row id>`
  (`.orig`, `.thumb.jpg`). Thumbs are 320×200 max (2× the 160×100 tile), JPEG q80, always kept; originals count against
  the cap (`app_setting` `media.cache_max_mb`, default 1024) and are evicted least-recently-viewed after each sync's media
  phase and after each `Put`. Decoding is bounded (max 60 MP) to avoid decompression bombs; failures → file card.
- **Serving**: asset middleware routes `GET /media/{attachmentRowId}` and `GET /media/{attachmentRowId}/thumb` read from
  the cache only (`Content-Type` from the stored mime, `X-Content-Type-Options: nosniff`, `Cache-Control: private,
  max-age=31536000, immutable`); a miss is `404` and **never triggers a fetch**. The UI then shows the thumb (or a
  placeholder frame) and fetches the original only through `TicketService.EnsureMedia` when the user opens the lightbox,
  or `TicketService.OpenAttachment` for files.

## 3. Wails v3 surface

### 3.1 Application wiring (illustrative)

```go
app := application.New(application.Options{
    Name: "Cuisle",
    Services: []application.Service{
        application.NewService(appSvc), application.NewService(standupSvc),
        application.NewService(configSvc), application.NewService(connectionSvc), application.NewService(syncSvc),
        application.NewService(ticketSvc), application.NewService(sinkSvc), application.NewService(historySvc),
    },
    Assets: application.AssetOptions{
        Handler:    application.AssetFileServerFS(distFS),
        Middleware: chain(bootMiddleware(viewBuilder), mediaMiddleware(mediaCache)),  // GET /boot.js (§5.1); GET /media/{id}[/thumb] (§2.4)
    },
    MarshalError:   apperr.Marshal,                   // typed errors → JSON (see §3.4)
    SingleInstance: &application.SingleInstanceOptions{UniqueID: "crueber.cuisle"},
})
```

Window: 1 `WebviewWindow`, min size 1024×640, initial size/position restored from `ui_state`, `BackgroundColour`
set from the persisted theme before the webview loads (no white flash). Devtools enabled only in dev builds.

### 3.2 Bound methods (exact Go signatures)

All methods take `ctx context.Context` first (Wails v3 injects it and cancels it when the JS promise is cancelled);
it is omitted from the generated TS signature. All return `error` last. Lists are never `nil` (empty slices).

```go
// ── AppService ──────────────────────────────────────────────────────────────
func (s *AppService) Bootstrap(ctx context.Context) (Bootstrap, error)
func (s *AppService) SetAppSetting(ctx context.Context, key string, value string) (SaveAck, error)
func (s *AppService) SetUIState(ctx context.Context, patch UIStatePatch) error
func (s *AppService) OpenIssue(ctx context.Context, issueKey string) error      // opens source.IssueURL(key) in the OS browser
func (s *AppService) OpenURL(ctx context.Context, url string) error             // http/https (with a host) and mailto only, ≤ 4096 chars; links inside the ticket modal (F12)
func (s *AppService) Diagnostics(ctx context.Context) (Diagnostics, error)      // version, paths, db size, last runs
func (s *AppService) ReportPerf(ctx context.Context, marks map[string]float64) error // dev/bench builds only
func (s *AppService) LogClientError(ctx context.Context, message string, stack string) error // rate-limited 20/min

// ── StandupService (read side for the Standup screen) ───────────────────────
func (s *StandupService) List(ctx context.Context) ([]StandupTab, error)
func (s *StandupService) View(ctx context.Context, standupID int64) (StandupView, error)   // = view.Build(cache.LoadCurrent(id), now)

// ── ConfigService (every Configure mutation; all autosave) ──────────────────
func (s *ConfigService) Get(ctx context.Context, standupID int64) (StandupConfig, error)
func (s *ConfigService) Create(ctx context.Context) (StandupConfig, error)                    // "New standup": inserts a draft row
func (s *ConfigService) Duplicate(ctx context.Context, standupID int64) (StandupConfig, error)   // named "<name> (copy)"
func (s *ConfigService) CopySettingsFrom(ctx context.Context, standupID int64, fromStandupID int64) (StandupConfig, error) // "Start from": copies settings, lanes, limits; not source/speakers
func (s *ConfigService) DiscardDraft(ctx context.Context, standupID int64) error             // hard-deletes a draft with no space chosen; else no-op
func (s *ConfigService) Delete(ctx context.Context, standupID int64) (SaveAck, error)        // soft delete (undoable)
func (s *ConfigService) Restore(ctx context.Context, standupID int64) (SaveAck, error)       // undo of Delete
func (s *ConfigService) Move(ctx context.Context, standupID int64, toPosition int) (SaveAck, error)
func (s *ConfigService) Update(ctx context.Context, standupID int64, patch StandupPatch) (SaveAck, error)
func (s *ConfigService) SetSpace(ctx context.Context, standupID int64, spaceID string) (StandupConfig, error) // clears board+mapping
func (s *ConfigService) SetBoard(ctx context.Context, standupID int64, boardID string) (StandupConfig, error)    // fetches columns, auto-maps
func (s *ConfigService) SetColumnLane(ctx context.Context, standupID int64, columnName string, lane string) (SaveAck, error)
func (s *ConfigService) MoveColumn(ctx context.Context, standupID int64, columnName string, toIndex int) (SaveAck, error) // order within its lane
func (s *ConfigService) SetWalkOrder(ctx context.Context, standupID int64, lanes []string) (SaveAck, error)     // permutation of todo,prog,review,qa
func (s *ConfigService) SetLaneLimit(ctx context.Context, standupID int64, lane string, days int) (SaveAck, error) // 0 = no limit
func (s *ConfigService) SetSpeakerIncluded(ctx context.Context, standupID int64, accountID string, included bool) (SaveAck, error)
func (s *ConfigService) MoveSpeaker(ctx context.Context, standupID int64, accountID string, toPosition int) (SaveAck, error)
func (s *ConfigService) SortSpeakers(ctx context.Context, standupID int64, mode string) ([]SpeakerConfig, error) // "shuffle"|"az"|"active"
func (s *ConfigService) AddSpeaker(ctx context.Context, standupID int64, accountID string) ([]SpeakerConfig, error)
func (s *ConfigService) RemoveSpeaker(ctx context.Context, standupID int64, accountID string) (SaveAck, error)   // anyone; sync won't re-add

// ── ConnectionService (connection + autocomplete sources) ─────────────────────────
func (s *ConnectionService) Connection(ctx context.Context) (Connection, error)
func (s *ConnectionService) Connect(ctx context.Context, in ConnectInput) (Connection, error)   // ConnectInput{Source "jira", BaseURL, Account, Secret}
func (s *ConnectionService) Disconnect(ctx context.Context, deleteContent bool) error              // deletes keychain item; synced data kept unless deleteContent (UI offers it only past storage.cleanup_offer_mb, ux-spec §14.11); record/history rows always kept
func (s *ConnectionService) SearchSpaces(ctx context.Context, query string) ([]SpaceOption, error)
func (s *ConnectionService) SearchBoards(ctx context.Context, spaceKey string, query string) ([]BoardOption, error)
func (s *ConnectionService) SearchUsers(ctx context.Context, standupID int64, query string) ([]UserOption, error)
func (s *ConnectionService) ValidateFilter(ctx context.Context, standupID int64, expr string) (FilterCheck, error)

// ── SyncService ─────────────────────────────────────────────────────────────
func (s *SyncService) Start(ctx context.Context, standupID int64, mode string) (int64, error) // mode "incremental"|"full"; returns run id; no-op (same id) if already running
func (s *SyncService) Cancel(ctx context.Context, standupID int64) error
func (s *SyncService) Status(ctx context.Context) ([]SyncStatus, error)

// ── TicketService (ticket modal; ADR 0015, ux-spec §14) ─────────────────────
func (s *TicketService) Get(ctx context.Context, standupID int64, issueID int64) (TicketDetail, error)   // local only, zero network; = view.BuildTicket(...)
func (s *TicketService) Transitions(ctx context.Context, issueID int64) (TransitionList, error)          // NETWORK (Status combobox opened); cached per issue; offline → cached list with Offline=true
func (s *TicketService) Transition(ctx context.Context, standupID, issueID int64, transitionID string) (TicketDetail, error) // write → refresh → apply
func (s *TicketService) Assign(ctx context.Context, standupID, issueID int64, accountID string) (TicketDetail, error)        // "" = unassign
func (s *TicketService) SetFlag(ctx context.Context, standupID, issueID int64, flagged bool) (TicketDetail, error)
func (s *TicketService) AddComment(ctx context.Context, standupID, issueID int64, in CommentInput) (TicketDetail, error)     // on success the draft is deleted
func (s *TicketService) Refresh(ctx context.Context, standupID, issueID int64) (TicketDetail, error)   // NETWORK (Refresh button / R): GetIssue + one-issue apply; 10 s per-issue cooldown → error code cooldown{retryInMs}; interactive lane of the limiter
func (s *TicketService) Draft(ctx context.Context, issueID int64) (CommentInput, error)
func (s *TicketService) SaveDraft(ctx context.Context, issueID int64, in CommentInput) (SaveAck, error)  // empty text deletes the draft; autosave pipeline (§5.3), 400 ms debounce
func (s *TicketService) EnsureMedia(ctx context.Context, attachmentID int64) (AttachmentView, error)     // NETWORK on cache miss (lightbox / "load image" click); touches LRU
func (s *TicketService) OpenAttachment(ctx context.Context, attachmentID int64) error                    // NETWORK on miss; writes to the OS temp dir and opens with the default app
func (s *TicketService) SearchMentions(ctx context.Context, standupID int64, query string) ([]UserOption, error) // "@" in the composer; = ConnectionService.SearchUsers, local people first

// Ticket field preferences (per connection; Configure › Ticket fields, ux-spec §14.8) and media cache — on ConnectionService / AppService
func (s *ConnectionService) TicketFields(ctx context.Context) (TicketFieldPrefs, error)                  // {Pinned []FieldPref, Hidden []FieldPref, Available []FieldOption}
func (s *ConnectionService) PinTicketField(ctx context.Context, fieldID string) (TicketFieldPrefs, error)
func (s *ConnectionService) UnpinTicketField(ctx context.Context, fieldID string) (TicketFieldPrefs, error)
func (s *ConnectionService) MoveTicketField(ctx context.Context, fieldID string, toPosition int) (SaveAck, error)
func (s *ConnectionService) SetTicketFieldHidden(ctx context.Context, fieldID string, hidden bool) (TicketFieldPrefs, error)
func (s *AppService) MediaCache(ctx context.Context) (MediaCacheInfo, error)                             // {Bytes, ThumbBytes, Files, CapMB}; cap is app_setting media.cache_max_mb
func (s *AppService) ClearMediaCache(ctx context.Context) (MediaCacheInfo, error)

// ── SinkService (outbound adapters; v1 = file sink only) ─────────────────────
func (s *SinkService) Kinds(ctx context.Context) ([]SinkKind, error)                          // registered adapters + config schema + capabilities
func (s *SinkService) List(ctx context.Context) ([]SinkConfig, error)
func (s *SinkService) Save(ctx context.Context, in SinkInput) (SinkConfig, error)             // create (ID 0) or update; validates against the schema; secret → keychain
func (s *SinkService) Delete(ctx context.Context, sinkID int64) error
func (s *SinkService) Send(ctx context.Context, sinkID int64, standupID int64, req SendRequest) (int64, error) // returns outbox id; v1: req.Kind "current" only
func (s *SinkService) Outbox(ctx context.Context, sinkID int64, limit int) ([]OutboxItem, error)
func (s *SinkService) Retry(ctx context.Context, outboxID int64) error

// ── HistoryService (PLACEHOLDER: registered and bound, no v1 UI; returns `not_implemented` until the history milestone) ──
func (s *HistoryService) Range(ctx context.Context, standupID int64) (HistoryRange, error)            // {From, To "YYYY-MM-DD"} from standup_sync_state.history_from
func (s *HistoryService) ViewAsOf(ctx context.Context, standupID int64, date string) (StandupView, error) // = view.Build(history.LoadAsOf(id, endOfLocalDay(date)), same)
```

```go
type SinkKind    struct { Kind, DisplayName string; Capabilities sink.Capabilities; Schema []sink.Field }
type SinkConfig  struct { ID int64; Kind, Name string; StandupID int64 /* 0 = all */; Config map[string]string; HasSecret bool; SendOn string /* "manual"|"after_sync" */; Enabled bool; LastDelivery *OutboxItem }
type SinkInput   struct { ID int64; Kind, Name string; StandupID int64; Config map[string]string; Secret *string /* nil = unchanged */; SendOn string; Enabled bool }
type SendRequest struct { Kind string /* "current"|"range" */; From, To string }
type OutboxItem  struct { ID, SinkID, StandupID int64; Cause, PayloadKind, Status string; Attempts int; ErrorCode, Error, Receipt string; CreatedAt, DeliveredAt int64 }
type HistoryRange struct { From, To string }
```

Ticket types (the row type inside `StandupView` keeps the name `TicketView`; the modal payload is `TicketDetail`):

```go
type TicketDetail struct {
    IssueID int64; StandupID int64; AsOf, SyncedAt, UpdatedAt int64
    Row        TicketView            // identical to the board row (key, summary, type, lane, days, blocked, pulse) — the modal header reuses it
    URL        string
    Parent     *IssueRefView         // {IssueID (0 if not synced), Key, Summary, TypeKind}
    PulseDays  int; Axis []AxisDay; LimitDays, BlockedDays int
    Description *adf.Doc             // nil when empty
    Attachments []AttachmentView     // images first, then files, each by created desc
    Subtasks   []IssueRow; SubtasksDone int
    LinkGroups []LinkGroup           // {Label "IS BLOCKED BY", Rows []IssueRow}
    Comments   []CommentView         // oldest first; the frontend orders per app_setting ticket.comment_order
    NewSince   int64                 // start of the previous working day (D3) — comments at/after it are "new since standup"; 0 = none
    Draft      CommentInput
    Rail       []RailGroup           // state, details, custom, development, dates — in that order; empty groups omitted
    Dev        *DevSummary           // {Branch string, Commits int, LastCommitAt int64, PRs []{ID, Title, Status}}; nil if none/unavailable
    Can        TicketCaps            // {Transition, Assign, Flag, Comment bool; Reason string /* "offline"|"auth"|"read_only"|"" */}
    Warnings   []string              // e.g. "media_unresolved"
}
type IssueRow      struct { IssueID int64 /* 0 = not synced → opens the browser */; Key, Summary, TypeKind, StatusName, StatusLozenge, AssigneeInitials, URL string; Done bool }
type AttachmentView struct { ID int64; Name, Ext, Mime string; Bytes int64; AuthorInitials, AuthorName string; CreatedAt int64
                             Image bool; Width, Height int; Cached string /* "full"|"thumb"|"none" */; URL, ThumbURL string /* /media/{id}, /media/{id}/thumb */ }
type CommentView   struct { ID string; Author PersonRef /* {AccountID, Name, Initials, Self} */; CreatedAt, UpdatedAt int64; Edited bool
                            Visibility string /* "" or e.g. "Developers only" */; Body adf.Doc }
type CommentInput  struct { Text string; Mentions []Mention /* {Start, End int; AccountID, Name string} */ }
type RailGroup     struct { ID, Label string; Rows []RailRow; NotSet []string /* labels of empty fields, in rail order */ }
type RailRow       struct { FieldID, Label, Kind string; Edit string /* ""|"status"|"assignee"|"flag" */
                            Text string; Number *float64; Items []string; Users []PersonRef; Rich *adf.Doc; Raw string
                            Mono bool; Sub string /* e.g. "ends 1 Dec" */; Progress *float64 /* time tracking 0..1 */; URL, URLLabel string }
type TransitionList struct { Items []TransitionOption; FetchedAt int64; Offline bool }
type TransitionOption struct { ID, Name, ToStatusName, ToLane string; NeedsScreen bool; URL string /* issue URL for "↗ Jira" */ }
```

Rules for `view.BuildTicket(snap model.BoardSnapshot, t cache.Ticket, prefs TicketFieldPrefs, asOf time.Time) TicketDetail`:
- `cache.LoadTicket(issueID)` returns the issue, its `IssueContent`, comments, attachments (+ cache state), link/subtask
  targets resolved against local issues, and dev events — **one reader transaction, no network** (`data-model.md` §4.1 Q9).
- **Rail folding is done in Go**: a field with no value never produces a row; its label goes to the group's `NotSet`
  (ux-spec §14.6). Exception: Assignee always has a row. Group membership: *state* = status, assignee, flag; *details* =
  reporter, priority, parent, sprint, story points, due, labels, components, fix versions, time tracking, environment;
  *custom* = custom fields per prefs (pinned in order, then the remaining non-hidden non-empty ones A–Z); *development*
  from dev events; *dates* = created, updated, resolved, watchers.
- An issue that is not a member of `standupID` (reached through an inline card) is built with that standup's lane mapping
  and an empty pulse if it has no history there.
- `Can.*` = source capability ∧ connection state `ok`; the frontend additionally disables writes while offline.

Light writes (all four share this path, in `internal/app` + `internal/sync`):
1. Per-issue mutex; reject with `conflict` if a write for the same issue is in flight.
2. Call the source write (`Transition`/`Assign`/`SetFlag`/`AddComment`). Failure → typed error (§3.4), **no retry**, emit
   `ticket:write_failed`; nothing is stored.
3. Success → `source.GetIssue` → `sync.ApplyIssue` (the same upsert code as a sync apply, one transaction: issue row,
   change tables — so the status/assignee/flag change is **historised from the provider's changelog**, comments, content;
   `standup.rev` bumped for every standup containing the issue) → return the rebuilt `TicketDetail`; emit `ticket:updated`
   and `data:changed {what:"view"}`. If the refresh fails after a successful write, return `partial`-style success with
   warning `refresh_failed` and apply the known delta locally (status/assignee/flag) so the UI does not lie; the next sync heals it.
4. A sync apply never overwrites newer data: an incoming issue whose `updated_at` is older than the stored row is skipped.

Principal types (Go; TS types are generated from these — field names are the JSON names, lowerCamel via tags).
**Where they live (frozen in Wave 0):** the view-model types (`StandupView`, `StandupTab`, `StandupConfig`, `SyncStatus`,
`SpaceOption`, `BoardOption`, `FilterCheck`, …) are in `internal/view/contract.go`, because `view` builds them and
nothing may import `app`. `internal/app/types.go` holds the service I/O types and aliases `StandupPatch`,
`UIStatePatch` and `UIState` to `internal/settings`. Additions over the sketch below: `TicketView.SourceID`,
`StandupView.LastSyncedAt`, `ViewOptions.DevActivity`, `Bootstrap.Recovered`; Pulse segment/mark/span types alias
`internal/pulse`. Event payload structs live in `internal/sync` (sync events) and `internal/app` (sink and day events).

```go
type SaveAck struct { Rev int64 `json:"rev"`; SavedAt int64 `json:"savedAt"`; NeedsSync bool `json:"needsSync"` }

type Bootstrap struct {
    Build      BuildInfo         `json:"build"`
    Settings   map[string]string `json:"settings"`      // app_setting rows (keys in §6.4)
    Connection Connection        `json:"connection"`
    Standups   []StandupTab      `json:"standups"`      // tab order
    UI         UIState           `json:"ui"`            // lastStandupId, lastView, lastPerson per standup
    View       *StandupView      `json:"view"`          // view for ui.lastStandupId, nil when there are no standups
    Sync       []SyncStatus      `json:"sync"`
}
type StandupTab struct { ID int64; Rev int64; Name, SpaceKey string; Position int; ActiveCount, SpeakerCount int
                         Configured bool /* board chosen; false = DRAFT (shown only in Configure rail, never as a header tab or shortcut) */; NeedsSync bool; LastSyncedAt int64 }
type Connection struct { State string /* "ok"|"invalid"|"missing"|"unknown" */; Source /* "jira" */, BaseURL, Account, DisplayName string
                         DevActivity string /* "ok"|"unavailable"|"unknown" */; Capabilities source.Capabilities }

type StandupView struct {
    StandupID int64; Rev int64; GeneratedAt int64; Today string       // "2026-11-23" local date of AsOf
    AsOf int64; Live bool                                              // Live=false for a historical view (HistoryService.ViewAsOf)
    Name, SpaceKey, BoardName, BoardType string                      // boardType "scrum"|"kanban"
    Scope      string        // "sprint"|"board"
    Sprint     *SprintInfo   // {ID, Name, StartAt, EndAt, DaysLeft, EndsLabel "ends today"|"ends tomorrow"|"ends in 3 days"|"ended 2 days ago"}; nil for kanban / no active sprint
    PulseDays  int           // 7|14|21|28
    Axis       []AxisDay     // len == PulseDays: {Date "YYYY-MM-DD", Label "23", Weekend, Monday bool}
    Lanes      []LaneInfo    // walk order, then done: {Lane, Label, LimitDays, Columns []string /* Jira column names mapped to it */}
    Speakers   []SpeakerView // effective speaking order: included speakers (Unassigned placed per setting), then people
                             // with tickets who are excluded (Included=false: board lanes only, not in rail or ←/→ walk)
    Tickets    []TicketView  // every active ticket + every Done ticket inside the done window, board-rank order
    Backlog    []BacklogItem // top N by rank: {Key, Summary, Type, URL}
    BacklogTotal int         // approximate count of the whole backlog (-1 unknown)
    Counts     Counts        // {Active, Unassigned, Done, Backlog}
    Options    ViewOptions   // {RowsMode "fit"|"roomy", ShowCommits, ShowPRs, ShowComments, ShadeWeekends bool, DoneWindow "sprint"|"14d"|"yesterday", DoneLabel "this sprint"|"14 days"|"since Friday" (weekday of the bound)}
    Sync       SyncStatus
    Warnings   []string      // e.g. "dev_activity_unavailable", "unmapped_statuses"
    PreviewKey string        // ticket key Configure › Pulse uses for its live preview (most marks, blocked preferred); "" if none
}
type SpeakerView struct { ID string /* accountId or "unassigned" */; Name, First, Initials string; Unassigned bool
                          Included bool /* false: excluded speaker, or Unassigned with position "skip" */; Active, Done int }
// The "unassigned" entry is present whenever unassigned tickets exist (the board always pins that lane first).
type TicketView struct {
    ID int64; Key, Summary, URL string
    Type       string  // "story"|"bug"|"task"|"spike"|"subtask"|"epic"|"other" (subtask/epic use the "other" icon, ux-spec §5.4)
    Assignee   string  // accountId, "" = unassigned
    Lane       string  // todo|prog|review|qa|done
    StatusName string
    DaysInLane int; OverLimit bool; Blocked bool
    DoneAt     int64   // 0 unless lane == done
    Pulse      Pulse
}
type Pulse struct {
    Segs    []PulseSeg  // {Lane string, From, To int}  day indexes, 0 ≤ From < To ≤ PulseDays, contiguous, oldest first
    Marks   []PulseMark // sparse, only days with activity: {Day, Commits, PRs, Comments int}
    Blocked []PulseSpan // {From, To int} day indexes
    Label   string      // aria-label for the track (ux-spec §7), composed in Go: jira-sync.md §5.8
}

type StandupConfig struct {   // everything the Configure form shows for one standup
    ID int64; Rev int64; Position int; Name string
    Space *SpaceOption; Board *BoardOption; Scope, ExtraFilter string; FilterCheck *FilterCheck
    Columns  []ColumnMapping  // {ColumnName string, StatusNames []string, Lane string, Auto bool}
    Lanes    []LaneInfo       // walk order + limits + mapped Jira column names
    UnmappedStatuses []string // names of statuses in the board filter's columns mapped to no lane (warning line)
    RowsMode string
    Speakers []SpeakerConfig  // {AccountID, Name, Initials string, Position int, Included, Manual bool, Active int, Note string}
    UnassignedPosition string // "first"|"last"|"skip"
    ReshuffleDaily bool
    PulseDays int; ShowCommits, ShowPRs, ShowComments, ShadeWeekends bool
    DoneWindow string; BacklogRows int
    NeedsSync bool
    Draft     bool            // no board chosen yet (DRAFT tag)
}
type StandupPatch struct {    // nil pointer = leave unchanged; one call may set several fields
    Name *string; Scope *string; ExtraFilter *string; RowsMode *string
    UnassignedPosition *string; ReshuffleDaily *bool
    PulseDays *int; ShowCommits, ShowPRs, ShowComments, ShadeWeekends *bool
    DoneWindow *string; BacklogRows *int
}
type UIStatePatch struct { LastStandupID *int64; LastView *string; StandupID *int64; LastPerson *string; Window *WindowState }
type SpaceOption struct { ID, Key, Name string }
type BoardOption struct { ID string; Name, Type string }
type UserOption  struct { AccountID, DisplayName, Initials string; OnBoard bool }
type FilterCheck    struct { OK bool; Errors []string; CheckedAt int64 }
type SyncStatus  struct { StandupID, RunID int64; State string /* "idle"|"running"|"failed" */; Mode, Phase string; Done, Total int
                          LastSuccessAt, LastAttemptAt int64; LastStatus string /* "ok"|"partial"|"cancelled"|"failed"|"" */
                          Warnings []string /* of the last finished run */; ErrorCode, ErrorMessage string; NeedsSync bool }
```

### 3.3 Events (Go → JS)

Emitted through the Wails event bus; JS subscribes via `Events.On(name, cb)` in `frontend/src/api/events.ts`.

| Event | Payload | When |
|---|---|---|
| `sync:started` | `{runId, standupId, mode, trigger}` (`trigger`: `"manual"\|"open"\|"config"`) | run accepted |
| `sync:progress` | `{runId, standupId, phase, done, total}`; `phase` ∈ `config\|scan\|issues\|changelog\|content\|dev\|apply\|media` (`media` runs after `sync:finished`) | ≤ 10/s, coalesced |
| `sync:finished` | `{runId, standupId, status, errorCode?, errorMessage?, warnings[], finishedAt, stats:{issues, requests, ms}}`; `status` ∈ `ok\|partial\|cancelled\|failed` | after the apply transaction commits (or run aborts) |
| `data:changed` | `{standupId, what}`; `what` ∈ `view\|config\|standups` (`standupId` 0 = all) | after any commit that changes what a screen shows and was not caused by the receiving call itself (sync apply, speaker auto-discovery, restore) |
| `connection:auth` | `{state}` ∈ `ok\|invalid\|missing` | on Connect/Disconnect and on any 401 |
| `ticket:updated` | `{issueId, key, standupIds[], cause, changed[]}`; `cause` ∈ `write\|sync`; `changed` = names of changed parts (`status`, `assignee`, `flag`, `comments`, `description`, `fields`, `attachments`) | after a light write's refresh is applied, and after a sync apply for every hydrated issue (the frontend acts only if that issue's modal is open: shows "Updated in Jira · Reload", ux-spec §14.9) |
| `ticket:write_failed` | `{issueId, key, action, errorCode, errorMessage}`; `action` ∈ `transition\|assign\|flag\|comment` | a light write was rejected or could not be sent (also returned as the call's error) |
| `media:ready` | `{attachmentId, issueId, cached}` | an image landed in the cache after the record apply (lazy media phase, or `EnsureMedia`) |
| `sink:delivered` | `{outboxId, sinkId, standupId, kind, ref}` | a delivery succeeded |
| `sink:failed` | `{outboxId, sinkId, standupId, kind, errorCode, errorMessage, attempts, willRetry}` (`willRetry` = will be retried on the next explicit trigger) | a delivery job gave up for now |
| `day:rolled` | `{today}` | local midnight (single timer); UI re-requests the current view |

JS → Go uses bound methods only (no custom JS-emitted events).

### 3.4 Error model

- Domain code returns `*apperr.Error` (wrapping causes with `%w`):

```go
type Error struct {
    Code      Code   `json:"code"`
    Message   string `json:"message"`   // safe, user-presentable, one sentence
    Detail    string `json:"detail"`    // technical; shown in "Details", written to log
    Retryable bool   `json:"retryable"`
    Field     string `json:"field"`     // for validation errors: the patch field name
}
```

- Codes: `validation`, `not_found`, `conflict`, `auth_missing`, `auth_invalid`, `forbidden`, `offline`, `timeout`,
  `rate_limited`, `source_http`, `filter_invalid`, `keychain_unavailable`, `sink_failed`, `unsupported` (capability absent),
  `write_rejected` (provider refused the change; `Message` carries the provider's reason), `transition_needs_screen`,
  `permission_denied` (403 on a write), `media_unavailable`,
  `not_implemented`, `db`, `cancelled`, `internal`.
- `application.Options.MarshalError` (`func(error) []byte`; nil falls back to Wails' default) serialises it to JSON.
  In JS the rejected promise is a `RuntimeError` whose `.cause` is that JSON object (verified in the M0 spike); a
  cancelled call rejects with `CancelError`. The frontend facade (`api/errors.ts`) converts every rejection into
  `AppError {code, message, detail, retryable, field}` (`CancelError` → `cancelled`); anything unparsable becomes
  `code: "internal"`.
- Wails logs every bound-method error at ERR with `err.Error()`. `apperr.Error.Error()` therefore returns only
  `code: message`, never `Detail`, and messages never contain secrets or raw HTTP bodies.
- Panics in bound methods are recovered in a service wrapper, logged with stack, returned as `internal`.
- Non-fatal sync problems (dev-status unavailable, one issue's comments failed) are **warnings** on the run
  (`sync:finished.warnings`, `StandupView.Warnings`), never errors.

### 3.5 Export payload — `cuisle.export.v1`

Built by `internal/export` from one or more `StandupView`s; this is what every sink receives and what `sink_outbox.payload`
stores. Stability contract: within `v1`, fields are only ever **added**; removing/renaming or changing meaning requires
`cuisle.export.v2` (sinks declare which versions they accept). Times are RFC 3339 strings, dates `YYYY-MM-DD` in the
snapshot's timezone, day spans are **dates** (not day indexes) so consumers need no window arithmetic. No secrets, no
comment bodies, no e-mail addresses.

```jsonc
{
  "schema": "cuisle.export.v1",
  "generatedAt": "2026-11-23T15:04:05Z",
  "generator": { "name": "Cuisle", "version": "1.0.0" },
  "kind": "current",                         // "current" = one snapshot; "range" = one snapshot per local day
  "source": { "kind": "jira", "baseUrl": "https://acme.atlassian.net" },
  "standup": { "id": 1, "name": "Saffron Mobile",
               "space": { "key": "MOB", "name": "Saffron Mobile" },
               "board": { "id": "12", "name": "MOB board", "kind": "scrum" }, "scope": "sprint" },
  "snapshots": [{
    "asOf": "2026-11-23T15:04:05Z", "date": "2026-11-23", "timezone": "America/Chicago", "live": true,
    "syncedAt": "2026-11-23T15:02:10Z",
    "sprint": { "id": "42", "name": "Sprint 42", "startAt": "…", "endAt": "…" },          // null for kanban
    "pulseDays": 21, "window": { "from": "2026-11-03", "to": "2026-11-23" },
    "lanes": [{ "lane": "qa", "label": "QA", "limitDays": 3 }],                            // walk order, then done
    "people": [{ "id": "5b10…", "name": "Iris Calloway", "included": true, "active": 14, "done": 6 }],  // speaking order; id "unassigned" for the pseudo-speaker
    "tickets": [{
      "key": "MOB-1429", "sourceId": "10429", "url": "https://acme.atlassian.net/browse/MOB-1429",
      "summary": "Share a recipe as a link with a preview image", "type": "bug",
      "assignee": "5b10…",                   // people[].id or null
      "lane": "prog", "status": "In Progress", "daysInLane": 6, "overLimit": true, "blocked": true, "doneAt": null,
      "pulse": {
        "segments": [{ "lane": "todo", "from": "2026-11-10", "to": "2026-11-17" }, { "lane": "prog", "from": "2026-11-18", "to": "2026-11-23" }],
        "marks":    [{ "date": "2026-11-12", "commits": 2, "prs": 0, "comments": 1 }],
        "blocked":  [{ "from": "2026-11-21", "to": "2026-11-23" }]
      }
    }],
    "backlog": [{ "key": "MOB-1525", "summary": "…", "type": "story", "url": "…" }],
    "counts": { "active": 40, "unassigned": 4, "done": 15, "backlog": 12, "backlogTotal": 40 }
  }]
}
```

Segment/blocked `from`/`to` are inclusive dates. `kind: "range"` requires `internal/history` and is therefore not
available in v1 (`SinkService.Send` returns `not_implemented`).

## 4. Frontend architecture

### 4.1 Stack: Preact + @preact/signals + TypeScript + Vite

| Option | Runtime gz | Why not / why |
|---|---|---|
| **Preact 10 + @preact/signals** (chosen) | ≈ 4.5 KB + ≈ 2.5 KB | JSX with full TS checking of props; signals update bound text/attributes without re-rendering components, which is exactly what speaker/standup switching needs; component model makes the combobox, steppers and lists straightforward; no compiler beyond esbuild/Vite's JSX transform. |
| lit-html + signals-core | ≈ 3.5 KB + ≈ 1.7 KB | Slightly smaller, but no component/lifecycle model: focus management, the combobox and keyed list reordering must be hand-rolled; template strings are not type-checked. Saves ~2 KB, costs correctness. |
| Vanilla TS + tiny reactive helper | ≈ 1–2 KB | Smallest, but every list diff, focus restore and cleanup is bespoke; highest defect risk for the Configure form. |
| Svelte 5 | ≈ 8–15 KB and grows per component | Excellent ergonomics, but compiled output grows with ~40 components, adds a compiler to the toolchain, and gains nothing measurable here over signals. |

No router library, no CSS framework, no component library, no state library beyond signals, no date library (`Intl` only).
Runtime dependencies are exactly: `preact`, `@preact/signals`, `@wailsio/runtime`.

### 4.2 Folder layout

```
frontend/
├── index.html                 <script src="/boot.js"></script> before the module entry
├── package.json  vite.config.ts  tsconfig.json  .size-limit.json
├── scripts/tokens.mjs         design/tokens.json → src/styles/tokens.css (run by `task tokens`, also in `vite build`)
├── src/
│   ├── main.tsx               mount, read window.__CUISLE_BOOT__ (fallback: AppService.Bootstrap())
│   ├── bindings/              GENERATED by `task bindings` (committed; CI fails on drift; never imported outside api/)
│   ├── api/
│   │   ├── index.ts           typed facade re-exporting services + view-model types; wraps errors
│   │   ├── errors.ts          AppError
│   │   ├── events.ts          typed on(event, cb) wrappers for §3.3
│   │   └── mock.ts            in-browser fake (fixture JSON) used by Vitest/Playwright and `vite --mode mock`
│   ├── state/
│   │   ├── app.ts             settings, connection, standups (signals)
│   │   ├── route.ts           route signal + navigation helpers
│   │   ├── standup.ts         views cache (Map<standupId, Signal<StandupView>>), selected person, derived selectors
│   │   ├── config.ts          StandupConfig signal for the Configure screen
│   │   ├── save.ts            autosave queue + saved-indicator state (§5.3)
│   │   ├── sync.ts            SyncStatus by standup, event handlers
│   │   └── ticket.ts          open ticket, step list (view order), back stack, optimistic writes, draft autosave
│   ├── layout/                PURE functions (unit-tested): density.ts, board.ts, tabs.ts, below.ts, track.ts
│   ├── views/
│   │   ├── standup/           StandupScreen, Header, Tabs, StandupSwitcher, Rail, PersonView, BoardView,
│   │   │                      TicketRow, PulseTrack, TrackBackdrop, MoreBar, Legend, DoneList, BacklogList
│   │   ├── configure/         ConfigureScreen, StandupList, sections/{Source,StatusMapping,PersonView,Speakers,
│   │   │                      Pulse,WholeBoard,Display,Connection}.tsx
│   │   ├── connect/           ConnectScreen (first run / reconnect)
│   │   └── ticket/            LAZY CHUNK: TicketModal, TicketHeader, PulseStrip, AnchorStrip, Attachments, IssueRows,
│   │                          Comments, Composer, FieldRail, RailValue, StatusField, AssigneeField, FlagField, Lightbox, UpdatedBar
│   ├── components/            Combobox, Segmented, Switch, Stepper, Checkbox, TextField, Toast, SavedIndicator,
│   │                          Icon (inline SVG sprite), KeyLegend, Banner
│   ├── components/adf/        LAZY CHUNK (with ticket/): AdfDoc + one component per node type (ux-spec §14.4); contrast.ts
│   ├── keys.ts                global keyboard map (ux-spec §8: ←/→, B, 1–9, R, Shift+R, /, `,`, ?, Esc, Enter)
│   └── styles/                tokens.css (generated), base.css, standup.css, configure.css
└── tests/                     unit (Vitest), perf + a11y (Playwright against `vite preview --mode mock`)
```

### 4.3 State management

- Module-level signals; no context/provider tree. Components read signals; event handlers call functions in `state/`.
- `standup.ts` holds one `StandupView` per standup (kept for all standups once loaded). Derived `computed`s:
  `currentView`, `currentPerson`, `personGroups` (tickets of the selected speaker grouped by `view.lanes`),
  `boardLanes`. **Switching speaker never calls Go** — it changes one signal; **switching standup** shows the cached
  view immediately and calls `StandupService.View` only if the cached `rev` is older than the tab's (`StandupTab`) rev
  or a `data:changed` arrived.
- Views are replaced wholesale (immutable payloads from Go); rows are keyed by ticket key.
- UI state persistence: `route` changes call `AppService.SetUIState` debounced 500 ms (fire-and-forget).

### 4.4 Bindings and types

- `task bindings` runs `wails3 generate bindings -clean=true -ts -i -d frontend/src/bindings ./cmd/cuisle`
  (verified on v3.1.0: `-ts` TypeScript, `-i` interfaces instead of classes, `time.Time` → `string` (RFC 3339), Go
  string enums with constants → TS `enum`, registered events → `bindings/events`). The Vite plugin is
  `wails("./src/bindings")` from `@wailsio/runtime/plugins/vite`.
- **Output is committed** so fresh worktrees and plain `tsc` work without a Go step; CI regenerates and fails on
  `git diff --exit-code frontend/src/bindings`. Never hand-edit it.
- Generated slices and maps are typed `T[] | null` and `{[k]?: V} | null` even though Go never returns nil lists
  (§3.2; the golden view test asserts it). `api/types.ts` re-exports the models through a `Fix<>` mapping that removes
  `| null` from list and map fields (pointer fields keep it), so the rest of the app sees `T[]`.
- Promise cancellation works end to end: `CancellablePromise.cancel()` rejects with `CancelError` in JS and cancels the
  bound method's `ctx` in Go.
- All application types come from the generated models (Go structs in §3.2 are the single source of truth).
  `api/index.ts` is the only importer; CI lints for imports of `bindings/` elsewhere (ESLint `no-restricted-imports`).
- `api/mock.ts` implements the same facade interface from fixture JSON so the UI can run and be tested in a plain browser.

### 4.5 Routing

```ts
type Route =
  | { view: 'standup'; standupId: number; person: string | null }   // person: accountId | 'unassigned' | null (= whole board)
  | { view: 'configure'; standupId: number | null }
  | { view: 'connect' };
export const route = signal<Route>(initialFromBoot());
```

`App` renders by `route.value.view`. Standup screen stays mounted (hidden with `display:none`) while Configure is open so
"Done" returns instantly with scroll position intact. No URL/hash routing (single window, no deep links). `connect` is
forced when `connection.state` is `missing` and there are no standups; otherwise a banner offers "Reconnect".

### 4.6 Combobox contract

One component serves every dropdown (space, board, lane pickers, add-person, standup switcher).

```ts
interface ComboboxProps<T> {
  id: string;
  label: string;                      // accessible name (visually hidden unless `showLabel`)
  value: T | null;
  getKey: (o: T) => string;
  getLabel: (o: T) => string;         // text placed in the input when selected; also the filter haystack
  renderOption?: (o: T, state: { active: boolean; selected: boolean }) => ComponentChildren;
  options?: T[];                      // static source: filtered locally (case-insensitive; prefix matches first, then substring)
  load?: (query: string, signal: AbortSignal) => Promise<T[]>;  // async source: debounced 150 ms, stale responses dropped
  onSelect: (o: T) => void;           // commit; caller autosaves
  placeholder?: string;
  minChars?: number;                  // default 0 (opens with full/initial list)
  clearable?: boolean;
  disabled?: boolean;
  emptyText?: string;                 // "No matches"
  variant?: 'field' | 'menu';         // 'menu' = button-triggered (standup switcher), same list behaviour
  clearOnSelect?: boolean;            // add-person: clears the input after commit
}
```

Behaviour (WAI-ARIA 1.2 combobox, list autocomplete, manual selection):
- DOM: `<input role="combobox" aria-expanded aria-controls aria-activedescendant aria-autocomplete="list">` +
  `<ul role="listbox">` of `<li role="option" aria-selected>`. Focus never leaves the input.
- Keys: `↓/↑` move (open if closed), `Alt+↓` open, `Home/End` first/last, `Enter` commit active option,
  `Esc` close and restore the committed label (second `Esc` clears if `clearable`), `Tab` closes without committing.
- Typing filters; the first option becomes active. Blur restores the committed label. Mouse: click commits; hover moves active.
- Async: shows "Searching…" row after 200 ms, error row with retry on rejection (`offline` → "Offline — showing cached"
  when the loader fell back to cache).
- Popup: `position: fixed`, anchored to the input rect, flips above when there is no room, max 8 rows then scrolls
  (active option kept in view with `scrollIntoView({block:'nearest'})`). No portal library.
- Global shortcuts (`keys.ts`) are ignored while focus is inside any `input`, `textarea` or `[role=combobox]`.

### 4.7 CSS tokens and themes

- `design/tokens.json` is the single source. `scripts/tokens.mjs` flattens paths to custom properties
  (`color.lane.review` → `--color-lane-review`, `size.row.roomy` → `--size-row-roomy`) and emits:

```css
:root { /* non-colour tokens + light colours */ }
:root[data-theme="dark"] { /* colour overrides */ }
:root[data-text="large"] { --scale: 1.25; }      /* Large (TV) */
```

- Expected `tokens.json` shape: top-level groups (`font`, `size`, `space`, `radius`, …) plus `color: { light: {...}, dark: {...} }`
  with identical key sets. Keys starting with `$` are ignored. The generator **fails the build** if a key exists in one mode only.
  It also emits `src/styles/tokens.ts` exporting the numeric `breakpoint`, `motion` and row-size values for `layout/` and `state/save.ts`
  (CSS custom properties cannot be used in size comparisons). Dark values are authored in
  `tokens.json` during implementation (derived from light), not computed at runtime.
- `<html data-theme>` is set by the frontend from `boot.settings` as it mounts (`/boot.js` carries only `window.__CUISLE_BOOT__`). The native window's `BackgroundColour` already follows the persisted theme, so there is no white flash. `system` resolves through `matchMedia('(prefers-color-scheme: dark)')` and follows OS changes.
  and listens for changes). Lane colours are applied with `data-lane="review"` attribute selectors, never inline colours.
- Sizes use `rem`/`calc(var(--scale) * …)` so Large (TV) scales the whole UI; layout breakpoints are evaluated in CSS px
  of the window/main area (ResizeObserver), per the UX spec. With Large (TV), `layout/` divides measured widths and heights by
  `--scale` before comparing to breakpoints and row sizes (ux-spec §11).
- Fonts are bundled as WOFF2 in `frontend/src/assets/fonts` (no network fetches, `font-display: block`, preloaded).

### 4.8 Rendering and virtualisation

- **No virtualisation.** Worst realistic case is ~500 rows; the "nothing is ever hidden" rule and the
  "Scroll for more — X below" bar need real element heights. Instead: rows have fixed heights per density, lists use
  `content-visibility: auto; contain-intrinsic-size` per lane group, and the scroll container has `contain: strict`.
- Pulse track = plain DOM: one positioned `<i>` per segment/mark with `--x`/`--w` custom properties in **day units**;
  CSS converts (`left: calc(var(--x) / var(--days) * 100%)`). Weekend shading, day axis and the today line are drawn
  **once per list** in a `TrackBackdrop` layer behind the rows, not per row.
- Layout decisions are pure functions in `layout/` fed by measured sizes: `chooseDensity(groups, availH)` (five-step
  ladder: row height 46 → 38 → 34 combined with stacked or side-gutter headings, ux-spec §5.5), `belowFold(groups, density, foldY)`, `balanceLanes(lanes, cols, rowH)`, `tabMode(tabs, availW)`. They are
  ports of the *rules* in `design/reference/prototype-logic.js`, documented in `docs/ux-spec.md`.

### 4.9 Ticket modal and rich text (ADR 0015, ux-spec §14)

- **Code-split (recommended, not required).** `views/ticket/*` and `components/adf/*` may form one lazy chunk
  (`import('./views/ticket')`), prefetched with `requestIdleCallback` after first paint so cold start stays flat and the
  chunk is in memory before anyone clicks. The JS budget is one all-JS number (≤ 70 KB gz, ADR 0014 as raised for F12). No new runtime dependency:
  no Markdown/ADF library, no sanitiser (nothing is ever parsed from HTML), no image viewer.
- **Open** = `TicketService.Get(standupId, issueId)` (local, ≤ 25 ms) → render. The step list (`J`/`K`) is computed in TS
  from the current view's row order (person view: walk order then Done; board: lane order, Done, backlog) and neighbours
  are prefetched (`Get` for ±1) so stepping is instant. The modal is a route overlay
  (`route.ticket = {issueId, stack: number[]}`), not a new view; the standup screen stays mounted behind the scrim.
- **ADF rendering**: `AdfDoc` maps `adf.Node.t` → component via a static table; unknown `t` renders the node's text plus
  the "unsupported block ↗" link. Links render as `<a>` whose click handler calls `OpenIssue`/the OS browser — the webview
  never navigates. Images are `<img src="/media/{id}">` with explicit `width`/`height` (no layout shift), `loading="lazy"`,
  and an `onerror` fallback to the thumb then a placeholder frame with "Load image" (→ `EnsureMedia`).
- **`textColor` contrast clamp is TS** (`components/adf/contrast.ts`): it depends on the active theme's background, so Go
  passes the authored `#rrggbb` and TS adjusts HSL lightness (hue kept) until contrast ≥ 4.5:1 against the theme background (memoised per colour × theme; WP-T2).
- **Optimistic writes** (`state/ticket.ts`): apply the expected change to the open `TicketDetail` **and** to the cached
  `StandupView` row (lane from `TransitionOption.ToLane`, assignee, blocked) → call the service → replace both with the
  authoritative result, or revert both and show the inline reason. Pending/failed comment bubbles are frontend state;
  a failed comment's text stays in the draft (`SaveDraft`) so it survives a restart. Nothing retries by itself.
- **Draft autosave** uses the §5.3 pipeline (key `draft:${issueId}`, 400 ms debounce, flush on blur/close/pagehide) but
  does not drive the Configure "Saved" indicator.
- **Esc unwinding** is a small stack in `state/ticket.ts` (combobox/lightbox → composer blur → modal); global keys in
  `keys.ts` are suspended while the modal is open and the modal's own map (ux-spec §14.10) applies.
- **Lightbox**: same chunk; on open it calls `EnsureMedia` for the image when `cached != "full"` (explicit network trigger).

## 5. Key flows

### 5.1 Cold start

1. `main()`: resolve paths → open log → open DB (writer+reader) → run pending migrations → construct services.
2. Create window (background colour from `app_setting.theme`), start Wails.
3. Webview requests `index.html`, then `/boot.js`: the middleware builds `Bootstrap` (including the last standup's
   `StandupView`, from cache) and returns `window.__CUISLE_BOOT__ = {…};` — no IPC round trip before first paint.
4. `main.tsx` mounts from the boot object → cached board/person visible. `performance.mark('first-view')`.
5. After first paint the frontend calls nothing; Go's `ServiceStartup` has already scheduled the **sync-on-open check**
   (runs once, 300 ms after the window's ready event): for each standup, if policy says so (§6.4) start an incremental
   sync, visible standup first.

### 5.2 Where derived values are computed

**Decision: Go computes every data-derived value; TypeScript only lays out.** (ADR 0008.)

| Computed in Go (`pulse`, `view`) | Computed in TS (`layout/`, components) |
|---|---|
| Status → lane mapping; per-ticket lane | Pixel geometry from day indexes and container width |
| Pulse segments (incl. pre-window state), sparse per-day marks in local time, blocked spans | Density ladder (needs measured height) |
| `DaysInLane`, `OverLimit`, `Blocked` | Below-the-fold summary text, "End of list" flip |
| Done-window filtering, backlog top-N, counts | Board lane balancing into 1–2 columns |
| Ticket detail: ADF → typed node tree, media/card/mention resolution, field kinds, rail grouping and "Not set" folding, comment visibility labels, "new since standup" bound | ADF node → component mapping, `textColor` contrast clamp (per theme), comment order toggle, optimistic write state, step list |
| Effective speaking order (manual / reshuffle-daily seed / Unassigned position) | Tab collapse mode (full → keys → menu), legend mode |
| Axis days (weekend/Monday flags), sprint "ends today" facts | Filtering tickets by selected speaker, grouping by `view.lanes` |

**Every view is built "as of" a time from day one.** `view.Build(snap model.BoardSnapshot, asOf time.Time) StandupView`
never reads the database and never calls `time.Now()`; `pulse` functions take `asOf` where they previously took "now"
(window end, days-in-lane, open blocked spans), and ignore events after it. The live path is
`view.Build(cache.LoadCurrent(id), clock.Now())`; the future historical view is
`view.Build(history.LoadAsOf(id, t), t)` — a different loader, the same builder, the same `StandupView` (with
`Live=false`). In v1 `internal/history` ships as a stub (`LoadAsOf` returns `not_implemented`) plus its table-driven test
skeleton; the data it needs is recorded from the first sync (`data-model.md` §2.5–2.6, ADR 0011).

Why: one tested implementation of time/timezone/changelog rules (table-driven Go tests, golden view models); no date
maths in JS; the fixture and the real path produce identical payloads; the payload is small (≈ 350 bytes/ticket →
≈ 150 KB for 400 tickets, built in < 15 ms) so sending the whole standup once makes speaker switching IPC-free.

### 5.3 Autosave pipeline

```
control change ─► state/config.ts: optimistic signal update
               ─► save.enqueue({key, call})          key = `${standupId}:${field}` (or `app:${settingKey}`)
                    │  text inputs: debounce 400 ms (flush on blur, Enter, route change, pagehide)
                    │  toggles/segmented/steppers/combobox commits/reorders: immediate
                    ▼
               per-key serial queue, last-write-wins coalescing (a newer pending value replaces an older pending one)
                    ▼
               ConfigService.* / AppService.SetAppSetting  (bound call)
                    ▼
               Go: validate → writer conn: BEGIN IMMEDIATE → UPDATE … ; standup.rev = rev+1 ; COMMIT
                    ▼
               SaveAck{rev, savedAt, needsSync} ─► save.ts: mark key clean; config.rev = max(rev)
                    ▼
               SavedIndicator
```

- **Indicator states** (Configure header, left of the **Done** button, `role="status"`; ux-spec §9.1): idle (nothing) →
  `Saving…` (only shown if a call is outstanding > 250 ms) → `Saved` (2 s, then fades to muted `All changes saved`) →
  `Not saved · Retry` (alert colour, Retry is a button) when any key is in failed state.
- **Optimistic UI**: the control shows the new value immediately. Ordering is safe because each key is serialised and
  every write carries the full new value (never a delta) — except reorders, which carry `toPosition` and are serialised
  on one shared key per list (`${standupId}:speakers`, `standups`).
- **Failure**: `retryable` errors (`db`, `timeout`) retry automatically 3× (0.5 s, 1 s, 2 s). `validation` errors do not
  retry: the field shows the message inline (`AppError.field`), keeps the user's text, and the key stays dirty; the last
  persisted value remains in the DB. Leaving Configure with failed keys shows a toast "1 change not saved" with Retry;
  nothing blocks navigation.
- **Server-shaped responses**: `SetSpace`, `SetBoard`, `SortSpeakers`, `AddSpeaker`, `Create`, `Duplicate` return the
  authoritative `StandupConfig`/list, which replaces local state (they change more than the edited field).
- **Source changes** (`space`, `board`, `scope`, `extraFilter`, column mapping) set `standup_sync_state.needs_sync = 1`
  (`SaveAck.NeedsSync`). View-only settings (pulse days, marks, limits, walk order, speakers, done window, backlog rows ≤ cached)
  never need a sync: the next `StandupService.View` reflects them. When the user leaves Configure ("Done"/`Esc`), the
  frontend calls `SyncService.Start(id, "full")` for every standup with `needsSync`. If that sync fails (e.g. offline) the flag
  stays set and the next manual Resync is promoted to full. Empty drafts (no space chosen) are removed with `DiscardDraft` on leaving.
- **Refreshing the view after a save**: after the ack of any view-affecting setting the frontend re-requests
  `StandupService.View` for that standup (≤ 15 ms); Configure › Pulse's live preview renders `PreviewKey`'s ticket from that
  view (a static sample track when the standup has never synced).
- **Extra JQL** is saved as typed (debounced) and validated asynchronously on blur (`ConnectionService.ValidateFilter`);
  an invalid filter shows an inline error and sync for that standup fails fast with `filter_invalid` until fixed.
- **Undo for destructive actions**: `ConfigService.Delete` sets `standup.deleted_at` (soft delete) and returns; the UI
  shows a toast "Deleted “Name” · Undo" for 10 s (also `Ctrl/Cmd+Z` while the toast is visible) → `ConfigService.Restore`.
  Soft-deleted standups are archived, never purged: hidden from tabs and the Configure rail, excluded from sync, history kept. `RemoveSpeaker` uses the
  same toast with a client-side re-`AddSpeaker` + `MoveSpeaker`.
- **Durability**: `synchronous=NORMAL` + WAL: a committed save survives an app crash; an OS crash/power loss can lose the
  last few hundred ms of commits but never corrupts the DB. On `pagehide`/window close the queue is flushed (pending
  debounces fire immediately); Go's window-closing hook waits up to 500 ms for in-flight writes.

### 5.4 Resync

`R` or the header button → `SyncService.Start(current, "incremental")` (`Shift+R` / long-press menu: full). Header shows
`Syncing · issues 120/300` with a cancel × from `sync:progress` (ux-spec §2.1); on `sync:finished(ok|partial)` the frontend re-requests the view (it also
receives `data:changed`). The displayed board never changes mid-sync (atomic apply — `jira-sync.md` §4.6).

## 6. Cross-cutting

### 6.1 Performance budgets and enforcement

Reference hardware: 2020-class laptop (M1 / 4-core x64, SSD). Dataset "L": 5 standups, 400 issues in the visible standup,
28-day pulse.

| Budget | Target (p95) | How measured | CI enforcement |
|---|---|---|---|
| Cold start → cached view painted | ≤ 500 ms macOS/Linux, ≤ 900 ms Windows | `cuisle --bench-startup` (fixture DB "L"): Go records `t_proc_start`, `t_db_ready`, `t_window_shown`; JS reports `first-view` via `ReportPerf`; process prints JSON and exits | Run 5× per OS runner; **fail at > 1.5× budget** (runner noise), warn at > budget; trend stored as artifact |
| Go start: DB open + migrate-check + Bootstrap build | ≤ 60 ms | `go test -bench BenchmarkBootstrap ./internal/view` | fail if > 60 ms/op on ubuntu runner (benchstat vs. committed baseline, +20 % tolerance) |
| `StandupService.View` build, dataset L | ≤ 15 ms, ≤ 2 MB alloc | `BenchmarkStandupView` | same |
| Standup switch (cached view) → painted | ≤ 50 ms | Playwright (Chromium, `--mode mock`, 4× CPU throttle): `performance.measure` from keydown to next frame after render | fail if p95 of 20 runs > 50 ms |
| Standup switch (needs `View` call) | ≤ 100 ms | manual/`--bench` only | warn |
| Person switch (←/→) | ≤ 32 ms (two frames), no IPC | Playwright as above; also asserts zero facade calls | fail > 32 ms |
| Configure field save: control → ack | ≤ 30 ms | `BenchmarkConfigUpdate` (real file DB) + e2e mark | fail if bench > 10 ms/op |
| Combobox keystroke → filtered list (static, 500 options) | ≤ 16 ms | Playwright | fail > 32 ms |
| JS bundle (all JS, gzip) | ≤ 70 KB (ADR 0014, raised by Chris on 2026-11-23 for F12 from 50 KB) | `size-limit` on `frontend/dist/assets/*.js` | fail |
| Ticket modal open: click/Enter → modal painted (fixture ticket with every ADF node, 7 comments, 5 attachments) | ≤ 100 ms, **zero network** | Playwright (mock mode, 4× CPU throttle, chunk prefetched); Go side `BenchmarkTicketDetail` | fail > 100 ms; bench fail > 25 ms/op; integration test asserts an empty fake-Jira request log |
| Ticket step (`J`/`K`) → painted | ≤ 50 ms | Playwright | fail |
| Light write: action → optimistic UI | ≤ 32 ms (the network round trip is not budgeted) | Playwright | fail |
| CSS (gzip) | ≤ 18 KB (ADR 0014 amendment, F12: 15.7 KB with the ticket modal mounted) | `size-limit` | fail |
| Fonts | ≤ 160 KB WOFF2 total | file-size check | fail |
| Memory: Go heap steady state | ≤ 40 MB | `runtime.ReadMemStats` in `--bench-startup` after loading 5 views | fail > 60 MB |
| Memory: total RSS incl. webview, steady | ≤ 300 MB | manual per release (Activity Monitor/Task Manager) | release checklist |
| Binary size (per arch, uncompressed) | ≤ 30 MB | `ls -l` in package job | warn |
| Incremental sync, nothing changed, 300-issue standup | ≤ 5 s on broadband; ≤ 6 requests + 1 per dev target whose Development field is unusable (jira-sync §2.11); total ≤ 8 on the fake board | integration test against `jiratest` counts requests | fail on request-count regression |
| Full sync, 300 issues (records incl. descriptions, fields and all comments; images land afterwards in the media phase) | ≤ 60 s | manual against a real site | release checklist |

Startup and interaction marks are also logged at `debug` in normal builds so regressions can be diagnosed from a user's log.

### 6.2 Logging

- `log/slog` JSON handler → rotating file `cuisle.log` (5 MB × 3 files) and, in dev, stderr text. Level from
  `CUISLE_LOG` (`info` default, `debug`).
- Every sync run logs: run id, standup id, mode, per-phase durations, request count, 429 count, outcome.
- HTTP logging: method, path (query **values redacted** for `jql` unless `debug`), status, duration, `X-ARequestId`/`Atl-Traceid`.
  Never logged: `Authorization`, the token, email at `info`, issue summaries, **descriptions, comment bodies, field values,
  attachment names, draft text** — at any level, including `debug` and error details. Request/response bodies are never
  logged; write calls log method, path, status and the issue id only. A redaction test greps logs for fixture body strings.
- **Privacy boundary (ADR 0015):** ticket content (descriptions, comment bodies, field values, images) is stored locally in
  `cuisle.db` and the media cache, like summaries. It never leaves the machine except back to its own source: it is not in
  `cuisle.export.v1`, logs, `Diagnostics()`, crash output or `LogClientError` payloads (the frontend strips `detail`).
- Frontend errors (`window.onerror`, unhandled rejections) are forwarded via `AppService.LogClientError`
  (deduplicated, rate-limited to 20/min).
- `Diagnostics()` powers a "Copy diagnostics" action (version, OS, paths, schema version, last 5 sync runs; no secrets).

### 6.3 Crash safety and storage

- SQLite via `modernc.org/sqlite`, `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`,
  `temp_store=MEMORY`. Writer pool: 1 connection, `_txlock=immediate`. Reader pool: 4 connections, `query_only=ON`.
- All multi-statement writes are single transactions. Sync applies in **one** transaction per standup; a crash mid-sync
  leaves the previous snapshot intact and a `sync_run` row in `running` state that is marked `failed (interrupted)` at next start.
- Migrations: embedded, numbered, forward-only, each in its own transaction; the DB file is copied to
  `cuisle.db.bak-v<N>` before the first pending migration (keep 2 backups). A DB with a schema version newer than the
  binary refuses to open with a clear dialog (no downgrade). Details in `data-model.md` §3.
- Clean shutdown: flush autosave, cancel syncs (rolled back), `PRAGMA optimize`, `wal_checkpoint(TRUNCATE)`, close.
- Corruption (`PRAGMA quick_check` fails at open): rename to `cuisle.db.corrupt-<ts>`, restore the newest `.bak` if any,
  else start empty; settings loss is reported in a banner. Current board state is recoverable by a full sync; accumulated history (spans, config history, old dev events) is not —
  hence the pre-migration backups, and a weekly `VACUUM INTO cuisle.db.bak-weekly` at startup (kept: 1) once history exists.

### 6.4 App data locations and setting keys

| OS | Data dir (DB, backups) | Logs |
|---|---|---|
| macOS | `~/Library/Application Support/Cuisle/` | `~/Library/Logs/Cuisle/` |
| Windows | `%LOCALAPPDATA%\Cuisle\` | `%LOCALAPPDATA%\Cuisle\logs\` |
| Linux | `${XDG_DATA_HOME:-~/.local/share}/cuisle/` | `${XDG_STATE_HOME:-~/.local/state}/cuisle/` |

Media cache: `<data dir>/media/` (inside the data dir so one folder holds everything; safe to delete — images re-download on demand).
`CUISLE_DATA_DIR` overrides both (used by tests, bench and `--fake-jira`). DB file: `cuisle.db`.
Keychain: service `crueber.cuisle`; accounts `<source>:<host>:<account>` for a connection (e.g.
`jira:acme.atlassian.net:me@acme.com`) and `sink:<id>` for a sink secret (macOS Keychain, Windows Credential Manager,
Linux Secret Service). On Linux without a Secret Service provider `Connect` fails with `keychain_unavailable` and
instructions; the only bypass is `CUISLE_JIRA_TOKEN` (process env, never persisted).

`app_setting` keys (all values are strings):

| Key | Values | Default |
|---|---|---|
| `theme` | `light` \| `dark` \| `system` | `system` |
| `text_size` | `standard` \| `large` | `standard` |
| `sync_on_open` | `stale` \| `always` \| `never` | `stale` |
| `sync_stale_hours` | integer 1–48 | `6` |
| `ticket.comment_order` | `newest` \| `oldest` | `newest` |
| `media.cache_max_mb` | integer 64–1048576 (cap for full-size images; thumbnails are not counted) | `1024` |
| `storage.cleanup_offer_mb` | integer 1024–65536 (Disconnect offers to delete synced content only above this, ux-spec §14.11) | `2048` |
| `jira.max_rps` | hidden; integer 1–20 (the limiter's sustained rate, jira-sync §3.1) | `8` |
| `history_retention_days` | reserved; `0` = keep forever (only behaviour in v1, no UI) | `0` |

Sync-on-open policy: `always` → sync every configured standup at launch; `stale` → sync a standup when
`now − last_success_at > sync_stale_hours` (or never synced); `never` → nothing. In all cases the cached view shows first.

### 6.5 Packaging, signing, CI

- Build system: Wails v3 Taskfiles (`wails3 task build`, `wails3 task package`); frontend built by Vite and embedded
  with `//go:embed all:frontend/dist`. Version via `-ldflags -X MODULE/internal/buildinfo.Version=…`.
- **cgo**: Wails needs cgo on macOS (Cocoa/WKWebView) and Linux (GTK/WebKitGTK); Windows builds are `CGO_ENABLED=0`.
  Therefore each OS is built on its own native runner — no cross-compilation. The pure-Go SQLite driver keeps the
  Windows build toolchain-free. `go test ./internal/...` runs with `CGO_ENABLED=0 -tags purego` everywhere: Wails
  v3.1's build guard refuses cgo-free builds unless the `purego` tag is set (verified in the M0 spike), and only
  `internal/app` imports Wails.
- **Signing: self-signed for now (ADR 0013).** No Apple Developer ID, no notarisation, no commercial Windows cert.
- **macOS**: universal binary (arm64 + amd64, `lipo`), `.app` in a `.dmg`. Minimum macOS 12. Bundle id `crueber.cuisle`
  (final; it keys the single-instance lock and the keychain service name). Signed with a **stable self-signed
  code-signing certificate** ("Cuisle Self-Signed", created once by Chris, exported as a `.p12`):
  `codesign --force --deep --options runtime --sign "Cuisle Self-Signed" Cuisle.app`. CI imports it into a temporary
  keychain from secrets `MACOS_SIGN_P12` / `MACOS_SIGN_P12_PASSWORD`; if the secrets are absent the build falls back to
  ad-hoc (`--sign -`). Local day-to-day builds are ad-hoc. Consequences to document for users:
  - First run is blocked by Gatekeeper ("cannot be opened because the developer cannot be verified"): right-click → Open,
    or System Settings → Privacy & Security → Open Anyway, or `xattr -dr com.apple.quarantine /Applications/Cuisle.app`.
  - Keychain items are ACL-bound to the signing identity. With the stable self-signed cert the "Cuisle wants to use your
    keychain" prompt appears once ("Always Allow"); with **ad-hoc** signatures the identity changes on every build, so the
    prompt reappears after each update. Hence the stable cert for anything distributed.
- **Windows**: amd64 (+ arm64 later), NSIS installer per-user (no admin), WebView2 Evergreen bootstrapper embedded
  (download-if-missing). Signed with a **self-signed Authenticode certificate** via `signtool` (secrets
  `WINDOWS_SIGN_PFX` / `WINDOWS_SIGN_PFX_PASSWORD`; unsigned if absent). SmartScreen "Windows protected your PC" →
  More info → Run anyway is expected on every new version; the public cert is published with the release for anyone who
  wants to trust it.
- **Linux**: amd64 AppImage + `.deb` (nfpm), **unsigned**, with a `SHA256SUMS` file attached to every release (covering
  all OS artifacts). Runtime deps: `libgtk-3-0`, `libwebkit2gtk-4.1-0`; build deps the `-dev` equivalents. Built on the
  oldest supported Ubuntu LTS runner (22.04) for glibc compatibility. Secret Service required.
- **CI and publishing (same model as Chris's Weave project).** Development happens on the Forgejo origin
  (`github.com/crueber/cuisle`); GitHub (`github.com/crueber/cuisle`) is a **published distribution mirror**, not a
  push mirror. The workflow files:
  - `.forgejo/workflows/ci.yml`: Forgejo, on push/PR/tag. **Linux only**: Go vet, layering check, tests; frontend
    tsc, eslint, vitest, vite build and size-limit; Linux package (AppImage + `.deb`) and the `--bench-startup` smoke under
    `xvfb-run`. On `v*` tags it publishes a Forgejo release with the Linux assets and `SHA256SUMS.txt` via the Forgejo API
    (curl). It uses no JavaScript actions: the job image is the Cuisle CI image.
  - `.forgejo/workflows/ci-image.yml`: builds that image (`build/ci/linux.Dockerfile`: Ubuntu 22.04, Go, Node, Task,
    wails3, GTK3/WebKitGTK 4.1 dev, xvfb, gnome-keyring) with daemonless buildah and pushes it to
    `github.com/crueber/cuisle-ci`. Pin it by digest, as Weave does with `scripts/update-pins.sh`.
  - `.forgejo/workflows/publish-github.yml`: manual "publish to github" (workflow_dispatch, version bump
    patch/minor/major/none). It runs `scripts/publish-to-github.sh`, ported from Weave:
    1. Exports the tree.
    2. Rewrites any `github.com` references (the Go module path is already `github.com/crueber/cuisle`).
    3. Removes `.forgejo/` and installs `.github/workflows/` from `scripts/publish/*.yml`.
    4. Pushes one squash commit per publish onto GitHub `main`, then pushes the version tag separately.

    Secret: `PUBLISH_GITHUB_TOKEN`.
  - `scripts/publish/ci.yml` (installed as GitHub `.github/workflows/ci.yml`): matrix `ubuntu-22.04`, `macos-14`,
    `windows-latest`.
    1. Go vet, layering check, tests (`-race` on mac/linux).
    2. Bindings, tsc, eslint, vitest, vite build, size-limit.
    3. Playwright perf, a11y and screenshots (Ubuntu).
    4. `task package` and a smoke run.
  - `scripts/publish/release.yml` (installed as GitHub `.github/workflows/release.yml`): on the pushed tag, build and
    package on all three OSes, self-sign (above), and write `SHA256SUMS.txt`. When `RELEASE_SIGNING_KEY` is set it adds an
    ed25519 `.sig`, as Weave does. It then publishes the GitHub Release with first-run instructions for Gatekeeper,
    SmartScreen and the Linux keyring. Secrets: `MACOS_SIGN_P12`, `MACOS_SIGN_P12_PASSWORD`, `WINDOWS_SIGN_PFX`,
    `WINDOWS_SIGN_PFX_PASSWORD`, `RELEASE_SIGNING_KEY` (all optional with documented fallbacks).
  - **macOS day-to-day builds are local** (`task package`, ad-hoc signed).
  - **Agents never publish:** an agent session's job ends at the Forgejo push. Running `publish-to-github.sh --apply` or
    pushing to GitHub is Chris's call (AGENTS.md §8).
- No auto-updater in milestone 1 (see risks).

## 7. Risks & open questions

| # | Risk / unknown | Impact | Mitigation / decision needed |
|---|---|---|---|
| R1 | ~~**Wails v3 API drift.**~~ **Resolved in the M0 spike on v3.1.0** (ADR 0001): every name below exists and behaves as described. Names used here (`application.NewService`, `Options.MarshalError`, `AssetOptions.Middleware`, context injection in bound methods, `wails3 generate bindings -ts`) must be verified against the exact pinned 3.x version. | Compile-time only | First implementation task is a spike that pins the version and adjusts §3.1/§4.4 wording; if `MarshalError` is absent, encode errors as `"<code>|<json>"` strings in the facade. |
| R2 | **Dev-status API is internal** (`/rest/dev-status/latest/...`), may reject API-token auth on some sites, change shape, or truncate commit lists. | Commit/PR marks missing or partial | Probe once per sync; on failure mark `dev_activity_state='unavailable'`, hide commit/PR legend entries with a one-line notice, keep everything else. See `jira-sync.md` §2.11, ADR 0007. |
| R3 | **PR timing fidelity.** Dev-status exposes a PR's `lastUpdate` and status, not its opened/merged timestamps. | PR diamonds mark "last PR activity day" plus transitions Cuisle itself observed across syncs | Accepted for M1; document in UX legend ("PR activity"). |
| R4 | **Scoped API tokens** (api.atlassian.com gateway, granular scopes) do not cover agile/dev-status internals. | Connect succeeds but boards/dev data fail | M1 supports classic (unscoped) API tokens against `https://<site>.atlassian.net` only; Connect screen says so. |
| R5 | **Board filter access.** JQL uses `filter = <id>`; the user must be able to view the board's saved filter. | Sync fails with `filter_invalid`/empty | Detect at `SetBoard` (probe query) and report "You can't see this board's filter". |
| R6 | **Embedded comments in search are capped** per issue. | Comment ticks under-counted on very chatty issues | Top-up with per-issue comment paging when `comment.total > len(comments)` and the oldest returned is inside the window. |
| R7 | **Jira rate limits** (burst and the newer points-based quotas). | Slow/failed sync on big boards | 4 concurrent requests, `Retry-After` honoured globally, bounded total wait; dev-status calls are the largest cost and are limited to "dev targets". |
| R8 | **WebView differences** (WebKitGTK is the slowest and oldest engine). | CSS features (`content-visibility`, container queries) missing/slow on Linux | Treat WebKitGTK 2.40 as the baseline; no feature without a fallback; Linux packaged build is in the CI smoke. |
| R9 | **Windows cold start** depends on WebView2 process spin-up. | 900 ms budget may be tight on cold disks | Window shown with themed background immediately; budget is "view painted", measured separately per OS. |
| R10 | **Linux keychain** absent in minimal/headless desktops. | Cannot store token | Clear error + env override; no plaintext fallback (ADR 0006). |
| R11 | **Days-in-lane counts calendar days**, not working days; limits apply to all four lanes incl. To Do (0 disables). The prototype excluded To Do from the red state. | Chips may go red over a weekend | Product decision to confirm; implemented behind one function `pulse.DaysInLane`. |
| R12 | **"Since yesterday"** is implemented as "since local midnight of the previous working day" (Mon → Fri). | Differs from literal reading | Confirm in product spec. |
| R13 | **Multiple active sprints** on one board. | Ambiguous header | All active sprints are included in scope; header names the one ending first. |
| R14 | **Kanban sub-filter / kanban backlog** variants across classic and team-managed boards. | Wrong Done/backlog membership | Board config `subQuery` is ANDed when present; backlog = statuses in columns mapped to lane `backlog`. Needs testing on a real team-managed board. |
| R15 | **Sample fixture vs. real changelog semantics** (fixture has whole-day history). | Edge cases (multiple transitions/day, reopen) untested by fixture | Table-driven `pulse` tests with hand-written changelogs cover these; see `jira-sync.md` §5. |
| R16 | **No auto-update / no telemetry** in M1. | Manual upgrades | Revisit after M1; data dir and schema are forward-migratable. |
| R17 | ~~Bundle id / module path / wordmark placeholders~~ **Resolved:** module `github.com/crueber/cuisle`, bundle id `crueber.cuisle`, wordmark "CUISLE". | — | — |
| R18 | ~~CI hosting~~ **Resolved:** Forgejo origin runs Linux CI and Linux releases; GitHub (published mirror via `publish-to-github.sh`, Weave model) runs the 3-OS CI and the desktop release (§6.5). | Two workflow sets to keep in step | Shared steps live in `Taskfile.yml` targets so every workflow stays thin; the GitHub workflows are templates in `scripts/publish/`. |
| R19 | **History fidelity.** Membership and config are historised per sync run, so an "as of" board is only as fine-grained as the user's sync habit, starts at the standup's first sync, and shows current summary/type/rank. Dev events exist only from when they were first observed. | Future historical view has gaps on days without a sync | Accepted; documented in `data-model.md` §2.6. The header of a historical view must state "as of last sync before <date>". No backfill attempt in v1. |
| R20 | **Keep-forever growth and migrations.** Record tables are never pruned and must survive every future migration in place. | DB grows ≈ 4–5 MB per board-year; migration mistakes are costlier | Size estimates + 3-year benchmark fixture (`data-model.md` §5.1); pre-migration backup; reserved `history_retention_days` cap. |
| R21 | **Provider-neutral abstraction designed against one provider.** `source.Source` and `model` are shaped by Jira (boards with columns, rank, sprints, flagging). | A second provider may force interface changes | Capabilities flags gate optional behaviour; provider extras live in `source_meta`; the interface is internal (no plugin ABI), so changing it is a refactor, not a break. |
| R22 | **Sink delivery without timers.** Failed deliveries wait for the next explicit trigger. | A failed after-sync export can sit unnoticed | `sink:failed` event + visible state on the sink; manual Retry; `dead` after 8 attempts. Only the file sink ships in v1, where failures are local and immediate. |
| R23 | **Self-signed builds.** Gatekeeper/SmartScreen warnings on install; macOS keychain prompts recur if the signing identity changes (ad-hoc builds, lost/rotated cert). | Friction for anyone but the owner | Stable self-signed cert kept by Chris and in CI secrets; first-run steps in README; revisit Developer ID / commercial cert if distribution widens (ADR 0013). |
| R24 | **ADF media id ↔ attachment id.** ADF `media` nodes carry a media-services file id; the attachment REST API exposes numeric ids. There is no documented join. | Inline images show "attachment unavailable ↗" | Layered mapping in the Jira adapter (`jira-sync.md` §2.14): media id captured from the content redirect, then filename/alt match, then document order; unresolved nodes degrade to a link and raise warning `media_unresolved`. Verify on a real site in the first content spike. |
| R25 | **ADF schema drift / unknown nodes** (new node types, extensions, macros from apps). | Content partially rendered | Unknown nodes degrade to text + "unsupported block ↗"; raw ADF is stored, so a renderer update fixes old tickets without a resync; golden test per node. |
| R26 | **DB and media growth from content.** Descriptions, comments and every field value roughly triple per-issue storage; images are bounded only by the cap. | Larger DB, slower backups | Estimates in `data-model.md` §5.1; media cap + LRU + "Clear image cache"; `issue_field_value` is current-only; weekly backup excludes media. |
| R27 | **Write permissions and workflow rules.** The user may lack transition/assign/comment permission; transitions may have validators, required screens or conditions Cuisle cannot see. | Writes rejected | Provider message surfaced verbatim (`write_rejected`/`permission_denied`); screen transitions routed to the browser; UI reverts; no auto-retry. |
| R28 | **Write/sync races and eventual consistency.** The one-issue refresh right after a write can return stale data (search index lag does not apply to `GET /issue`, but changelog can lag). | History row for the change missing until next sync | Refresh uses the issue endpoint, not search; apply never overwrites newer `updated_at`; if the changelog lacks the change, a synthetic change row is **not** invented — the next sync merges the real one. |
| R29 | **Sensitive content on a shared screen and on disk.** Restricted-visibility comments and attachments are now local and rendered. | Disclosure during screen share; data at rest unencrypted | Loud visibility tag (ux-spec §14.5); DB/media rely on OS disk encryption (documented); content excluded from exports/logs; whether Disconnect should offer to delete synced content is an open product question. |
| R30 | **Sync cost.** Full comments per changed issue and image downloads add requests. | Slower first sync; rate limits | Comments only for hydrated issues; media after the record apply in a bounded pool (2), resumable, skipped when offline; budgets in `jira-sync.md` §4.3. |
