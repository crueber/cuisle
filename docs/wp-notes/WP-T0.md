# WP-T0: one rate limiter per Jira connection; every Jira request goes through it

Spec: `docs/jira-sync.md` §3 and §3.1 (ADR 0015), `docs/testing.md` "Ticket refresh and throttling",
`docs/wp-notes/F12-freeze.md` decisions 1 and 2. Code: `internal/source/jira/limiter/` (stdlib-only),
`internal/source/jira/client_limiter.go`, `policy_lanes.go`, edits in `client.go` and `policy.go`; gate test in
`internal/sync/budget_test.go`.

## How it attaches (read this before T4, T5, T9)

`NewClient` wraps its `http.Client`'s transport in a `limiter.Transport`. Anything that sends through `c.http`, including
retries, redirect hops and the raw downloads T4 will add, takes a limiter permit first. There is no call site to remember.
`Client`, `ClientOptions`, `Policy`, `PolicyOptions` and `Adapter.New` are frozen (`contract.go`), so nothing was added to
them: the limiter is reached through `(*Client).Limiter()` and the registry (`LimiterFor`).

```
http.Client → limiter.Transport   lane, bucket, concurrency, 429 gate, AIMD observe
            → Policy.slot         process-wide in-flight cap (no bucket any more)
            → countRT             sync_run.requests / Client.Counts (only requests that reach the wire)
            → caller's transport
```

The limiter takes its numbers from the `Policy` the client was given, so `SetDefaultPolicy` (the tests' fast policy:
`RatePerSecond: 1e6`, capped `Sleep`) keeps working untouched: `RatePerSecond` is the sustained rate (burst = 2x),
`Concurrency` the in-flight cap (media = half, min 1), `MaxGate`, `Jitter` and `Sleep` as before.

## API

### package `limiter` (`internal/source/jira/limiter`)

```go
type Lane int            // Interactive (0) > Sync (1) > Media (2); lower value is served first
func (Lane) String() string

type Config struct{ MaxRate, Burst, MinRate, RecoverStep float64; RecoverEvery, HalveCooldown, MaxGate time.Duration
    Concurrency, MediaConcurrency int; Jitter func() float64; Clock Clock
    OnThrottle func(ThrottleEvent); OnGate func(until time.Time)
    WaitGate func(ctx context.Context, until time.Time) error; ManualGate bool }
func Defaults() Config                       // the §3.1 numbers
func New(cfg Config) *Limiter                // zero fields take the defaults

func (l *Limiter) Acquire(ctx context.Context, lane Lane, opts ...AcquireOption) (*Permit, error)
func FailFastOnGate() AcquireOption          // *GateError at once while the gate is closed (writes)
type GateError struct{ ResumeAt time.Time; RetryIn time.Duration }
func (p *Permit) Release()                   // idempotent; hold until the response body is closed
func (l *Limiter) Observe(status int, h http.Header)   // 429 → halve (+gate unless ManualGate); NearLimit → halve
func (l *Limiter) CloseGate(until time.Time)           // never shortens; fires OnGate / OnThrottle
func (l *Limiter) SetOnThrottle(fn func(ThrottleEvent))  // attach the sync:throttled feed to a built limiter
func (l *Limiter) TryLockIssue(key string) (release func(), ok bool)
func (l *Limiter) Stats() Stats              // Rate, MaxRate, GateUntil, InFlight, MediaInFlight, Waiting[3], Requests[3], Throttles, NearLimit, Events (last 32)

func WithLane(ctx, Lane) context.Context;  func LaneFrom(ctx) Lane
func WithWrite(ctx) context.Context;       func IsWrite(ctx) bool       // write = Interactive + never retried + fail-fast
func RetryAfter(h http.Header, now time.Time) time.Duration   // Retry-After → X-RateLimit-Reset → RateLimit t= → 5 s
func Signal(h http.Header) bool                               // NearLimit, Remaining/Limit < 0.2, structured r/q < 0.2

type Transport struct{ Base http.RoundTripper; Limiter *Limiter; Slot func(ctx) (release func(), err error); Timeout time.Duration }

type Clock interface{ Now() time.Time; TimerAt(at time.Time) (<-chan time.Time, func()) }   // absolute timers, no lost advances
type RealClock struct{};  type FakeClock  // NewFakeClock, Advance, SetAutoAdvance, SetTimersFireImmediately, Pending, Started, WaitPending
```

