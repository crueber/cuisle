# Handoff: zoom for the standup screen (F15)

**For Claude Code.** This is a new feature on the shipped app, designed with Chris on 2026-11-25 (design canvas
Round 11). It builds on F13 (merged). If F14 (command palette) has landed, register the three zoom commands there too.

## What Chris asked for
"A way to zoom in and out on the whole board view. It's sometimes hard to read. It should … zoom in and out on
everything except the navbar… + and − should zoom in and out. It definitely needs to … rebalance the columns for the
whole board view … when zooming in and out." Then: "it should apply to the person view too."

## What it looks like (Read these PNGs first)
| File | Shows |
|---|---|
| `design/mockups/zoom-board-100-1920x1080.png` | **The control:** `− 100% +` in the Whole board title row, after the Assignee \| Status switch. 2 columns. |
| `design/mockups/zoom-board-80-1920x1080.png` | **80%:** the layout behaves as if the window were 2400px wide, so **3 columns**, and everything fits incl. Done and Backlog. Toast: "80% · 3 columns". |
| `design/mockups/zoom-board-150-1920x1080.png` | **150%:** the effective width is 1280, so the rail goes compact, still 2 columns, rows at 20px, and the more bar shows what's below. The control is filled ink because zoom ≠ 100%. |
| `design/mockups/zoom-board-175-1920x1080.png` | **175%:** 1 column, big type for a screen share, and a more bar listing the people below. Toast: "175% · 1 column". |
| `design/mockups/zoom-person-150-1920x1080.png` | **Person view at 150%:** the same zoom and the same control in its title row; the density ladder re-runs on the effective size. **Its Done chips come from an old prototype renderer; follow ux-spec §5 (ladder, then more bar), not the chips.** |

- **Canvas:** https://claude.ai/artifact/ByQrCUJ3PUPfcjdwKG9ASg#page-r11.
- **Header in every mockup:** it stays at native size; only content below it zooms.

## Spec (add as ux-spec §16, product-spec F15, §8 keys, AGENTS §10 decision)

### 16.1 What zooms
- **Zoomed:** everything in the standup screen **below the header**, in both the **person view** and the **Whole
  board**:
  - the speaker rail, the title row and its controls, the axis, lanes and rows, Done, Backlog, the more bar
  - the board/person banners
- **Not zoomed:** the header (wordmark, tabs, Find, Resync, Configure). Also not zoomed: Configure, the ticket modal,
  the command palette, the lightbox and toasts. Those keep the Text size scale only.
- **Effective scale:** content scale = **Text size × zoom**. At Large (TV) 1.25 × 150% = 1.875.

### 16.2 Levels, control and keys
- **Steps:** 70, 80, 90, 100, 110, 125, 150, 175, 200 %. Default 100.
- **Persistence:** **one zoom per standup**, shared by its person view and Whole board, in `standup_ui_state.zoom`.
  Switching standups restores that standup's zoom.
- **Control:** `− 100% +` (30px tall, 2px ink border, radius 3) at the right of the title row.
  - **Whole board:** after the Assignee \| Status switch.
  - **Person view:** before the legend or `? Key`.
  - **Behaviour:** − and + step the level; they are disabled at 70% / 200%.
  - **The middle button** shows the level in mono 14 bold and resets to 100% on click. It is **ink-filled whenever the
    zoom ≠ 100%**, so a zoomed screen is obvious at a glance.
  - **Accessibility:** `role="group"` with `aria-label="Zoom"`; the buttons are labelled "Zoom out (−)", "Reset zoom to
    100% (0)" and "Zoom in (+)".
- **Keys** (standup screen; ignored while typing and when the ticket modal or palette is open):
  - `+` / `=`: zoom in.
  - `−` / `_`: zoom out.
  - `0`: reset to 100%.
  - **⌘ / Ctrl with `+` `=` `−` `0`** do the same, and must call `preventDefault`, so the webview never applies its own
    page zoom. Disable webview zoom in the Wails window options too, if that option exists in the version you pin.
  - **Code order:** the current `handleGlobalKey` returns early on ⌘ or Ctrl, so handle these combinations before that
    check, exactly as F14 does for ⌘K.
- **Feedback:** on every change, a toast below the zoom control (ink fill, mono 15 bold) reads
  `150% · 2 columns` on the board or `150% · side headings` on a person (the chosen density name). It lasts 1.2s and
  sits in `role="status"`.
