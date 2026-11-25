# Handoff: Whole board, group by Assignee or Status (F13)

**For Claude Code.** This is a small feature on top of the shipped app. Chris designed it with Claude on 2026-11-25 and
picked **option A ("status sections")**. It is mostly frontend layout. The only backend change is one saved
per-standup setting.

## What Chris asked for
"The Board View needs a way to switch between an assigned view and a status view that shows which status tickets are
in."

## What it looks like (Read these PNGs first)
| File | Shows |
|---|---|
| `design/mockups/board-status-1920x1080.png` | **The main reference.** Whole board grouped by status at 1920: QA → In Review → In Progress in column 1; To Do, then Done and Backlog, in column 2. Each section header carries a lane swatch and red flags. Rows gain an assignee column, and unassigned rows show a dashed red `··`. |
| `design/mockups/board-status-1280x720.png` | The same at 1280×720: compact rail, compact headers, the more bar at the bottom. |
| `design/mockups/board-assignee-toggle-1920x1080.png` | Today's board (grouped by assignee) with the new **Assignee \| Status** switch in the title row. Nothing else changes in that mode. |

- **Canvas:** Round 8 at https://claude.ai/artifact/ByQrCUJ3PUPfcjdwKG9ASg#page-r8.
- **Option B was rejected.** The columns variant is on the canvas, but don't build it.
- **Mockup vs real app:** the mockups use the Saffron sample data with the tab labelled "Saffron Mobile". The written
  spec below wins over the mockups on any detail. Example: keep the existing 36px days chip and the `titleW` rule from
  ux-spec §6.2.

## Spec (add as ux-spec §6.5, product-spec F13, and AGENTS §10 decision)

### 6.5 Group by Assignee / Status
- **Switch:** a Segmented control `Assignee | Status` (the existing `Segmented` component, 30px, 2px ink border,
  selected = ink fill). It sits in the Whole board title row, right-aligned and before the legend, followed by a `G`
  kbd hint.
  - Under 1360 (no legend) it sits before the `? Key` button.
  - The stats line keeps its ellipsis rule; the legend gaps tighten to 11px and the swatches to 18px, so at 1920 the
    full stats line fits.
- **Key:** `G` toggles the grouping on the Whole board. On a person view it does nothing. It is ignored while typing
  and while the ticket modal is open. Add it to the key legend (`?`) and the rail footer hints ("G group by").
- **Persistence:** per standup, in `standup_ui_state.board_group` (`'assignee' | 'status'`, default `'assignee'`).
  Switching standups restores each standup's choice. Toggling persists immediately (UI state, not Configure; no save
  indicator).
- **Assignee mode:** exactly today's §6.2–6.4.
- **Status mode:**
  - **Sections:** one per lane in **walk order** (`StandupView.Lanes`, the same order as the person view). Then Done,
    then Backlog, both unchanged from §6.3. A lane with no active tickets gets no section.
  - **Section header (30px):**
    - a 14px lane swatch
    - the lane name: 15px bold, caps, 0.12em tracking
    - mono 13 muted: `7 tickets` (just `7` below 1360)
    - then, only if non-zero, in alert-text 600: `· 2 over limit` (`· 2 over` below 1360), `· 1 blocked`,
      `· 1 unassigned`
    - right-aligned, mono 12 muted: `limit 3d · longest first`. Below 1360 only `limit 3d`; it is omitted when the
      lane limit is 0.
    - a 2px ink rule under the header. Person lanes use a soft rule after the name; the stronger rule marks a status
      group.
  - **Rows:** the §6.2 lane row plus an **assignee column**: 30px wide, between the title and the track.
    - It shows the speaker's initials (mono 12 bold), with the full name in `title`.
    - An unassigned row shows a 30×18 box with a 1.5px dashed alert border and `··` in alert-text.
    - `BLOCKED` stays after the title, as in the person view.
  - **Row order within a section:** over-limit first, then `daysInLane` descending, then board rank (stuck work
    first).
  - **Columns:** use the same `boardColumns(mainW)` rule (2 at ≥1150). The split keeps order: the sections, then Done,
    then Backlog form one sequence, cut once into column 1 | column 2. Choose the cut that minimises the taller
    column. Done and Backlog render as two-column grids inside a column.
    - Each column has its own axis row, as today.
    - In one-column mode everything stacks.
    - The mockup's split is QA, In Review, In Progress | To Do, Done, Backlog.
  - **Row height:** the same 23 → 20 rule as §6.2, applied to the taller column.
  - **More bar:** list the sections whose header is below the fold by lane name, e.g.
    `In Progress (10) · Backlog (12) below`. In the count, Done and Backlog follow the existing rules.
  - **Ticket modal stepping:** `J`/`K` follow the on-screen order (section order, then row order), then Done, then
    Backlog.
  - **Title-row stats are unchanged** (the same counts in both modes).

### Data and API
- **No `StandupView` change.** `TicketView` already has `lane`, `assignee`, `daysInLane`, `overLimit`, `blocked`.
  Grouping and sorting are layout (ADR 0008: Go computes data, TS lays out).
- **Migration `0005_board_group.sql`:** `ALTER TABLE standup_ui_state ADD COLUMN board_group TEXT NOT NULL DEFAULT
  'assignee' CHECK (board_group IN ('assignee','status'));`
- **Settings:**
  - `UIState.BoardGroup map[int64]string` (standup id → mode; absent = assignee).
  - `UIStatePatch.BoardGroup *string`, applied together with `StandupID` like `LastPerson`.
  - Regenerate the bindings and update `api/mock.ts`.
- **Docs:** data-model (§2.1 `standup_ui_state`), architecture (UIState), ux-spec §6.5 + §8 (`G`), product-spec F13.

## Work (small; dispatch as 3 Haiku WPs or do it directly)
| WP | Owns | Done when |
|---|---|---|
| G1 | migration `0005`, `internal/settings` UIState and patch, bindings, `api/mock.ts` | migration test (existing rows default to `assignee`); patch round-trip; per-standup isolation |
| G2 | `frontend/src/layout/board.ts`: `statusSections(view)`, `sortStatusRows`, `splitSequence(heights, cols)` | Vitest golden cases: mockup data → QA, Review, Prog \| ToDo, Done, Backlog; empty lane omitted; one column at `mainW` < 1150; sort order; 20px fallback |
| G3 | `views/standup/BoardView*` status mode, `StatusSectionHeader`, the assignee column in `TicketRow` (board variant), the title-row Segmented, `G` in `keys.ts`, key legend, rail hints, more bar, modal step order | component tests + Playwright screenshots vs `board-status-*` and `board-assignee-toggle-*`; axe clean (the Segmented is a group with `aria-pressed`); `G` persists across restart |

## Ground rules
- AGENTS invariants apply. Nothing is hidden to fit: status mode uses the same density then more-bar behaviour.
- Tokens only.
- No new runtime dependencies.
- The JS budget holds (this should be well under 2 KB gz).
- Commit as `F13: whole board group by status`.
- Push to Forgejo only when Chris asks. Never push to GitHub.
