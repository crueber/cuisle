# Cuisle: UX spec

The approved look is in `design/mockups/*.png`, and static DOM snapshots with exact inline styles are in
`design/reference/*.html` (both kept in the private development repository, not the published mirror). This document is the contract: where a mockup and this text disagree, **this text wins**.
Known differences from the mockups are listed in §13.

All sizes are CSS px at Standard text size. `vw` and `vh` are the window's inner size. `mainW` is the width of the main
content column (window minus rail minus horizontal padding). Tokens are named as `--token` (see `design/tokens.json`).

---

## 1. Screen inventory

| Screen | Mockups |
|---|---|
| Standup: person view | `person-1920x1080.png`, `person-1440x900.png`, `person-1280x720.png`, `person-1024x640.png`, `person-unassigned-1920x1080.png` |
| Standup: whole board | `board-1920x1080.png`, `board-1440x900.png`, `board-1280x720.png`, `board-1024x640.png`, `board-case-team-1920x1080.png` |
| Standup: whole board, group by (F13, §6.5) | `board-status-1920x1080.png`, `board-status-1280x720.png`, `board-assignee-toggle-1920x1080.png` |
| Configure | `configure-1920x1080.png`, `configure-1440x900.png`, `configure-1280x720.png`, `configure-1024x640.png` |
| Configure: new standup | `configure-new-standup-1920x1080.png` |
| Connect (first run) | not mocked; see §9.7 |
| Ticket modal (§14) | `ticket-1920x1080.png`, `ticket-1440x900.png`, `ticket-1280x720.png`, `ticket-1024x640.png`, `ticket-edit-states-1920x1080.png`, `ticket-lightbox-1920x1080.png` |

## 2. Frame (standup screen)

```
┌ header 64px (3px bottom rule) ─────────────────────────────────────────────────────────────────┐
│ CUISLE │ [1 Saffron Mobile MOB] [2 Web & Checkout WEB] … [+]           Mon, 23rd of Nov     synced 2 min ago [↻ Resync] [Configure] │
├ rail ─┬ main (scrolls) ───────────────────────────────────────────────────────────────────────────┤
│ Whole │ title row (pinned)                                                                        │
│ board │ axis row (pinned)                                                                          │
│ …     │ content                                                                                    │
│       │ [more bar, pinned to bottom when content overflows]                                        │
└───────┴───────────────────────────────────────────────────────────────────────────────────────────┘
```

### 2.1 Header
- **Wordmark:** "CUISLE", 20px bold caps, tracking `--font-tracking-wordmark`. Then a 2×24 rule, then the tabs (§4).
- **Date:** stands alone, centred in the free space between the tabs and the right cluster (flexible spacers on both sides).
- **Right cluster, in order:** the sync status, then **Find** (§15), then Resync, then **Update** (only when a newer
  release is available), then Configure.
- **Find** (F14): the same outlined 44px button with a search icon, "Find" and a `⌘K` chip (`Ctrl K` on Windows and
  Linux); icon-only below 1280; ink-filled while the palette is open. aria-label "Find a ticket or run a command (⌘K)"
  (`(Ctrl+K)` off macOS). `rightClusterWidth` adds 132px (44 icon-only) plus the 16px gap, so the tabs give way and
  nothing overlaps or scrolls sideways.
- **Sync status:** "synced 2 min ago", mono 14px muted, followed by the **Resync** button (↻ and "Resync", the
  same bordered 44px button as Configure; icon-only below 1280; aria-label "Resync (R)"). Ages read "2 min", "3h", "2 days".
  - While syncing it changes to "Syncing · issues 120/300" with a Cancel button (× and "Cancel", same style).
  - After a partial run it shows ⚠ "synced 2 min ago · partial", and clicking opens the warnings.
  - When an automatic sync-on-open fails offline, it reads "offline · data from 3h ago" in `--color-alert-text`, with
    no banner. Banners are only for failures of a sync the user started (and for auth failures, which block every sync).
- **Date:** "Mon, 23rd of Nov" (Sun, Mon, Tues, Wed, Thurs, Fri, Sat; ordinal day; short month), mono 18px bold, hidden below 1360.
- **Update** (ADR 0016): shown only in a build with an update channel (GitHub or Forgejo releases) when a newer
  release is known; never in local/dev builds. The same outlined 44px button as Resync and Configure
  (`.sf-header__configure`), with a download icon and "Update"; icon-only below 1280. aria-label "Update to vX.Y.Z",
  tooltip "Cuisle vX.Y.Z is available". It takes its width from the tabs (`rightClusterWidth(collapse, true)` adds
  124px, 44 icon-only, plus a 16px gap), never from the page: nothing scrolls sideways. Clicking it opens a small
  dialog on the ticket modal's scrim (click outside or Esc closes it, except while installing):
  - title "Update Cuisle", then "v1.2.0 → v1.3.0" (mono), a **What's new** link (the release page in the browser),
    then **Not now** and **Install and restart** (primary).
  - While installing: "Downloading… 42%" with a thin bar, then "Verifying the download…", "Installing…",
    "Restarting…". Buttons are disabled.
  - Failures in `--color-alert-text`: "Couldn't download the update. Check your connection and try again.",
    "Couldn't verify the download, so nothing was installed.", "Couldn't install the update. Cuisle is unchanged."
    The buttons become **Close** and **Try again** (user-initiated; nothing retries by itself). If the new version is in
    place but the automatic restart failed: "Cuisle vX.Y.Z is installed. Quit and open Cuisle again to finish." with
    **Close** only.
  - When this copy can't replace itself (a .deb install, a read-only or translocated macOS app), the dialog says so and
    the primary button is **Open the release page**.
- **Configure:** an outlined 44px button with a sliders icon and "Configure". `,` also opens it.
- **What collapses as the window narrows:**

| Condition | Behaviour |
|---|---|
| `vw < 1700` | The sync *label* hides; the ↻ button stays, with the label moved to its tooltip. |
| `vw < 1360` | The date hides. |
| `vw < 1280` | Configure becomes a 44×44 icon button. |

### 2.2 Speaker rail
- **Full rail (`vw ≥ 1360`), 208px wide, `--color-rail-bg`, 2px right rule:**
  - "Whole board" button (44px, bold 17px; selected = inverted).
  - "SPEAKING ORDER" caps label.
  - One 44px button per included speaker: first name (18 semibold; the full display name when two speakers share a first
    name), active count (mono 14, right). No initials here (maintainer, 2026-11-23); the compact rail keeps them.
  - 8px gap, then **Unassigned** with a 2px dashed `--color-unassigned-border` and its count. It's hidden when
    `unassigned_position = skip`, and sits first in the list when `first`.
  - Footer hint (mono 12 muted): "↑ ↓ speaker · ← → standup · B board ⇄ person · G group by · + − zoom · 1–N jump ·
    ⌘K find".
- **Compact rail (`vw < 1360`), 76px:** a whole-board icon button, then per speaker the initials (mono 15 bold) with the
  count beneath (mono 11). The full name goes in `title` and `aria-label`. Unassigned shows as "Unas." + count, dashed.
- The rail scrolls vertically if it overflows. It never hides people.

### 2.3 Main area
- Padding: left/right 32px if `vw ≥ 1600`, else 20px. Top 20px if `vh ≥ 800`, else 12px. Bottom 10px.
- Vertical scroll container (`overflow-y:auto`). The title and axis rows are `position:sticky; top:0` with `--color-bg`
  behind them.

## 3. Responsive rules (summary)

**Nothing scrolls sideways, ever** (AGENTS invariant 16): not the window, a screen, a panel, the ticket modal or any box
in it. Content wraps, tightens or stacks; scrolling is vertical only.

| Rule | Value |
|---|---|
| Rail | Full 208px when `vw ≥ 1360`, else compact 76px |
| Tabs | Full, then key-only, then switcher menu, by measured fit (§4) |
| Legend | Full when `mainW ≥ 1560`, short (4 lane swatches + blocked + over-limit) when ≥ 1300, else a "? Key" button that opens a popover |
| Person track width | 600px when `mainW ≥ 1500`, 440px when ≥ 1150, else 300px; never wider than the row leaves (`mainW − 360`, at least 100), so a high zoom on a small window shrinks it instead of scrolling sideways (§16.3) |
| Axis labels | Every day number; Mondays bold `--color-fg`; weekends `--color-muted` (the weekend backdrop marks them; `--color-rule-mid` fails text contrast, §10). If a day is narrower than 18px, label Mondays only. |
| Board columns | 3 when `mainW ≥ 1720`, 2 when ≥ 1150, else 1 (F15) |
| Board Done/Backlog | With 3 board columns, Done in 3 grid columns and Backlog in 4. Otherwise side by side when `mainW − 40 ≥ 1100`; Done in 2 columns and Backlog in 3 when that width ≥ 1300 (Backlog 2 at ≥ 1000), else 1 |
| Title sizes | h1 36px (person) / 30px (board) when `vh ≥ 800`, else 28 / 24. Title row 56px / 48px, or 46 / 40. |
| Configure | 3 columns when `mainW ≥ 1500`, 2 when ≥ 960, else 1 (§9.2) |

Breakpoints are also in `design/tokens.json` under `breakpoint`. They are evaluated in CSS px, so OS zoom, **Large (TV)**
text size (`--text-scale: 1.25`) and the screen's own **zoom** (§16) move the app into smaller or larger layouts
automatically: everything below the header is laid out on the window divided by text size × zoom.
The zoom control now sits beside the legend in the title row (person view and Whole board), so the **short** legend's
threshold also rises by 150px (on top of the lane-name allowance). The `full` threshold stays 1560: the default view keeps
its full legend beside the control.

## 4. Standup tabs (`layout/tabs.ts: tabMode`)

