# WP-T5: Jira light writes (transitions, assign, flag, comment)

Spec: `docs/jira-sync.md` §2.15 and §3.1, ADR 0015, `docs/wp-notes/F12-freeze.md` decision 8 and the WP-T5 notes.
Code: `internal/source/jira/writes.go`, fake handlers in `internal/source/jira/jiratest/server_writes.go` (routes added to
`server.go`).

## Status

- Every write test passes except the two `Capabilities().Writes` tests, which are blocked by `contract.go` (see below).
- `layercheck`, `go vet` and the full Go suite are otherwise green.

## How a write is sent

- `writeOnce` calls the client's single-attempt method (`attempt`), with `WithWrite(ctx)`. It never enters the retry loop
  of `do`, so a write is sent exactly once.
- A non-2xx answer is built with `newHTTPError` and mapped by `writeStatusError`. The 400 rule for POST transitions
  reads the body's `errors{}` directly, because `HTTPError.Messages` does not say whether `errors{}` was present.
- A 429 uses `limiter.RetryAfter` on the response headers, so `Retry-After: 0` gives 0 and no headers gives 5 s. The
  write also closes the connection's gate for the next request, as `Client.do` does. `MapWriteError` was not used
  because it turns a zero `Retry-After` into 5 s.
- A refusal by the limiter while the gate is closed (`GateError`) gives `rate_limited` with the remaining wait.
- Transport failure before a response (dial, DNS, TLS, refused) is `offline`. Any other failure is `timeout`, with the
  detail redacted.

## Transitions

- `Transition` takes the per-issue lock, then looks the transition up (an interactive read, not write-marked). It
  refuses a transition that needs a screen, or one the list no longer offers, without posting. The POST is a single
  attempt.

## Known gap (CONTRACT-ISSUE)

- `Adapter.Capabilities()` in the frozen `contract.go` does not set `Writes`. The fix is one line there:
  `Writes: writesCapabilities(a.meta)`, read under `a.mu`. `writesCapabilities` is in `writes.go`.

## Fake

- Transitions use the id `"t"+statusID`. The "Done" transition needs a screen with a required `resolution`, and "Code
  Review" has an optional-only screen. Everything else has no screen.
- A rejected write leaves the dataset unchanged, because every check runs before the first mutation.
- Write ids use the `w` prefix and are chosen so they do not collide with any existing change or comment id.