### package `jira` additions

```go
func LimiterFor(conn model.Connection) *limiter.Limiter     // the limiter all Clients/Adapters of the connection share
func (c *Client) Limiter() *limiter.Limiter
func (c *Client) TryLockIssue(issueKey string) (release func(), err error)   // err is apperr conflict (T5, T6 Refresh)
func MapWriteError(err error) error          // MapError + RetryInMs on rate_limited (Retry-After, or the gate's remaining wait)
func WithInteractive(ctx) context.Context    // ticket Refresh, combobox search, Connect, validate (T4, T6, T9)
func WithMedia(ctx) context.Context          // attachment and thumbnail downloads (T4)
func WithWrite(ctx) context.Context          // transitions, assign, flag, comment (T5)
```

Usage rules:
- **Mark only the request that carries the mark.** A write operation that first reads (the transitions list) marks the
  context of the POST, not of the read. A write is `WithWrite`: one attempt, 429 and 5xx return at once, and while the 429
  gate is closed it is refused without touching the network. Wrap its error with `MapWriteError`, which sets
  `RetryInMs` (6000 for `Retry-After: 6`, 5000 when Jira named no wait, the gate's remaining time when refused locally).
- **T4's media/raw downloads:** send through `c.http` (the transport limits them), with `WithMedia(ctx)`. A raw caller
  that sees a 429 should call `c.Limiter().CloseGate(time.Now().Add(wait))` after its own decision, as `Client.do` does.
- **T4 should mark the interactive adapter methods** (`Test` when used by Connect, `SearchSpaces`, `SearchBoards`,
  `SearchUsers`, `ValidateFilter`) with `WithInteractive` inside the method; `endpoints_ref*.go` is T4's, not T0's. Today
  every existing call is the Sync lane.
- **T5/T6 (per-issue serialisation):** `rel, err := c.TryLockIssue(key)`; on error return it (`conflict`); `defer rel()`.
  The lock table is the limiter's, so every Client of the connection sees it. The 10 s Refresh cooldown stays in
  `TicketService`, before the limiter.
- **T9/diagnostics:** `jira.LimiterFor(conn).Stats()`. For `sync:throttled {resumeAt, reason}` attach a callback to the
  connection's limiter from wherever the event stream lives (it may import `jira` only in `cmd/cuisle`):
  `jira.LimiterFor(conn).SetOnThrottle(func(e limiter.ThrottleEvent){ ... })`; `e.ResumeAt` and `e.Reason`
  (`"rate_limited"`) are the event's fields. It runs outside the limiter's lock when a honoured 429 closes the gate.

## The numbers (every one is in a code comment citing jira-sync §3.1)

| Number | Value | Where |
|---|---|---|
| Sustained rate | 8 req/s (`PolicyOptions.RatePerSecond` default, was 10) | `policy.go` `defaultRate` |
| Burst | 16 = 2 x rate; shrinks in proportion when the rate is halved | `limiter.Config.Burst` |
| In flight | 4 (policy), media at most 2 | `Concurrency`, `MediaConcurrency` |
| AIMD floor / step / period | 1 req/s / +1 / 30 s, up to the configured rate, computed lazily | `MinRate`, `RecoverStep`, `RecoverEvery` |
| Near-limit | `X-RateLimit-NearLimit: true`, or Remaining/Limit < 0.2, or structured r/q < 0.2 | `headers.go` |
| 429 gate | Retry-After → X-RateLimit-Reset → RateLimit `t=` → 5 s, + 0-500 ms jitter; none when it exceeds `MaxGate` (120 s) | `headers.go`, `Client.do` |
| 429 retries / cumulative wait | 5 / 120 s, unchanged (jira-sync §3) | `Client.do`, `reserveGate` |
| Halving cooldown | 1 s (see decision 1 below; not in the spec) | `Config.HalveCooldown` |

No goroutine tickers: recovery is arithmetic on timestamps; a waiter parks on a one-shot timer or a state-change channel
(`TestNoBackgroundGoroutines`).

## Decisions I made

1. **One halving per 1 s window.** The headers of one burst window arrive on up to 4 concurrent responses; halving per
   response would take 8 to 1 req/s from a single window. Signals inside the cooldown only restart the 30 s recovery
   clock. This is the only behaviour not in §3.1.
2. **Registry key is site + account, not connection ID.** `Adapter.New` (frozen) hands `NewClient` only `BaseURL` and
   `Account`, and `connection.base_url` is UNIQUE in `0001_init.sql`, so the pair names the same connection. Normalised
   (case, trailing slash), never logged. Entries are also tied to the `Policy` they were built from, so a different
   policy (a test's `SetDefaultPolicy`, a changed `jira.max_rps`) gets a fresh limiter and reused httptest ports cannot
   inherit state.
3. **The 429 gate is closed by the client, not by `Observe`** (`Config.ManualGate`). The client's retry and cumulative
   budgets decide first, exactly as before: a `Retry-After` beyond `MaxGate`, or a 429 over the 120 s budget, closes
   nothing (`TestReviewFix_429*` pin that). `Observe` still halves the rate. Writes close the gate (so the next request
   waits) but never retry.
4. **The Policy gate and sleeper stay in charge of waiting a 429 out** (`OnGate` closes the Policy gate; `WaitGate` waits
   through `Policy.waitGate`). The existing client and sync tests pin two different fake-`Sleep` styles (one that clears
   `pol.gate`, one that caps sleeps and relies on real time passing); a second, independent gate sleeper breaks one or the
   other. Consequence: a 429 on connection A also pauses connection B (the Policy gate is process-wide, as before). The
   limiter's own gate remains per connection for fail-fast writes, `Stats` and lane order.
5. **`Policy.Acquire` is kept, `Client` uses `Policy.slot`** (gate + semaphore, no bucket): the token bucket moved into
   the limiter so there is one bucket, not two in series. The policy's process-wide in-flight cap stays because the
   existing tests pin it (`TestClient_ContextCancelledWhileWaitingForSlot_SendsNothing`).
6. **Order is limiter then policy slot**, both inside the transport, so lane order is decided before the process-wide cap.
7. **`Client.Counts` counts at the wire** (`countRT`, after limiter and slot), so a request cancelled or refused while
   waiting is not counted and `sync_run.requests` equals the fake's request log (asserted by the budget gate).
8. **Time base:** the limiter and the gate use real `time.Now`, never `ClientOptions.Now` (the sync harness and `app`
   tests inject frozen clocks there; a frozen clock would never refill the bucket or expire a gate).
   `reserveGate` keeps using `c.now()` as before.
9. **A client built from another client's `http.Client` is not limited twice** (it unwraps first); a second wrap would
   deadlock on its own permit at `Concurrency: 1`.
10. **`http.Client.Timeout` moved into the transport** (`limiter.Transport.Timeout`; `limit()` zeroes the client's own).
    The client's 30 s timer starts before `RoundTrip`, so with the limiter inside the transport a `Retry-After` of 40 s,
    an AIMD-slowed queue or media behind a long sync would have been charged to the request, ended as a timeout, and
    burned `do()`'s backoff retries. The transport now starts the same budget after the permit and slot are granted and
    ends it when the body is closed; the error is still `context.DeadlineExceeded`, so it maps to `timeout` and is
    retried as before (`TestTimeout_*`).

## Tests

- `limiter/`: 57 top-level tests (about 83 with subtests), all on `FakeClock`, no real sleeps (two transport timeout
  tests use a few ms of real context deadline): bucket and burst, 200-request
  synthetic sync (never above burst + rate x t, takes 23 s of fake time), interactive behind 100 sync requests goes out
  within one token interval, media never starts while sync waits, concurrency 4/2, AIMD halve/floor/recover/restart,
  header table (both families, structured r/q, NearLimit casing), 429 pauses every lane, Retry-After fallbacks and
  MaxGate, fail-fast, host bridges, issue locks, no goroutines, race stress.
- `client_limiter_test.go`: 24 tests (three `TestTimeout_*` use real timers: the `Sleep == nil` path and the 100 ms
  `http.Client.Timeout` against a 300 ms gate, which failed before decision 10). Transport-bypass tests with an innermost checking transport (a request without a
  held permit is flagged; the checker's own negative control; whole-adapter surface against jiratest; redirects;
  retries), registry, lanes, shared gate, writes (429/5xx/transport not retried, `retryInMs`, fail-fast during the gate,
  sync reads still wait), `NearLimit` halving, issue-lock conflict, permit release when the policy slot wait fails.
  Removing `c.limit(...)` from `NewClient` makes the bypass tests fail (checked).
- `internal/sync/budget_test.go`: `TestRequestBudget_FullAndIncrementalSyncOnTheFixtureBoard` (the gate) and
  `TestLimiter_NoSyncRequestBypassesIt` (every request of two full orchestrator runs held a permit, all in the sync lane).
  Measured, identical to the v1 baseline: full 159 = 30 non-dev + 44 summary + 85 detail; incremental 38 = 9 + 29 + 0.
  Budgets: non-dev <= 250 / <= 50, summaries <= targets, details <= 2 x changed targets (none on the incremental).

## CONTRACT-ISSUES and doc contradictions (for the orchestrator)

1. **`go run ./scripts/layercheck` is red on this branch until the orchestrator applies one line** (`scripts/layercheck`
   is outside T0's ownership, so I did not commit it). The only violation is `internal/source/jira imports
   internal/source/jira/limiter` (non-test and test). Patch, in `allowed` of `scripts/layercheck/main.go`:
   `"internal/source/jira": {"internal/source", "internal/model", "internal/apperr", "internal/adf", "internal/source/jira/limiter"},`.
   The limiter imports nothing internal, so it needs no entry of its own; `internal/sync` tests deliberately do not import
   the limiter (the budget test reads `Stats().Requests` by lane index). architecture §2's table should list the package.
2. **`SetDefaultPolicy`'s restore can leave `DefaultPolicy()` nil** (`contract.go`): `SetDefaultPolicy` spends the
   creation `sync.Once` and remembers `prev`, which is nil when the default was never created, so after `restore()`
   `DefaultPolicy()` returns nil for the rest of the process. `NewClient` now falls back to a private default policy so it
   cannot panic; the contract should create the default before swapping. My tests call `DefaultPolicy()` first.
3. **Number drift in docs and comments:** `docs/jira-sync.md` §1 (line 35) still says a token bucket of 10 requests/s;
   §3.1 says 8 (burst 16). `contract.go` comments on `PolicyOptions.RatePerSecond` ("10 (token bucket)"), `Policy`
   ("shared by every Client in the process (global semaphore, token bucket, 429 gate)") and `SetDefaultPolicy`
   ("10/s bucket") are stale: the rate is 8, the bucket lives in the limiter, and the 429 gate is now also per connection
   in the limiter. I changed `defaultRate` to 8 and `TestPolicy_NewPolicy_FillsDefaults` with it.
4. **Spec vs reality, 429 gate scope:** §3.1 says the gate is "for this connection". It is per connection in the limiter
   (fail-fast, Stats, lane order) but the waiting still runs through the process-wide Policy gate (decision 4).
5. **Not wired yet (out of T0's files):** `sync:throttled` events and the `jira.max_rps` hidden setting (feed
   `PolicyOptions.RatePerSecond` through `SetDefaultPolicy`; AIMD never exceeds it); `Test`/search/validate are still Sync
   lane until T4 marks them `WithInteractive`.