- **Full tab:** a 2px-bordered number box (mono 12 bold), the standup name (18px; bold when active), and the Jira key
  (mono 12 muted). The active tab has a 4px `--color-fg` bottom border that sits over the header rule.
- **Key-only tab:** the number box plus the key (mono 16). The full name goes in `title` and `aria-label`.
- **Menu:** one button, "[n] Saffron Mobile 1/5 ▾", followed by "1–N to switch" (mono 12 muted). It opens a
  `Combobox` with `variant="menu"` listing all standups, type-to-filter.
- **Choosing the mode:** measure each tab's natural width in both forms. Use `full` if the full widths sum to no more
  than the available width, else `key` if the key widths fit, else `menu`. Available width = header width − wordmark
  block − right cluster − 56px (the `+` button).
- With more than 7 standups in `full` or `key` mode, show the first 7 and then "+N more ▾", which opens the menu. The
  active standup is always visible (it swaps into the last visible slot).
- `+` (aria-label "Add a standup") opens Configure on a new draft.
- **Drafts (no board chosen yet) are not tabs** and take no number shortcut. They appear only in Configure's standups
  rail, marked DRAFT. A draft joins the tabs, at its rail position, once a board is chosen.
- Switching a standup keeps the person selection if that person is a speaker there; otherwise it shows the whole board.

## 5. Person view

### 5.1 Title row (pinned)
- h1: the person's name (36/28px bold). Then mono 15 muted: "14 active · 6 done".
- If the density isn't step 1, a small outlined tag names it (§5.5).
- The legend is right-aligned (§3).

### 5.2 Axis row (pinned, 30px)
- Left, right-aligned: "last 21 days" (13px muted). Then the axis, exactly the track width, with a 2px
  `--color-rule-mid` bottom rule and day labels centred per day. Then a 48px spacer above the days chip column.

### 5.3 Groups
Groups appear in **walk order** (only lanes that have tickets), then **Done** last when it has tickets. Group names are
the lanes' configured labels, and lanes you named (§9.4 Status mapping) are groups like any other. The header legend
lists the same lanes; when their names need more room than the built-in four, it falls back to short and then to the
**? Key** button sooner (`legendExtra`), and a long name in the side-heading gutter wraps there.

- **Stacked headings** (roomy, compact): a heading row of `--size-head-*` height with the lane name (14px bold caps),
  the count (mono 13 muted), and a 3px bottom rule (`--color-rule`; Done uses `--color-rule-mid` and `--color-done-fg`).
  Rows follow.
- **Side headings** (the `*-side` steps): each group is a flex row with a 3px top rule and a 4px gap above. Left is a 118px
  gutter with "QA 3" (12px bold caps + mono count) aligned to the first row. Right is the rows, behind a 2px left rule
  in `--color-rule-soft`.

### 5.4 Ticket row

| Part | Spec |
|---|---|
| Height | Density row height (46 / 38 / 34, §5.5), `box-sizing:border-box`, 2px bottom rule `--color-rule-soft` |
| Icon | 18px issue-type icon, stroke 2.4, colour `--color-type-*` (Done: `--color-done-icon`). Story = bookmark, Bug = bug, Task = check-square, Spike = bolt, other = circle. |
| Key | Mono, 15 / 14 / 14px semibold (by row height 46 / 38 / 34), width `calc(<maxKeyLen>ch + 6px)` across the view so the columns align, no wrap |
| Title | Sans 20 / 18 / 17px semibold (by row height), one line, ellipsis. Full title in `title`. |
| BLOCKED tag | Before the title when blocked (maintainer, 2026-11-23): mono 11 bold, tracking .08em, `--color-tag-bg/fg`, 2px radius |
| Pulse track | Track width × row height (§7) |
| Days chip | 48px wide, mono 14 bold, centred, "6d" (minimum "1d"). Over limit or blocked: `--color-alert-bg/fg`. Otherwise muted text on no background. Done: `--color-done-fg`. |
| Done rows | Same anatomy, text in `--color-done-fg`. The track shows segments only (no marks): earlier lanes in `--color-lane-done-history`, the Done segment in `--color-lane-done`. |
| Interaction | The whole row is a button; Enter or click opens the ticket modal (§14). Hover/focus shows a `--color-rail-bg` background. |

### 5.5 Density ladder (`layout/density.ts: chooseDensity`)

Two independent knobs, tried in order of readability. **Row height matters more than heading style.**

| Step | Row height | Headings | Title / key font | Head cost per group |
|---|---|---|---|---|
| 1 `roomy` | 46 | stacked | 20 / 15 | 40 |
| 2 `roomy-side` | 46 | side gutter | 20 / 15 | 7 (3px rule + 4px gap) |
| 3 `compact` | 38 | stacked | 18 / 14 | 32 |
| 4 `compact-side` | 38 | side gutter | 18 / 14 | 7 |
| 5 `tight-side` | 34 | side gutter | 17 / 14 | 7 |

```
H = height of the scroll container below the pinned title + axis rows   // mockup formula: vh − 64 − padTop − titleH − 30 − 10
need(step) = Σ over all groups incl. Done (step.headCost + group.count × step.row)
density = rows_mode == 'roomy' ? roomy : first step with need(step) ≤ H, else tight-side
scroll  = need(density) > H
```
- Never drop rows, never collapse Done to chips or a count, never truncate the list.
- The title-row tag shows the step's plain name ("roomy rows, side headings", "compact rows", …) whenever it isn't
  step 1.
- **Golden cases** (14 active in 4 lanes + 6 done = 5 groups, 20 rows; see testing.md):

  | Window | H | Density | Scrolls? |
  |---|---|---|---|
  | 1920×1080 | 900 | `compact-side` (need 795) | no |
  | 1440×900 | 720 | `tight-side` (715) | no |
  | 1280×720 | 558 | `tight-side` | yes |
  | 1024×640 | 478 | `tight-side` | yes |

  The mockups predate steps 2 and 4: `person-1920x1080.png` shows 34px side rows, where the spec now gives 38px.

### 5.6 More bar (`layout/below.ts: belowFold`)
- Shown only when `scroll` is true. It's 36px, `position:sticky; bottom:0`, 8px margin-top, an outline: page-coloured
  `--color-more-bar-bg`, a 1px `--color-rule-mid` border and muted `--color-more-bar-fg` text (a filled bar pulled the
  eye; changed 2026-11-24), radius 3px, centred: "⌄ Scroll for more" (15px bold) plus a mono 13 summary.
- **Summary:** walk the groups top to bottom with fold = visible height − 36. For each group, count rows whose bottom
  edge is past the fold, and join as "2 To Do · 6 Done below".
- Recompute on scroll (throttled to one animation frame).
- At the end of the list (`scrollTop + clientHeight ≥ scrollHeight − 6`) the label changes to "End of list" with
  the summary "every ticket is shown above". Keep the bar's height so the layout doesn't jump.

### 5.7 Empty and edge states
- No tickets: "Nothing on the board for this person." (20px muted, 40px top padding).
- Unassigned person view: the title is "Unassigned", and the rows are the same.
- A speaker excluded from the order still appears on the whole board if they have tickets. Clicking their lane title
  opens their person view.

## 6. Whole board

### 6.1 Title row (pinned)
- "Whole board" h1. Then mono 14 muted, one line with ellipsis: "Sprint 42 · ends today · 40 active ·
  **4 unassigned** (alert-text) · 15 done · 40 backlog". Kanban shows "Kanban · continuous flow".
- On the right: the **Assignee | Status** switch and its `G` hint (§6.5), then the **zoom control** (§16.2), then the
  legend.

### 6.2 Lane columns (`layout/board.ts: boardColumns, splitLanes, splitSequence`)
- **Columns:** `cols = mainW ≥ 1720 ? 3 : mainW ≥ 1150 ? 2 : 1`, with a 40px gap. `mainW` is the **effective** width
  (§16.3), so zoom moves the board between 1, 2 and 3 columns.
- **Column assignment (F15; replaces the greedy balance):** the sequence is *Unassigned* (if it has tickets) first, then
  the speakers in **speaking order** (excluded speakers with tickets go after included ones). It is cut into `cols`
  contiguous runs that minimise the tallest column (`splitSequence`, the same cut as Group by Status, §6.5). Ties: column
  1 fills first (the later first cut wins); with three columns the last column then takes as much as fits and the middle
  one what is left (the earlier second cut wins), and a middle column is never left empty while the last one is not.
  Lane height = `30 + rows × laneRowH + 8`. Speaking order therefore always reads down, then across, at any column
  count. The prototype sorted by size, and the first build balanced greedily; both are deliberate changes.
  - MOB sample at 23px rows: 2 columns are Unassigned, Iris, Silas | Noor, Otto, Amara, Jonas, Wren (620 | 604);
    3 columns are Unassigned, Iris | Silas, Noor | Otto, Amara, Jonas, Wren (490 | 260 | 474), the split in
    `zoom-board-80` (Silas, Noor, Otto | Amara, Jonas, Wren, 367 | 367, is as short but loses the tie).
- **Lane row height:** 23px, or 20px if the tallest column exceeds the available height at 23px (the effective height,
  so zooming in goes to 20px rows before the more bar appears).
- **Per-column axis row (30px):** "last 21 days" in a left block as wide as icon + key + title, then a positioned
  label at each Monday ("Mon 16", or "16" if days are narrower than 16px) with a 2px left tick, then "today" at the right.
- **Lane header (30px button):** name (17px bold), "4 active · 2 done" (mono 12 muted), and a 2px `--color-rule-soft`
  rule filling the rest. Unassigned uses `--color-alert-text`, "4 need an owner", and a 2px dashed rule.
- **Lane row:** icon 14px, key (mono 13, `ch` width), title (15px semibold, width `titleW`), thin track (flexible
  width, segments 11px tall plus blocked hatch, no marks, 2px today line), days chip (36px, mono 13).
  - `titleW = mainW ≥ 1500 ? 250 : clamp(colW − keyW − 14 − 36 − 32 − 240, 150, 300)`.
