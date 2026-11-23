# ADR 0004 — Explicit sync, no background polling

Status: Accepted (supersedes the "Refresh 1/2/5 min" control in the mockups)

## Context
A standup board should not shift under the presenter, and the app must open instantly and work offline from cache.
Polling costs rate-limit budget and makes the display non-deterministic.

## Decision
Jira is contacted only when:
1. the user presses **Resync** (header button, key `R`; `Shift+R` = full);
2. the app opens and the app-wide policy `sync_on_open` applies: `stale` (default; older than `sync_stale_hours`, default 6),
   `always`, or `never`;
3. the user changes a standup's source in Configure (space, board, scope, extra JQL, column mapping) — a full sync of that
   standup starts when they leave Configure (picking a board and validating the extra filter also call Jira);
4. the user types in a Jira-backed combobox or connects an account.

There are no timers that call Jira. The only timer is the local-midnight tick that re-renders dates.
Sync is scan-and-diff with an atomic apply (ADR 0009); the header always shows the age of the data ("synced 2 h ago").

## Consequences
- Data can be stale; the age is always visible and one keypress refreshes it.
- No rate-limit pressure while idle; predictable behaviour on a shared screen.
- The Display section shows "Sync on open: When stale / Always / Never" instead of a refresh interval.
