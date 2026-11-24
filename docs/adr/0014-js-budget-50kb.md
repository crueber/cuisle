# ADR 0014 — Raise the JS bundle budget to 50 KB gzip

Status: Accepted (2026-11-23, maintainer). Amended 2026-11-23: JS gate raised to 70 KB gzip for F12 (see below).

## Context
ADR 0002 set the frontend JS budget at 45 KB gzip, checked by `size-limit` in CI (architecture §6.1). By the end of v1
the bundle was 45.33 KB:
- **The Wails runtime (about 10 KB gz) is fixed cost.** Its index module has side effects (context menu, drag, app
  region and the runtime-ready event) that the app depends on.
- **The rest is product surface the maintainer asked for during UAT:** visible sync progress, ordering statuses within
  a section, removing any speaker, and the first-sync notice.
- **The cheap savings were already taken:**
  - terser minification;
  - one shared icon set, with unused icon names dropped;
  - de-duplicated helpers.
- **Further deduplication doesn't help.** Factoring the repeated ↑/↓ markup into a component made the gzip output
  larger, because gzip already compresses repeats.

Cold start, the budget users actually feel, is 363–398 ms against 500 ms on macOS with 5 standups. Startup isn't near
its limit; the JS gate is the only one blocking.

## Decision
- The JS gate becomes **50 KB gzip** (`frontend/.size-limit.json`). The CSS budget (12 KB) and every other §6.1 budget
  stay as they are.
- Runtime dependencies remain exactly `preact`, `@preact/signals` and `@wailsio/runtime` (invariant 10). The headroom
  is for product code, not new libraries.

## Consequences
- About 4.7 KB of headroom for post-v1 features.
- The cold-start and switching budgets still guard the experience. If the bundle growth shows up there, revisit this
  before raising the limit again.
- Before adding a large UI area (for example the History view), consider loading it lazily. The gate counts all JS,
  but a lazy chunk keeps first paint lean.

## Amendment (2026-11-23): 70 KB for the ticket modal (F12, ADR 0015)
Chris raised the JS gate to **70 KB gzip** for all JS. The ticket modal and the ADF renderer are expected to add about
20 KB. Splitting `views/ticket` and `components/adf` into a chunk prefetched at idle is recommended, so cold start stays
flat, but it isn't required. The runtime dependency rule (invariant 10) is unchanged.

## Amendment (2026-11-23): CSS 18 KB for the ticket modal (F12)
With the ticket modal and the ADF renderer mounted, the CSS bundle measures 15.7 KB gzip against the 12 KB gate set in
ADR 0002 (v1 used 10.5 KB). Chris raised the CSS gate to **18 KB gzip**, leaving about 2 KB of headroom, with no visual
changes. CSS is one file, so this has little effect on cold start.