- **Lane order:** walk order lanes, then nothing else (Done is listed separately in §6.3).

### 6.3 Done and Backlog
- **Done:** a section heading "DONE" plus "15 this sprint" (or "14 days" / "since Friday"), with a 3px
  `--color-rule-mid` rule. Then *every* Done ticket in the window, 26px rows: icon (done colour), key, title, assignee
  initials (mono 12, right). Laid out in grid columns, filling top-to-bottom (3 grid columns with three board columns,
  else 2 or 1, §3).
- **Backlog:** "BACKLOG" plus "top 12 by rank", with a 3px `--color-rule`. Rows are icon, key (muted) and title. If
  more exist: "Showing the top 12 as set in Configure · 28 more ranked below in Jira" (13px muted). Hidden entirely
  when backlog rows = 0.

### 6.4 More bar
Same component as §5.6. Summary: lanes whose header is below the fold (first names), then "Done (15)" and "Backlog (12)"
if those sections start below the fold.

### 6.5 Group by Assignee / Status (F13; `layout/board.ts: statusSections, sortStatusRows, splitSequence, statusLayout`)
- **Switch:** the shared `Segmented` in its `toggle` mode (`Assignee | Status`: a `group` named "Group the board by" of
  two `aria-pressed` buttons, so the arrow keys stay the screen's keys; 30px, 2px ink border, selected = ink fill) in
  the Whole board title row, right-aligned and before the legend, followed by a `G` kbd hint. Under 1360 (no legend) it
  sits before the `? Key` button. The stats line keeps its ellipsis rule; the legend gaps tighten to 11px and its
  swatches widen to 18px, and the board's legend is always the short one (the lane tracks draw no marks, so the commit /
  PR / comment keys never belong here), so at 1920 the full stats line fits.
- **Key:** `G` toggles the grouping on the Whole board. On a person view it does nothing. It is ignored while typing and
  while the ticket modal is open. It is in the key legend (`?`) and the rail footer hints ("G group by").
- **Persistence:** per standup in `standup_ui_state.board_group` (`'assignee' | 'status'`, default `'assignee'`).
  Switching standups restores each standup's choice. Toggling is written at once (UI state, not Configure; no save
  indicator).
- **Assignee mode:** exactly §6.2–6.4.
- **Status mode:**
  - **Sections:** one per lane in **walk order** (`StandupView.Lanes`, the same order as the person view; the lane id is
    the header swatch's `data-lane`, so custom lanes need nothing here). Then Done, then Backlog, both as §6.3. A lane
    with no active ticket gets no section.
  - **Section header (30px, its 2px ink rule included):** a 14px lane swatch; the lane name (15px bold, caps, 0.12em
    tracking); mono 13 muted `7 tickets` (just `7` below 1360); then, only if non-zero and in alert-text 600,
    `· 2 over limit` (`· 2 over` below 1360), `· 1 blocked`, `· 1 unassigned`; right-aligned mono 12 muted
    `limit 3d · longest first` (below 1360 only `limit 3d`; omitted when the lane limit is 0). The strong ink rule marks
    a status group; person lanes (§6.2) use a soft rule after the name. The first row starts 3px under the rule and a
    section leaves 4px below its last row.
  - **Rows:** the §6.2 lane row plus an **assignee column**, 30px, between the title and the track (the track gives up
    those 30px and one gap; `titleW` is unchanged). It shows the speaker's initials (mono 12 bold) with the full name in
    `title`; an unassigned row shows a 30×18 box with a 1.5px dashed alert border and `··` in alert-text. `BLOCKED` sits
    after the title (the title truncates first).
  - **Row order in a section:** over-limit first, then `daysInLane` descending, then board rank (stuck work first).
  - **Columns:** `boardColumns(mainW)` (2 at ≥1150). The sections, then Done, then Backlog form one sequence, cut once
    into column 1 | column 2 by the cut that minimises the taller column (ties: the later cut, so column 1 fills first).
    Done and Backlog render as two-column grids inside a column (one grid column when the column is under 480px).
    Each column has its own axis row. In one-column mode everything stacks. At the mockup data this is
    QA, In Review, In Progress | To Do, Done, Backlog.
  - **Row height:** the §6.2 rule, 23 → 20, decided on the taller column at 23 including Done and Backlog (section height
    = 30 + 3 + rows × rowH + 4; Done/Backlog = 36 + 6 + grid rows × 26 + 8).
  - **More bar:** sections in on-screen order. A lane section is listed by name with the number of its rows below the
    fold (`In Progress (10)`), which includes a lane whose header is itself below. Done and Backlog follow §6.4: listed
    by their full label (`Backlog (12)`) when their heading is below the fold.
  - **Ticket modal stepping:** `J`/`K` follow the on-screen order: section order, then row order, then Done, then Backlog.
  - **Title-row stats are unchanged** (the same counts in both modes).

## 7. Pulse track (`layout/track.ts`; inputs from Go `Pulse`)

- **Units:** everything is positioned in **day units** within an N-day window. Day `i ∈ [0, N)`, where `N − 1` is today.
  CSS: `left: calc(var(--x) / var(--days) * 100%)`, `width: calc(var(--w) / var(--days) * 100% − 2px)`.
- **Backdrop** (drawn once per list, behind the rows): weekend days filled with `--color-weekend` when shading is on, plus
  the today line (3px person view, 2px board, `--color-today`) at the right edge.
- **Segments:** one per contiguous lane span, colour `--color-lane-<lane>`, radius 2px, 2px gap between segments. A
  lane the user named (§9.4 Status mapping) is coloured from the extended lane palette `--color-lane-x1…x8`: Go gives
  each such lane a palette index (`LaneInfo.hue`), elements carrying `data-lane` also carry `data-hue`, and one shared
  CSS rule paints segments, legend and Configure swatches, status-section swatches and the ticket modal's chips (status
  lozenges use `--color-lane-x-ink` text). A segment in a lane the config no longer has is drawn as To Do.
  Vertical position by row height:

  | Row | Segment height | Top |
  |---|---|---|
  | ≥ 40 | 12 | row − 18 |
  | ≥ 32 | 10 | row − 16 |
  | else | 8 | row − 14 |
  | lane rows | 11 | 6 |

  The span before the window is clipped at day 0, so a ticket that was already In Progress shows its segment from the left edge.
- **Blocked:** hatched overlay `repeating-linear-gradient(135deg, var(--color-hatch) 0 4px, transparent 4px 8px)` over
  blocked spans, 2px taller than the segment on each side.
- **Marks** (person view only, not on Done rows; each type toggleable):
  - **Commit:** a 4px-wide tick rising from 2px above the segment, height `3 + level × unit`, where
    `unit = floor((segTop − 9) / 3)`, colour `--color-tick`. Level 1–3 comes from the count (1, 2–3, 4+).
  - **PR activity:** an 8px square rotated 45°, centred 12px above the segment top. Drawn instead of the commit tick when
    both happen that day.
  - **Comment:** a 4×6 tick in `--color-tick-comment`, 8px above the segment.
  - Marks are centred in their day. **No marks before the ticket existed.**
- **Accessibility:** the track is `role="img"` with an `aria-label` like
  "In Review 3 days, previously In Progress 6 days; 9 commits, 2 PR updates, 4 comments in 21 days; blocked 3 days".
  Go supplies this string in the view model.

## 8. Keyboard map

| Key | Action | Notes |
|---|---|---|
| `↑` / `↓` | Previous / next speaker (whole board after the last, first speaker after the whole board) | The arrows no longer scroll lists; use the wheel, trackpad or Page Up/Down |
| `←` / `→` | Previous / next standup tab (wraps; keeps the person if they speak there; no-op with one standup) | |
| `B` | Toggle: on a person → the whole board; on the board reached by that `B` (no navigation since) → back to that person | On a board reached any other way it stays on the board |
| `G` | On the whole board: group by Assignee / Status (§6.5). Does nothing on a person view | Remembered per standup; ignored while typing and behind the ticket modal |
| `+` / `=` | Zoom in one step (§16.2) | Standup screen only; ignored while typing and behind the ticket modal or palette |
| `−` / `_` | Zoom out one step | Same |
| `0` | Reset zoom to 100% | Same; `1`–`9` stay the standup digits |
| `⌘` / `Ctrl` + `+` `=` `−` `0` | The same, and always swallowed (`preventDefault`) so the webview never page-zooms | Handled before the modifier early-return, like `⌘K` |
| `1`–`9` | Switch standup by position | |
| `R` / `Shift+R` | Resync incremental / full | |
| `⌘K` / `Ctrl+K` | Open the command palette with the ticket finder (§15). Works everywhere: standup, Configure, in a field, and over the ticket modal (Esc then returns to the modal). Pressing it again closes it | |
| `T` | Same as `⌘K`, when no field has focus (standup and Configure; not inside the ticket modal) | The palette owns every key while it is open |
| `/` | Focus the standup switcher (opens the menu in menu mode, else the first tab) | Still the switcher; the palette's prefixes are `>`, `#`, `@` |
| `,` | Open Configure | |
| `?` | Key legend popover (keys + pulse legend) | |
| `Esc` | Close popover or menu. In Configure with no popup open, the same as Done. | |
| `Enter` | On a focused ticket, open the ticket modal (§14) | `O` in the modal opens Jira |

Global keys are ignored while focus is in an `input`, `textarea` or `[role=combobox]`.

## 9. Configure

### 9.1 Frame
- **Header:** "CUISLE │ Configure". On the right: the **save indicator**, then **Done** (primary button, 44px,
  `--color-button-bg/fg`), which returns to the standup the user came from.
  - **Save indicator states:** idle (nothing); "Saving…" (only if a save takes over 250ms); "Saved" (2s, then fades
    to "All changes saved", muted); "Not saved · Retry" (alert text, Retry is a button).