- **Discoverability:** add to the rail footer hints ("+ − zoom"), the `?` key legend and §8. If F14 exists, add the
  palette commands "Zoom in", "Zoom out" and "Reset zoom" (category View, shortcuts `+`, `−`, `0`; detail "now 150%").

### 16.3 Layout: zoom re-runs the responsive rules
Implement zoom as a CSS scale, **not** a `transform`. Every rule already works in "CSS px divided by `--scale`", so:
- **CSS:** the header keeps `--scale: var(--text-scale)`. The content wrapper (rail + main) sets
  `--scale: calc(var(--text-scale) * var(--zoom))`, and all its sizes use `--scale`. Today header and content share one
  `--scale`; split it here.
- **Layout maths:** `useViewport()` (or its caller) returns `contentScale = textScale × zoom` for the content. Every
  layout function receives width and height divided by it:
  - `railMode(vw)`, `trackWidth(mainW)`, `legendMode`, `boardLegendMode`
  - the density ladder (`chooseDensity`), `belowFold`, `laneRowHeight`, `doneBacklogLayout`, `statusLayout`
- **Board columns:** extend `boardColumns(mainW)` to **`1 | 2 | 3`**: 3 when `mainW ≥ 1720`, 2 when `≥ 1150`, else
  1, using the effective `mainW`. `doneBacklogLayout` follows: with 3 columns, Done uses 3 grid columns and Backlog 4.
- **Column split, assignee mode:** **replace the greedy `balanceLanes` with F13's order-preserving `splitSequence`**.
  Unassigned first, then speakers in speaking order, cut into `cols` contiguous runs that minimise the tallest column.
  Speaking order then always reads down, then across, at any column count.
  - **This changes today's 2-column split slightly:** on the sample it becomes Unassigned, Iris, Silas | Noor, Otto,
    Amara, Jonas, Wren. Update ux-spec §6.2 and its golden cases.
  - Status mode already uses `splitSequence`; it just needs `cols` up to 3.
- **Row height:** the 23 → 20 fallback uses the effective available height, so zooming in naturally goes to 20px rows
  before the more bar appears. **Nothing is ever hidden to fit**; past the ladder, the more bar takes over as today.
- **Keeping your place:** when zoom changes, keep the topmost fully visible lane (board) or group (person) at the top
  of the scroll area after reflow. Don't jump to the top.
- **Speed:** reflow on zoom must be ≤ 1 frame for layout maths and ≤ 50ms to paint (existing switch budgets).

### Data and API
- **Migration `0007_zoom.sql`** (0006 was taken by custom lanes while this was designed): `ALTER TABLE standup_ui_state ADD COLUMN zoom INTEGER NOT NULL DEFAULT 100 CHECK (zoom
  IN (70,80,90,100,110,125,150,175,200));`
- **Settings:**
  - `UIState.Zoom map[int64]int` (absent = 100).
  - `UIStatePatch.Zoom *int`, applied with `StandupID` like `BoardGroup`.
  - Regenerate the bindings and update `api/mock.ts`.
- No `StandupView` change.

## Work (small; 2 Haiku WPs or do it directly)
| WP | Owns | Done when |
|---|---|---|
| Z1 | migration `0007`, `internal/settings` (UIState/patch), bindings, mock | migration test (existing rows → 100; CHECK rejects 105); per-standup round-trip |
| Z2 | `styles` (split `--scale` into text vs content), `useViewport`/content scale, `layout/board.ts` (`boardColumns` 1–3, `splitSequence` for assignee mode, `doneBacklogLayout` at 3), the zoom control, keys, toast, scroll anchoring, hints/legend, palette commands | Vitest golden cases: effective sizes at 80/100/150/175 give 3/2/2/1 columns and the mockups' splits; person ladder at 150% on 1920×1080 = side headings; Playwright screenshots vs `zoom-*.png`; ⌘+ never zooms the header; axe clean |

## Build notes (2026-11-25)
- Built in full; see ux-spec §16, AGENTS §10 and the F15 commits. Differences from this handoff: migration `0007` (not 0006),
  the 3-column tie rule follows `zoom-board-80` (ux-spec §6.2), the legend's `full` threshold is unchanged (only `short`
  rises by 150px for the control), toasts do not zoom, and the shell's default menu loses its Zoom items (`cmd/cuisle/menu.go`).

## Ground rules
- AGENTS invariants. Tokens only, no new dependencies, budgets hold.
- Commit as `F15: zoom for person view and whole board`.
- Push to Forgejo only when Chris asks. Never push to GitHub.
