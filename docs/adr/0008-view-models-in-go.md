# ADR 0008 — View models are computed in Go; TypeScript only lays out

Status: Accepted

## Context
The pulse track needs changelog folding, timezone-aware day bucketing, days-in-lane, blocked spans, done-window and
speaker-order rules. These could live in TS (as in the prototype) or in Go.

## Decision
`internal/pulse` (pure functions) and `internal/view` build a complete `StandupView` per standup: tickets with lane,
`Pulse{Segs, Marks, Blocked}` in **day-index units**, `DaysInLane`, `OverLimit`, `Blocked`, effective speakers, axis,
done/backlog lists, counts. The frontend filters by selected speaker, groups by lane order, converts day indexes to
pixels, and makes measurement-dependent choices (density ladder, below-fold text, lane balancing, tab collapse).
The whole standup is sent in one payload (also embedded in `/boot.js` at startup) so speaker switching needs no IPC.
Cached Jira activity is stored as raw timestamps and bucketed at view-build time, not at sync time.

The builder is `view.Build(snapshot model.BoardSnapshot, asOf time.Time)`: it never reads the DB or the clock, so the live
view (`cache.LoadCurrent`, now) and a future historical view (`history.LoadAsOf`, past instant) share it unchanged (ADR 0011).

## Consequences
- One tested implementation of all time/changelog logic (table-driven Go tests, golden view JSON); no date maths in JS.
- Timezone/DST changes and setting changes (pulse days, limits, mapping) need no resync — only a rebuild (< 15 ms).
- Payload ≈ 350 bytes per ticket; fine up to several thousand tickets.
- A Go change to the view model shape requires regenerating bindings; CI type-checks the frontend against them.