- **Left rail** (280px at `vw ≥ 1360`, else 230px):
  - "STANDUPS · 5" caps label.
  - One item per standup (min-height 56px): number box, name (17px bold, clamped to 2 lines), "MOB · 7 people"
    (mono 12), and up/down buttons on the right. Selected = inverted.
  - **+ New standup** (dashed).
  - Footer: "Order here = tab order and the 1–N shortcuts."
- **Main:** a title row with the standup name (32px bold) + "standup 1 of 5" (mono 14 muted) + DRAFT tag when new. On
  the right: **Duplicate** and **Delete standup** (alert text) links. Then the section grid. The main area scrolls.

### 9.2 Section layout by column count
Each column is its own container in a CSS grid (`repeat(n, minmax(0, 1fr))`), never CSS multi-column: in WebKit a
multi-column box inside the scrolling main fragmented into extra columns past the right edge. Crossing a column-count
threshold remounts the sections; values live in the config state, so nothing typed is lost.

| Columns | Column 1 | Column 2 | Column 3 |
|---|---|---|---|
| 3 | Source, Person view, Export | Speakers, Display, Connection | Pulse, Whole board, Status mapping |
| 2 | Source, Speakers, Whole board, Export | Person view, Pulse, Status mapping, Display, Connection | |
| 1 | Source, Speakers, Person view, Pulse, Whole board, Status mapping, Export, Display, Connection | | |

With 1 or 2 columns, a sticky **jump bar** of pill links (Source · Speakers · Person view · Pulse · Whole board ·
Status mapping · Export · Display · Connection) sits under the title. Sections have `scroll-margin-top: 64px`.

### 9.3 Shared field anatomy
- **Section heading:** 15px bold caps, a mono 13 muted subtitle, a 3px `--color-rule` bottom rule.
- **Field row:** min-height 50px, 2px bottom rule. Label column 132px (16px semibold, optional 13px muted hint
  beneath). The control column flexes and wraps.
- **Controls:** `Combobox` (every dropdown; architecture.md §4.6), `Segmented` (radiogroup, 34px, selected inverted),
  `Switch` (44×24), `Stepper` (− value +), `Checkbox` (24px, real `role="checkbox"`), `TextField` (38px).
- **Ordered lists** (speakers, walk order, standups): number, then up/down chevron buttons (24×19 each; disabled at the
  ends at 30% opacity), then the item. Keyboard: with an item's handle focused, `Alt+↑` / `Alt+↓` moves it. Drag and
  drop is optional later.

### 9.4 Sections

| Section | Controls |
|---|---|
| **Source** | Name (TextField, autosaves after 400ms). *Start from* (new only: Defaults / Copy ‹standup›, a Combobox listing standups; copies the Person view, Pulse, Whole board and speaker-behaviour settings, walk order and limits, but not the source, speakers or column mapping, which depend on the board). Space (Combobox, async `SearchSpaces`). Board (Combobox, async `SearchBoards(spaceKey)`, disabled until a space is chosen; placeholder "Pick a space first"). Scope (Segmented: Active sprint / Whole board; Active sprint is disabled on Kanban with a hint). Extra filter (TextField, mono, JQL for Jira, validated with `ConnectionService.ValidateFilter` on blur and shown inline on error). |
| **Status mapping** | One row per board column in board order: column name → lane Combobox (Backlog / To Do / In Progress / In Review / QA / ‹lanes you named› / Done / Hidden, from `StandupConfig.laneOptions`) with a lane swatch. **Lanes you name:** type a name no lane has and the Combobox's last option is **Create lane “‹name›”** (the shared Combobox's create affordance); it adds a *working* lane (like To Do … QA: active, a days-in-lane limit, in the walk order and on the whole board) and maps that column to it. Names are 1–40 characters, unique ignoring case; up to 12 per standup; Backlog, Done and Hidden keep their meaning and can't be created. Each custom lane gets a stable id (`c1`, `c2`, …, never reused) and the lowest colour of the extended lane palette (`lane-x1…x8`) that no other custom lane of the standup uses (rotating only when all eight are taken). A **Lanes** list follows the columns: one name field per working lane and Done (autosave, 400ms; blank restores a built-in name such as "In Review"; ids never change), headed by the lane's current name, with a hint naming the columns mapped to it, and **Delete** on lanes you named. Deleting a lane with columns first asks "Move its columns to" (Combobox), then **Delete lane**; synced history is untouched. A board change keeps custom lanes; new columns auto-match lane names first (case-insensitive), then the usual rules. There is no "auto" tag (removed 2026-11-24 as noise). If statuses aren't mapped, show a warning line. When two or more columns map to the same working or Done lane, an **Order within sections** block follows: one numbered list per shared lane with ↑/↓ (like the walk order); tickets in that lane are grouped in this order, rank order within a column. It defaults to board order. New standup: an explanatory paragraph until a board is chosen. |
| **Person view** | Walk order: an ordered list of the working lanes (the four built-in ones plus any you named) (swatch, name, "Jira: ‹columns›" mono 12 muted), presets **Right to left** and **Left to right** (board column order from Go, `StandupConfig.walkLeftToRight`, and its reverse), then a locked Done row ("always last, so it's first below the fold") under Done's configured name. The subtitle shows "Right to left (QA first)", "Left to right (To Do first)" (the lanes' configured names) or "Custom order". Rows: Segmented, *Fit when possible* / *Always roomy*, with the hint "Tighten before scrolling". |
| **Speakers** | Toolbar: Shuffle, A–Z, Most active, then the *Reshuffle daily* switch. An ordered list with include checkboxes (excluded rows muted, number "—") and a **Remove** button on every row (Undo toast). A removed person is not re-added by sync; adding them again restores them. Choosing a different board starts a fresh list from the next sync. Then *Unassigned as a speaker* (First / Last / Skip) in a dashed box. Then "Add someone from this space…" (Combobox, async `SearchUsers`, clears on select). Footnote: "Listed: anyone with tickets on this board in the last 30 days." After a sort action, a transient note on the right ("Shuffled just now"). New standup: an empty state until a board is chosen. |
| **Pulse** | Days shown (Segmented 7 / 14 / 21 / 28). A live **preview track** of a sample ticket from this board (axis + track, updates instantly). Marks: Commits / PRs / Comments switches. Status limits: one stepper per working lane (custom lanes included) in walk order with swatches, 0–99 (0 = none). Weekends: Shade / Plain. If dev-status is unavailable, a one-line warning: "Commit and PR marks unavailable — Jira's development info isn't reachable for this site." |
| **Whole board** | Done window: Sprint / 14 days / Since yesterday (Sprint is disabled on Kanban). Backlog rows: Stepper 0–50, with the hint "top of the ranked backlog · 0 turns it off". |
| **Display** *(app-wide)* | Theme: Light / Dark / System. Text size: Standard / Large (TV). Sync on open: When stale / Always / Never, plus "Stale after" Stepper in hours (1–48, default 6), shown only when *When stale* is selected. **Updates** (only in builds with an update channel, ADR 0016): the *Check for updates daily* Switch (default on; description "Once a day Cuisle asks GitHub/Forgejo whether there's a newer release. Nothing about you or your Jira data is sent."), then "Cuisle v1.2.0 · last checked 3h ago" (or "not checked yet") with a **Check now** button ("Checking…" while it runs), then one result line: "You're up to date.", "Cuisle v1.3.0 is available: use Update in the standup header.", or the failure in alert text. |
| **Export** *(per standup)* | One row per destination scoped to this standup or to all standups: name, kind ("File (JSON)"), destination detail (folder path, mono 13, middle-ellipsis), *Send on* Segmented (Manual / After each sync), **Send now**, and a status line ("Delivered 2 min ago · cuisle-MOB-2026-11-23.json", or "Failed · Retry" in alert text). **+ Add destination** opens a Combobox of sink kinds (v1: File (JSON)), then the kind's fields, rendered from its `ConfigSchema` (folder uses the OS folder picker). Everything autosaves. Delete uses Undo. |
| **Connection** *(app-wide)* | Site, account email, "Connected" tag, **Reconnect** (opens the Connect form inline) and **Disconnect** (confirm; offers to delete synced content only past the storage cap, §14.11). |

### 9.5 Behaviour
- **Autosave:** every control commits on change (Segmented, Switch, Stepper, Checkbox, Combobox select, list move).
  TextFields commit 400ms after the last keystroke and on blur. See architecture.md §5.3.
- **Source changes:** the rail item and title show "needs sync". Leaving Configure triggers a full sync of that standup.
- **New standup:** created as a DRAFT the moment it's opened (so it autosaves too). It leaves DRAFT once a board is
  chosen. Drafts are excluded from the header tabs and the 1–9 numbering (§4). Leaving Configure with an empty draft (no space) deletes it silently.
- **Duplicate:** copies settings, speakers and walk order, names it "‹name› (copy)", and selects it.
- **Delete:** soft delete with the toast "Deleted ‹name› · Undo" (10s). Afterwards the standup is archived: hidden, not synced, and its history is kept (an archive/restore list is post-v1).

### 9.6 Responsive
Uses the same breakpoints as §3, with the column layouts in §9.2. Nothing in Configure is hidden at small sizes; the page
scrolls and the jump bar helps navigation.

### 9.7 Connect screen (first run, not mocked)
- A centred 520px card on `--color-bg`: wordmark, the one-line value statement, then fields.
  - **Jira site:** TextField, placeholder `your-site.atlassian.net`; normalise to `https://…`.
  - **Email.**
  - **API token:** password field, with the link "Create an API token ↗" to
    `https://id.atlassian.com/manage-profile/security/api-tokens`.
  - Note: "Classic API tokens only. Stored in your system keychain."
- **Connect:** primary button. On success it goes to Configure → New standup. Errors show inline under the relevant field.

## 10. Accessibility and legibility
- **Contrast:** text ≥ 4.5:1 (≥ 3:1 for text 24px+); lane swatches and chips ≥ 3:1 against the background; light and
  dark both verified in CI (axe in Playwright). The extended lane palette for lanes the user names (`lane-x1…x8`) is
  held to the same 3:1 on bg, weekend, surface and rail-bg, and `lane-x-ink` text on each of them to 4.5:1
  (`tests/a11y/contrast.spec.ts`).
- **Colour is never the only signal:** lanes are named in headings and gutters, blocked has a tag and a hatch,
  over-limit uses a filled chip, and PR is a different shape from commit.
- **Minimum type:** 13px mono and 15px sans for content (12px allowed for micro labels and tags).
- **Hit targets:** 44px for rail items, tabs, header buttons and Done; 34px or more for in-form controls.
- **Focus:** a 2px `--color-focus-ring` outline with a 2px offset on every interactive element. Never removed.
- **Screen readers:** tracks are `role="img"` with a Go-provided label. The more bar is `aria-live="polite"`. The save
  indicator is `role="status"`.
- **Reduced motion:** no animated transitions beyond 140ms fades. All motion is disabled under
  `prefers-reduced-motion`.

## 11. Large (TV) text size
`--text-scale: 1.25` multiplies every font, row height and spacing token (`--scale` is the product the CSS multiplies
by: the text scale in the header, text scale × zoom below it, §16.1). Breakpoints are compared after scaling, so a 1920
window at Large behaves like a 1536 window at Standard, and Large at 150% zoom like a 1024 window.

## 12. Copy guidelines
- Plain words and sentence case. Numbers as numerals; "1 active" is singular. Days are "6d" in chips and "6 days" in
  prose.
- **Empty states** say what to do next. **Errors** say what happened and the action ("Jira rejected the token —
  Reconnect").
- Use "space" for a Jira project in UI copy, and "board" for a Jira board.

## 13. Known differences from the mockups (this spec wins)

| Mockup shows | Implement |
|---|---|
| Wordmark "STANDUP" | "CUISLE" |
| Configure: "Cancel" + "Save & back to standup", plus `~/.config/standup/config.toml` | Save indicator + **Done**. Everything autosaves; settings live in SQLite. |
| Display: "Refresh 1 min / 2 min / 5 min / Manual" | "Sync on open: When stale / Always / Never" + "Stale after N h". No polling. |
| No Resync control | ↻ Resync button in the header (§2.1) |
| Board lanes ordered by ticket count | Speaking order, cut into contiguous columns (§6.2) |
| Person view at 1920 uses 34px side-heading rows | 38px rows with side headings (`compact-side`, §5.5) |
| Mockup dropdowns look like plain selects | Every dropdown is an autocomplete Combobox |
| No Connection section | Configure → Connection (app-wide), plus the first-run Connect screen |
| No Export section | Configure → Export (per standup), §9.4 |
| Footer hint "press D or K to open them" (old round) | Removed; lists are always drawn |
| Ticket modal background shows the old "STANDUP" mockup | Real app behind the scrim (§14) |
| `board-status-*` / `board-assignee-toggle-*`: Saffron sample data, tab "Saffron Mobile"; the legend lists only five lane/blocked/limit keys; the BLOCKED badge also shows on assignee-mode rows | Real data and tab name; the board's legend is always the short one (no commit / PR / comment keys: lane tracks draw no marks); BLOCKED shows after the title in **status mode only** (assignee mode stays as §6.2) |
| `zoom-board-100` shows the "? Key" button at 1920 | The board's legend is `short` there (§3): at 1648 it fits beside the switch and the control; it becomes "? Key" below `mainW` 1450 |
| `zoom-*` icons and the "? Key" button keep one size | The rail's compact whole-board icon (22px) and the "? Key" button's height are fixed px, as at Large text; everything sized with `--scale` zooms |
| `zoom-person-150` shows Done as chips from an old renderer | The ladder, then the more bar (§5.5–5.6) |
| `zoom-*`: the toast reads "150% · side headings" over the axis | Same, in a `role=status` region under the control (§16.2) |

## 14. Ticket modal ("Docket")

Opens any ticket inside Cuisle (F12, ADR 0015). Mockups: `ticket-1920x1080.png`, `ticket-1440x900.png`,
`ticket-1280x720.png`, `ticket-1024x640.png`, `ticket-edit-states-1920x1080.png`, `ticket-lightbox-1920x1080.png`.
This is the only layout. A side sheet and a three-column variant were explored and rejected as too busy (Chris,
2026-11-23); don't build or add a wide breakpoint for them.

### 14.1 Opening, stepping, closing
- **Open:** click a ticket row or lane row, or `Enter` on a focused row, in the person view, whole board, Done list or
  backlog. Rows show a hover/focus affordance (`--color-rail-bg` fill, cursor pointer).
- **Step:** `J`/`K` and `→`/`←` step through the tickets in the current view's order without closing the modal.
  - Person view: walk order, then Done.
  - Whole board: lane order, then Done, then backlog.
  - Stepping never changes speaker, and it stops at the ends.
  - Stepping is keyboard only: the header has no stepper buttons and no `7 / 20` counter (Chris, 2026-11-24). The footer
    hint (`J K step`) keeps it discoverable.
- **Inline cards:** an inline card or link to another synced issue opens that issue in the modal with a back stack
  ("← MOB-1421", `Backspace`). Any other link opens the browser.
- **Esc unwinds one layer at a time:** combobox or lightbox, then composer blur (the draft is kept), then the modal.
  Focus returns to the row that opened it.
- **Edits update the screen behind the modal live:** the row's lane, pulse and blocked tag change at once.

### 14.2 Geometry
A centred modal with `--color-bg`, a 1px `--color-rule-mid` border (a heavy frame drew the eye; changed 2026-11-24) and radius 4. There is no shadow or blur. The scrim is
`--color-scrim` (light `rgba(22,24,29,.55)`, dark `rgba(0,0,0,.65)`). A click on the scrim (pressed and released there) closes the modal, like Esc; a dragged text selection released over it does not.

| Window | Modal | Content column | Field rail | Body text | Header |
|---|---|---|---|---|---|
| 1920×1080 | 1320×1000 | 880 | rest (≈432) | 18/28 | tall |
| 1440×900 | 1240×836 | 840 | rest | 18/28 | tall |
| 1280×720 | 1216×672 | 836 | rest | 17/26 | short, no footer |
| 1024×640 | 1000×616 | 700 | rest (≈292), labels stacked | 17/26 | short, no footer |

- **The table rows are exact** (Playwright and WP-T7's tests pin them).
- **Sizing between rows:** height = `min(1000, vh − 2·my)`, with my = 40 / 32 / 24 / 12 at the four sizes. Width =
  `min(1320, vw − 2·mx)`, with mx = 100 at 1440 and above (capped by 1320 at 1920), 32 at 1280 and 12 at 1024.
- **Content column:** the table values. Between rows, use the nearest smaller row's column, never more than
  `modal − 292`.
- **Short mode:** applies when `vh < 800`.
- **Under 900 CSS px wide** (1024 at Large): the State group (Status / Assignee / Flag) becomes a 44px strip pinned under
  the header, and the remaining fields become the first section of the content column.
- **Scrolling:** the header is sticky, and the content column and rail scroll independently.

### 14.3 Header (an 8px gap, then a 1px `--color-rule-soft` rule below; the old 3px ink rule was too loud)
1. **Row 1, 52px (44 short).** Quiet by design: nothing here may wrap, and nothing repeats the rail.
   - Left: `← KEY` back button (only with a back stack), type icon 22, key mono 22 bold (click copies the key; never wraps,
     `white-space: nowrap`). There is no parent crumb: the parent is a row of the field rail (§14.6), and clicking it opens the parent (in the modal when synced, else in Jira).
   - Right: `⚡︎9:41` (U+26A1 + U+FE0E so it draws as a text glyph, then local `H:mm`, mono 13 muted) with a tooltip:
     `Synced at 9:41 (this ticket's data is as of that sync)`. After a per-ticket refresh it reads `⚡︎9:58` with the tooltip
     `Refreshed just this ticket at 9:58 · board synced 9:41`. Before any sync it reads `not synced yet`.
     Failures and waits stay spelled out in the same slot (`Couldn't refresh · offline` in alert colour,
     `waiting for Jira (rate limit) · 6s`, and `refreshing…` after 250 ms).
   - Then **↻ Refresh** (§14.12; icon-only below 1280, tooltip `Refresh this ticket (R)`), an icon-only **Open in Jira**
     button (a plain external-link glyph, not Atlassian's logo; accessible name `Open in Jira`, tooltip
     `Open in Jira (O)`), and ✕ (44px).
2. **Row 2:** summary 28/34 bold (22/28 short). It wraps to two lines and is never ellipsised.
3. **Row 3, pulse strip, 46px (28 short).**
   - Lane swatch and name (13 bold caps), then the 21-day track (600 / 440 / 440 / 300) with weekend shading.
   - Monday labels in mono 11 sit in their own 20px row above the track; there are no labels in short mode. The row is
     taller than the text on purpose: a PR diamond overhangs the 26px track by about 6px, so labels and marks never
     overlap (a Playwright test measures this at all four sizes). The person and board rows are unaffected: their rows are
     at least 34px tall, so marks stay inside the track.
   - The days chip sits flush against the today line and is red past the limit. A `BLOCKED 3D` lozenge shows when
     flagged. On the right: `limit 5d · 21 days` (mono 12 muted).
   - Status is not repeated as a lozenge; the editable Status sits at the top of the rail.

### 14.4 Content column
- **Anchor strip (32px, pinned):** `DESCRIPTION · ATTACHMENTS 5 · SUBTASKS 3/5 · LINKS 2 · COMMENTS 7` (13 bold caps,
  mono counts). Clicking scrolls to the section, and the active section is underlined 3px. Empty sections are omitted.
- **Order:** Description, then Attachments, Subtasks, Linked issues, Comments.
  - Description has no section head of its own (the strip names it); the others use the Configure section head (15 bold
    caps, mono count, 3px rule).
  - Padding is 32 (24 at 1280, 20 at 1024).
- **Measure:** everything (text, panels, tables, code, media) spans the full inner width of the column (at most 880
  minus padding). There is no separate text measure.
- **ADF rendering** (Go parses ADF into a typed node tree; Preact renders it; never `innerHTML`):

| Node | Rendering |
|---|---|
| heading 1–3 | 26 / 22 / 19 bold; h4–h6 16 bold caps .08em |
| paragraph, hardBreak | body size; 14px paragraph gap |
| strong / em / strike / underline / sub / sup | native semantics |
| textColor | hue kept, lightness clamped to ≥ 4.5:1 for the current theme |
| link | `--color-link`, underlined |
| code (mark) | mono 0.84em, `--color-code-bg` fill, 1px rule-mid, radius 2 |
| codeBlock | code-bg fill, 2px rule-mid border, mono 14/22, language tab top-right (mono 11 caps); no syntax colour in v1; long lines wrap (never scrolls sideways) |
| blockquote | 3px rule-mid left bar, muted text |
| rule | 2px rule-soft |
| bulletList / orderedList (nested) | 26px indent |
| taskList | 22px read-only checkbox; done items muted (no strikethrough) |
| decisionList | 3px `--color-type-story` left bar + "DECISION" micro caps |
| panel info / note / success / warning / error | 4px left bar + 10% tint (14% dark) of `type-task` / `type-spike` / `type-story` / `lane-qa` / `alert-bg`; 18px icon |
| table | 2px rule-mid outer, 1px rule-soft cells; header row/column on rail-bg, 15 bold; cells 16/22; colwidth is a hint, colspan, rowspan; fits the column width and cells wrap, even long unbroken text (never scrolls sideways) |
| expand / nestedExpand | 38px row, chevron + title, 2px rule-soft border; collapsed as authored; `E` toggles all |
| status (lozenge) | mono 11 bold caps, radius 2, filled: neutral→todo, blue→prog, purple→review, yellow→qa, green→done, red→alert |
| mention | 600 weight on a rail-bg chip; the current user's mention is inverted |
| date | mono chip; alert-text when in the past |
| emoji | native glyph (shortName fallback text) |
| inlineCard / blockCard | 1px rule-mid chip: type icon, mono key, title, status lozenge when the URL is a synced issue; otherwise favicon-less host + path |
| mediaSingle | honours `width`/`layout` up to the column width; 1px rule-mid frame on `--color-surface` (white in both themes); caption 14 muted; click opens the lightbox |
| mediaGroup / mediaInline (files) | file card (below) |
| unknown node | its text content + muted "unsupported block ↗" (opens in Jira) |

- **Attachments section:** image thumbnails 160×100 (cover, 1px rule-mid) with name (14 semibold) and
  `412 KB · TL · 18 Nov` (mono 12). These are followed by file cards: 240×56, 2px rule-soft, a 46px mono extension
  block on rail-bg, name 15 semibold, and meta mono 12. Clicking a file downloads it to the OS temp folder and opens it.
- **Subtasks and links:** 34px rows (type icon, mono key, title, status lozenge, assignee initials). Links are grouped
  under micro caps ("IS BLOCKED BY", "BLOCKS", "RELATES TO"). Done subtasks are muted and struck through; this is a
  list row, not a task list.

### 14.5 Comments
- **Order:** newest first by default, via a Segmented control (Newest / Oldest) that persists (app setting
  `ticket.comment_order`). All comments are drawn; there is no "show older".
- **Composer** at the head of the section:
  - Initials, then a TextField (38px, grows to 120), then Send (primary, disabled when empty).
  - `@` opens the people Combobox. `Ctrl/Cmd+Enter` sends.
  - The draft autosaves per issue (`ticket_draft` table) and survives close/reopen and app restart.
- **Comment:** initials, name (16 semibold), `Mon 23 Nov 09:12 · 29m` (mono 13 muted), `edited`, and the visibility tag.
  Body is the ADF renderer at body−1. Comments are separated by 2px rule-soft rules.
- **Restricted visibility:** a lock icon and `DEVELOPERS ONLY` (mono 11 caps, 1.5px alert outline). It is loud on purpose
  because the screen is shared.
- **Standup marker:** a 2px ink rule labelled `↑ 2 NEW SINCE STANDUP · FRI 20 NOV`, placed below the comments newer than
  the previous standup ("since yesterday", D3). The arrow flips in oldest-first order.
- **Sending:** the comment is inserted at once at 60% opacity, and `sending…` appears on the author line after 250ms.
- **Failed:**
  - A 3px dashed alert left border, then `Not sent · <reason>` (alert text 15 bold).
  - Retry / Edit / Discard buttons (34px).
  - The text is never lost, and nothing retries automatically.

### 14.6 Field rail
- **Rail:** `--color-rail-bg`, 2px rail-rule left border, padding 14/20.
- **Row:** label in sentence case (15/500 muted), in a 132px column (120 when the rail is under 420). Value 16 semibold.
  Rows are at least 32px with no per-row rules.
  - Groups are separated by 2px rule-soft, and group heads are 13 bold caps in fg.
  - Under 340px the labels stack above the values, and short values pair two-up (Priority | Points, Sprint | Due,
    Fix version | Time).
- **Groups, in order:**
  1. **State (editable):** Status, Assignee, Flag. The head carries `edits go to Jira` (mono 12 muted).
  2. **Details:** Reporter, Priority, Parent, Sprint (+ `ends 1 Dec`), Points, Due, Labels, Components, Fix version,
     Time tracking (`14h of 24h` + a 6px bar), Environment.
  3. **Custom fields:** order and visibility come from Configure → Ticket fields (§14.8). The default is all non-empty
     custom fields A–Z.
  4. **Development:** branch (mono 13), `7 commits · last 2h`, then PRs as `#418` + lozenge (OPEN prog, MERGED done,
     DECLINED alert) + title, with the repository as muted secondary text after it (the repository alone stands in for the
     title on PRs synced before titles were recorded; no Branch row until a PR with a source branch is known).
  5. **Dates:** Created, Updated, Resolved (mono), and Watchers.
- **Empty fields** never get a row. Each group ends with one wrapping line `Not set: Environment · Original estimate`
  (mono 13 muted). The exception is an empty Assignee, which renders as the dashed-red "Unassigned" control.
- **Value rendering by field kind** (`model.FieldValue.Kind`):

| Kind | Rendering |
|---|---|
| `text` (single line) | text, wraps |
| `paragraph` (ADF) | full width under the label, 15/22, clamped to 6 lines + "Show all" |
| `number`, `date`, `datetime`, story points | mono 15 |
| `url` | link showing host › last path segment(s); full URL in `title` |
| `option` (select, radio) | plain text |
| `options` (multi-select, checkboxes), `labels`, `versions`, `components` | wrapping outlined chips (1px chip border, 14 semibold; mono for version/label-like ids) |
| `cascade` | `Parent › Child` |
| `user` / `users` | initials box + name; multiple users one per line |
| `group`, `project`, `team` | icon + name |
| `unknown` | raw value, mono 14 |

- **Editable fields:**
  - **Status** and **Assignee** are real 38px Combobox fields: white, 2px rule-mid border (ink when open), chevron. They
    are the only white boxes on the rail, and that is the affordance.
  - **Flag** is a 44×24 Switch. On, it is alert-filled and shows the `BLOCKED` lozenge + days.
- **Status combobox, open state:**
  - A filter input, then the transitions as `→ In Review` with a lane swatch, max 8 rows.
  - Transitions that need a Jira screen go below a 2px rule-soft divider. They are fully muted and marked
    `screen · ↗ Jira`; choosing one opens the browser.
  - Transitions are loaded when the combobox opens (spinner after 250ms). When offline, the cached list is shown
    disabled with "Offline · open in Jira".
- **Saving:** optimistic, with `saving…` after 250ms. On failure the field reverts and an alert-text line appears under
  it ("Jira rejected the change: <reason>") with Retry.

### 14.7 Lightbox
- **Background:** the scrim is `rgba(22,24,29,.92)`. A click on it (not on the image) closes the lightbox.
- **Top bar:** solid 64px `--color-fg`. It holds:
  - key, then filename (mono)
  - `412 KB · 1200×720 · Wren Kowalski · 18 Nov`
  - zoom readout (`100%` or `fit 62%`)
  - `1 / 2`
  - Open file
  - ✕
- **Image:** never upscaled; shown at natural size or fit, centred.
- **Navigation:** ← → step through the ticket's images (56px outlined buttons at the edges). The caption sits centred at
  the bottom. `Esc` returns to the ticket.

### 14.8 Configure → Ticket fields (per connection)
- **Location:** a section under Connection.
- **Contents:** a Combobox to add fields ("Pin a field…"), then a reorderable list of pinned custom fields with a hide
  toggle each. Hidden fields go into a collapsed "Hidden (n)" list.
- **Defaults:** nothing pinned and nothing hidden, so all non-empty custom fields show A–Z.
- **Saving:** autosaves (ADR 0005).

### 14.9 Updated under the reader
If a Resync lands while the modal is open and this issue changed, a 36px bar appears under the anchor strip:
`Updated in Jira · Reload` (the 3 changed fields are named). The content never reflows under the reader; edits the
user made in Cuisle apply immediately.

### 14.10 Keyboard (inside the modal)
| Key | Action |
|---|---|
| `J` / `→`, `K` / `←` | Next / previous ticket in view order |
| `C` | Focus the comment composer |
| `S` / `A` | Open the Status / Assignee combobox |
| `F` | Toggle flag |
| `R` | Refresh this ticket from Jira (§14.12). While the modal is open, `R` never starts a board resync. |
| `O` | Open in Jira |
| `E` | Expand/collapse all expand blocks |
| `Backspace` | Back to the previous ticket in the inline-card stack |
| `Ctrl/Cmd+Enter` | Send comment |
| `Esc` | Unwind one layer (§14.1) |

The footer (30px, `vh ≥ 800` only) lists `J K step · C comment · S status · A assignee · F flag · R refresh · O open in
Jira · Esc close`. In short mode these hints live in the `?` popover.

### 14.11 Storage and Disconnect
- **Display → Storage** shows local content size: the database plus the media cache (`1.2 GB · images 640 MB`). It has
  **Clear image cache** and a **Cleanup offer above** number field in GB.
  - That field is the app setting `storage.cleanup_offer_mb`, default 2048.
  - The setting autosaves.
- **Disconnect** always keeps synced content by default (history, ADR 0011), and the keychain item is always removed.
  - **Under the cap:** the confirm offers no deletion.
  - **Over the cap:** the confirm adds one **unchecked** checkbox, "Also delete synced content (2.4 GB): descriptions,
    comments and images. History of status, assignee and flags is kept."
  - Checking it deletes `issue_content` rows, comment bodies (bodies only; the comment rows stay), field values,
    transition caches, drafts, and the media cache for that connection. Then it runs `VACUUM`.
- There is no automatic deletion anywhere, and no prompt outside Disconnect.

### 14.12 Refresh this ticket
- **Button:** ↻ **Refresh**, left of the icon-only Open in Jira button (38px outlined; icon-only below 1280; `R`). It
  re-fetches **only this issue**: fields, description, comments, attachments (new thumbnails), links, subtasks, and its
  changelog. Then it applies it with the one-issue apply (jira-sync §4.8), so status, assignee and flag changes are
  historised exactly as a sync would record them. Any cached transitions for the issue are dropped.
- **While refreshing:** the icon spins and the meta text reads `refreshing…` after 250ms. The content does not reflow
  until the result lands; then it swaps in one frame and the meta reads `⚡︎9:58` (tooltip `Refreshed just this ticket at 9:58 · board synced 9:41`).
- **Live update behind the modal:** the row behind the modal, and any other standup containing the issue, update live
  (`ticket:updated`, `data:changed`).
- **Cooldown:** 10 s per issue. The button is disabled with a mono countdown (`↻ 8s`) to protect the API (jira-sync
  §3.1). Stepping to another ticket and back does not reset it.
- **Offline or failure:** the meta reads `Couldn't refresh · offline` (or Jira's reason) in alert text. The last synced
  data stays.
- **Not deleted, only tombstoned:** a refresh can show that the issue was deleted or moved out of the board. The modal
  then shows a 36px banner "No longer on this board" and keeps the stored content (history, ADR 0011).
- **Rate limiting:** when Jira is throttling (jira-sync §3.1), Refresh waits its turn in the interactive lane. The meta
  reads `waiting for Jira (rate limit) · 6s`.

### 14.13 Rate-limit visibility (whole app)
When the limiter is paused by a 429, or slowed by a near-limit signal (jira-sync §3.1), the header sync status shows
`Jira is rate-limiting · resuming in 12s`. This is a quiet mono label with the ↻ button still available, never a banner.
Diagnostics show requests in the last hour, the current rate, and throttle events.

## 15. Command palette with the ticket finder (F14)

Designed 2026-11-25 (canvas Rounds 9 and 10). Mockups: `design/mockups/palette-{empty,ticket,commands,people,nested}-1920x1080.png`
(Saffron data over an older backdrop: where they differ from the app, this spec wins). The header search field and a
"jump to key" overlay were considered and not chosen. Implementation: `frontend/src/commands/*` (registry, scorer,
builtin commands), `frontend/src/views/palette/*` (dialog, rows, pure `model.ts`), `frontend/src/state/palette.ts` (the
small eager half). The dialog and the commands are a **lazy chunk** fetched on first open.

### 15.1 Opening and closing
- **Header button:** Find (§2.1). **Shortcuts:** `⌘K` / `Ctrl+K` everywhere (§8), `T` when no field has focus outside
  the ticket modal. Typing `#`, `@` or `>` as the first character switches mode. `/` stays the standup switcher.
- **Layout:** a dialog `min(860, vw - 48)` wide (× `--scale`), top 96px (64px when `vh < 800`), 3px ink border, radius
  4. The scrim is `--color-scrim-palette` (35% ink; 45% black in dark) so the app stays readable behind it. Results scroll
  inside the dialog, max height `vh - top - 48`. It sits above the ticket modal.
- **Esc unwinds:** a nested level first, then the mode chip, then it closes. Focus returns to the opener. Every action
  closes the palette except entering a nested command or a list (`Go to person…`). It keeps no state between opens
  except the recents and the command usage (§15.7).

### 15.2 Input and modes
- **Input:** 62px, search icon, mono 22 semibold, placeholder "Find a ticket by key or title, or run a command" (`#`: "Find a
  ticket by key or title"). An `Esc` chip at the
  right. The **mode chip** (`>`, `#`, `@`; mono 15 bold on ink) sits before the text; `⌫` on an empty input removes it.
- **No prefix** searches everything. A query with a digit lists tickets, then commands, then people; a query of letters
  only lists commands, then people, then the tickets whose **titles** match (§15.3). Empty sections are omitted. The
  finder is asked for any query with a digit or two non-space characters; a single letter asks for nothing.
- **Empty query:** RECENT TICKETS (the last 3 opened from any path), then suggested COMMANDS: the five most run
  (`palette.command_usage`), topped up with Resync, Group board by…, Go to person…, Switch standup…, Open Configure.
- `>` lists every command. `#` takes a ticket key or title words (empty: up to 8 recents; one letter: "Type a ticket key or a few
  letters of its title, like 1429, mob-1429 or recipe card."). `@` lists people (§15.5).

### 15.3 Ticket matching (by key and by title)
The finder matches the **key** (tiers 1 to 3) and the **summary** (tier 4). Key tiers always rank first. Matching is local
Go (`TicketService.FindByKey`; `internal/keyfind`), p95 under 5 ms at 5,000 issues, and never touches the network.
- **Candidates** (both kinds): every synced issue of the connection across all standups, including Done issues and issues
  no longer on a board (muted "not on board"). Within a tier: current standup first, then active before done, then key
  ascending (a title tier ranks by score first, below).
- **Key matching.** Normalise the query: lower-case, drop spaces and `-`, split into letters and digits (`mob1429` →
  `mob` + `1429`). The letters must be a prefix of the project key (`m` → MOB); no letters = any project. Tiers:
  1. **exact** (digits equal the number);
  2. **starts with** (digits are a prefix; first 10, then "↓ N more · keep typing");
  3. **one character off** (same length, one digit substituted or two adjacent digits swapped; 2+ digits; at most 5).
- **Title matching** (tier 4, label **title**, first 10, then "↓ N more · keep typing").
  - **Words.** The query is split on spaces; every word must match the summary, in any order, case-insensitively
    (`card recipe` finds "Recipe card crops the hero photo"). A word matches, best first:
    1. a **whole word** ("card" in "Recipe card crops"): 4000
    2. a **word start** ("rec" in "Recipe card crops"): 3000
    3. a **substring** ("cip" in "Recipe card crops"): 2000
    4. an in-order **subsequence**, fzf-style, for words of **3 or more** characters ("rcd" → **R**ecipe **c**ar**d**): at
       most 999. A match scores 16, +8 at the start of a word, +8 when it follows the previous match directly, and
       costs 3 for a gap's first character and 1 for each more. The matched characters must lie within 3 × length + 3
       characters of each other, so "mob" does not match "**M**eal plan templates f**o**r **b**usy weeks".
    A one- or two-character word, and **any word containing a digit**, must match contiguously (whole word, word
    start or substring), so `1429` never matches digits scattered across a title. A ticket's score is the sum over its words; words are matched
    independently, so two words may ink the same characters. Higher scores first, then the order above.
  - **When it runs.** The query needs two non-space characters, including at least one letter or digit. **Numbers
    search titles too** (changed 2026-11-25, maintainer): `404` finds "Fix the 404 page", and `1429` lists the key
    matches (MOB-1429, …) first, then titles containing "1429". Key-shaped queries (`mob1429`, `m14`) likewise list key
    matches first, then titles. A query of several words searches both: `m 14` lists the tickets numbered 14… first,
    then those whose title has an "m" and a "14". A ticket that hits
    both ways is listed once, in its key tier. The key itself is not searched as title text (`1429 share` finds titles
    containing both words).
  - **Examples** (summary "Recipe card crops the hero photo"): `recipe card`, `card recipe`, `RECIPE CARD`, `rec ca`
    (word starts), `cip` (substring) and `rcd` (subsequence) all match; `ce` does not (two characters, not contiguous);
    `recipe photo zzz` does not (a word is missing).
  - **Ink positions** are indexes of the summary's characters as code points (Go runes; the frontend walks `[...summary]`,
    so an emoji counts as one). Case folding maps one character to one character, so positions never shift.
- **Highlight:** ink background, paper text, on the matched characters. A key hit inks the matched digits of the key (the
  differing digit of a near match stays plain) and leaves the title plain; a title hit inks the matched summary
  characters and leaves the key plain.
- **Ticket commands are key-driven:** a title hit never makes "Refresh MOB-12 from Jira" appear under COMMANDS (typing
  `resync` must not offer commands for an unrelated ticket whose title contains the word).
- **Row (48px):** type icon, key (mono 17 bold), title (17 semibold, ellipsis), lane swatch + lane name (12 bold caps,
  from the standup's lane data, so custom lanes work), assignee initials or the dashed `··` box, days chip (red over the
  limit; none for Done), the standup key (mono 12 muted).
- **Actions:** `↵` opens the ticket modal in the standup the issue belongs to (the current one if it is a member, else
  the first by tab order; an issue on no board opens in the current standup) with that standup's view order for `J`/`K`;
  from Configure it leaves Configure first. `⌘↵` / `Ctrl↵` opens it in Jira.
- **Not synced:** a fully specified key (`mob-1729`) with no exact hit shows, first and selected, "**MOB-1729** isn't
  synced in Cuisle · ↵ fetches just this ticket from Jira (one request), then opens it". `↵` calls
  `TicketService.FetchByKey` (the palette's **only network call**, explicit, one request, the limiter's interactive
  lane), stores the issue without any standup membership, and opens it in the current standup's lane mapping. Errors
  appear in that row and the palette stays open: "Couldn't reach Jira · offline", "No issue MOB-1729 in Jira", "You
  don't have access" (also rate-limited and auth cases).
- **Ticket commands** appear under COMMANDS when the modal is open or a ticket matched the query: "Refresh MOB-1429
  from Jira" (opens it, then the modal's Refresh with its 10 s cooldown), "Open MOB-1429 in Jira", "Copy MOB-1429".

### 15.4 Commands
- **Registry** (`commands/registry.ts`): every action is registered once with `id`, `title`, `category` (Navigate, Sync,
  View, Display, Ticket, Configure, App), `keywords`, `shortcut`, `detail`, `available` (true or the reason it is
  disabled), `run` or `children` (nested) and, as extensions, `visible` (hide entirely), `mode` (opens a list), `checked`
  and `placeholder`. Registry order is the final tie-break.
- **Initial set:** Resync `R`, Full resync `⇧R`, Cancel sync (only while running); Group board by status/assignee `G`
  (the title names the other mode, the detail "now: assignee"; from a person view it also goes to the whole board);
  Whole board `B`, Go to person… (`@`), Unassigned, Switch standup… `1–9`, Open Configure `,`, Keyboard shortcuts `?`;
  Theme › (System / Light / Dark), Text size › (Standard / Large (TV)); New standup, Duplicate this standup; Copy
  diagnostics, Check for updates, Install update (disabled "none waiting"), Reconnect Jira (only when auth is invalid);
  the ticket commands above. Lane names are never hard-coded.
- **Matching:** a fuzzy subsequence over the title and keywords, fzf-style (`commands/score.ts`): a bonus for a match
  that starts a word and for contiguous runs, a penalty for gaps; ties go to recent use, then registry order. `sta` ranks
  "Group board by status" first. Matched title characters are inked; an alias match inks nothing.
- **Row (48px):** 20px icon, title, optional detail (mono 13 muted), ✓ on the current choice, category (mono 11 bold caps,
  92px right-aligned), shortcut kbd, `›` for a nested command. A disabled row is muted with its reason as the detail.
- **Nested:** a breadcrumb chip (`Theme ›`) replaces the mode chip, the placeholder says "Choose a theme", the children
  filter as you type, `⌫` on an empty input goes back, and a note line explains it. Choices apply and save at once (the
  same settings as Configure → Display) and close the palette.

### 15.5 People (`@`)
Every speaker of every standup, deduplicated by account (`StandupService.People`), fuzzy-matched on the name, then an
ALSO section with Unassigned ("N need an owner"). Row: initials (mono 13 bold), name, "N active", PERSON, the standup key
(the current standup if they speak there, else their first). `↵` goes to that standup and selects the person.

### 15.6 Keyboard inside the palette
`↑` `↓` move (wrapping), `Home` `End`, `PageUp` `PageDown` by 8; `↵` runs or opens; `⌘↵` / `Ctrl↵` opens a ticket in
Jira; `→` enters a nested command when the caret is at the end; `⌫` on an empty input removes the chip or goes back a
level; `Esc` unwinds (§15.1); `Tab` does nothing (focus stays in the input). The palette stops every key from reaching
anything behind it. The footer (36px) reads `↑↓ choose · ↵ run / open · ⌘↵ open in Jira` and, at the right, `> commands
# tickets @ people`.
- **Accessibility:** the input is an ARIA combobox (`aria-expanded`, `aria-controls`, `aria-activedescendant`) over a
  listbox of grouped options (`aria-selected`, `aria-disabled`); a polite live region says "N results". axe is clean in
  both themes. The selected row reads its muted text as ink so contrast stays at 4.5:1.

### 15.7 Recents and persistence
`palette.recent_tickets`: the last 8 issue ids opened from any path (palette, rows, inline cards, stepping), most recent
first; the empty state shows 3, resolved locally by `TicketService.ByIDs`. `palette.command_usage`: command id → run
count. Both are app settings (architecture §6.4).

## 16. Zoom (F15)

Zoom makes the standup screen readable from further away without leaving it: everything below the header scales, and the
responsive rules **re-run on the effective size**, so a zoomed screen reflows (columns, density, rail) instead of just
getting bigger. Designed with Chris (2026-11-25); mockups `design/mockups/zoom-*-1920x1080.png`.

### 16.1 What zooms
- **Zoomed:** everything in the standup screen **below the header**, in the **person view** and the **Whole board**: the
  speaker rail, the title row and its controls, the axis, lanes and rows, Done, Backlog, the more bar and the banners.
  (`PulseTrack` takes the content scale as a prop from those two views; the ticket modal's header track and Configure's
  preview use the text scale alone.)
- **Not zoomed:** the header (wordmark, tabs, Find, Resync, Configure). Also Configure, the ticket modal, the command
  palette, the lightbox and toasts: they keep the Text size scale only.
- **Effective scale:** content scale = **Text size × zoom** (Large 1.25 × 150% = 1.875).

### 16.2 Levels, control and keys
- **Steps:** 70, 80, 90, 100, 110, 125, 150, 175, 200 %. Default 100.
- **Persistence:** **one zoom per standup**, shared by its person view and Whole board (`standup_ui_state.zoom`,
  data-model §2.1). Switching standups restores that standup's zoom. A change is saved at once (UI state: no debounce, no
  save indicator).
- **Control:** `− 100% +` (30px tall, 2px ink border, radius 3) at the right of the title row: on the Whole board after the
  Assignee | Status switch, in the person view before the legend. − and + step the level and are disabled at 70% / 200%.
  The middle button shows the level (mono 14 bold) and resets to 100% on click; it is **ink-filled whenever zoom ≠ 100%**.
  `role="group"` `aria-label="Zoom"`; the buttons are "Zoom out (−)", "Reset zoom to 100% (0)" and "Zoom in (+)". When
  zoomed, the middle button's name starts with its visible text ("150%. Reset zoom to 100% (0)", WCAG 2.5.3).
- **Keys:** §8. They work on the standup screen and are ignored while typing and while the ticket modal or palette is open.
  **⌘/Ctrl with `+` `=` `−` `0` are always swallowed**: the webview's own page zoom would also magnify the header (and, on
  macOS, the default View menu's Zoom items run before the page sees the key, so the application menu drops them:
  `cmd/cuisle/menu.go`; the window option `ZoomControlEnabled` is off).
- **Feedback:** a toast below the control (ink fill, mono 15 bold, `role="status"`, 1.2 s) reads `150% · 2 columns` /
  `175% · 1 column` on the board and `150% · side headings` on a person: the density the ladder chose ("side headings"
  for any side-heading step, else "roomy rows" / "compact rows"). It shows on a change, not when the view is switched.
- **Discoverability:** the rail footer ("+ − zoom"), the `?` key legend, §8, and the palette commands Zoom in `+`, Zoom out
  `−`, Reset zoom `0` (category View, detail "now 150%"; disabled at an end of the range or at 100%).

### 16.3 Layout: zoom re-runs the responsive rules
- **CSS, not `transform`.** The header keeps `--scale: var(--text-scale)`. `.sf-body` (rail + main) sets
  `--scale: calc(var(--text-scale) * var(--zoom))` and `--zoom: <level / 100>`; its sizes use `--scale`. The rail's own px
  sizes, the main area's padding and the board's 40px column gap follow `--zoom`, so they stay the design sizes the maths
  assumes.
- **Maths:** `contentViewport(w, h, textScale, contentScale)` gives the effective `vw` and `vh`. The header's 64px comes off
  first and goes back at design size, so the height constants that count the header stay right. Every layout function
  receives these: `railMode`, `mainWidth`, `mainPadding`, `titleSizes`, `trackWidth` / `personTrackWidth`, `legendMode`,
  `boardLegendMode`, `chooseDensity`, `belowFold`, `laneRowHeight`, `boardColumns`, `doneBacklogLayout`, `statusLayout`.
- **At 1920 × 1080:** 80% sees a 2400 window (3 board columns, everything incl. Done and Backlog fits); 100% sees 1920
  (2 columns); 150% sees 1280 (compact rail, 2 columns, 20px rows, the more bar lists Done and Backlog); 175% sees ~1097
  (1 column). The person view at 150% walks the ladder to *tight rows, side headings* and the more bar takes the rest.
- **Narrow effective widths** (high zoom on a small window) shrink the person track (§3) instead of scrolling sideways.
- **Nothing is hidden to fit** (AGENTS invariant 1): past the ladder, the more bar takes over as at 100%.
- **Keeping your place:** when the zoom changes, the topmost fully visible lane (board) or group (person) is put back at
  the top of the scroll area after the reflow. At the top of the screen it stays there (the board's title row scrolls
  with the lanes and must stay in view).
- **Speed:** a zoom step is one render of the visible screen: p95 about 24 ms (board, 2 ↔ 3 columns) and 15 ms (person,
  *compact* ↔ *tight* rows) at 4× CPU throttle, inside the 50 ms budget (`tests/perf/zoom.spec.ts`, which asserts the two
  levels really lay out differently).

