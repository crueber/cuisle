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
| Configure | `configure-1920x1080.png`, `configure-1440x900.png`, `configure-1280x720.png`, `configure-1024x640.png` |
| Configure: new standup | `configure-new-standup-1920x1080.png` |
| Connect (first run) | not mocked; see §9.7 |

## 2. Frame (standup screen)

```
┌ header 64px (3px bottom rule) ─────────────────────────────────────────────────────────────────┐
│ CUISLE │ [1 Saffron Mobile MOB] [2 Web & Checkout WEB] … [+]      synced 2m ↻   Mon 23 Nov  [Configure] │
├ rail ─┬ main (scrolls) ───────────────────────────────────────────────────────────────────────────┤
│ Whole │ title row (pinned)                                                                        │
│ board │ axis row (pinned)                                                                          │
│ …     │ content                                                                                    │
│       │ [more bar, pinned to bottom when content overflows]                                        │
└───────┴───────────────────────────────────────────────────────────────────────────────────────────┘
```

### 2.1 Header
- **Wordmark:** "CUISLE", 20px bold caps, tracking `--font-tracking-wordmark`. Then a 2×24 rule, then the tabs (§4).
- **Right cluster, in order:** the sync status, then the date, then Configure.
- **Sync status:** "synced 2 min ago", mono 14px muted, followed by the **Resync** icon button (↻, 36px, aria-label
  "Resync (R)").
  - While syncing it changes to "Syncing · issues 120/300" with an × (cancel).
  - After a partial run it shows ⚠ "synced 2 min ago · partial", and clicking opens the warnings.
  - When an automatic sync-on-open fails offline, it reads "offline · data from 3 h ago" in `--color-alert-text`, with
    no banner. Banners are only for failures of a sync the user started (and for auth failures, which block every sync).
- **Date:** "Mon 23 Nov", mono 16px semibold.
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
  - Footer hint (mono 12 muted): "← → next speaker · B whole board · 1–N switch standup".
- **Compact rail (`vw < 1360`), 76px:** a whole-board icon button, then per speaker the initials (mono 15 bold) with the
  count beneath (mono 11). The full name goes in `title` and `aria-label`. Unassigned shows as "Unas." + count, dashed.
- The rail scrolls vertically if it overflows. It never hides people.

### 2.3 Main area
- Padding: left/right 32px if `vw ≥ 1600`, else 20px. Top 20px if `vh ≥ 800`, else 12px. Bottom 10px.
- Vertical scroll container (`overflow-y:auto`). The title and axis rows are `position:sticky; top:0` with `--color-bg`
  behind them.

## 3. Responsive rules (summary)

| Rule | Value |
|---|---|
| Rail | Full 208px when `vw ≥ 1360`, else compact 76px |
| Tabs | Full, then key-only, then switcher menu, by measured fit (§4) |
| Legend | Full when `mainW ≥ 1560`, short (4 lane swatches + blocked + over-limit) when ≥ 1300, else a "? Key" button that opens a popover |
| Person track width | 600px when `mainW ≥ 1500`, 440px when ≥ 1150, else 300px |
| Axis labels | Every day number; Mondays bold `--color-fg`; weekends `--color-muted` (the weekend backdrop marks them; `--color-rule-mid` fails text contrast, §10). If a day is narrower than 18px, label Mondays only. |
| Board columns | 2 when `mainW ≥ 1150`, else 1 |
| Board Done/Backlog | Side by side when `mainW − 40 ≥ 1100`; Done in 2 columns and Backlog in 3 when that width ≥ 1300 (Backlog 2 at ≥ 1000), else 1 |
| Title sizes | h1 36px (person) / 30px (board) when `vh ≥ 800`, else 28 / 24. Title row 56px / 48px, or 46 / 40. |
| Configure | 3 columns when `mainW ≥ 1500`, 2 when ≥ 960, else 1 (§9.2) |

Breakpoints are also in `design/tokens.json` under `breakpoint`. They are evaluated in CSS px, so OS and app zoom and
**Large (TV)** text size (`--scale: 1.25`) move the app into smaller layouts automatically.

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
Groups appear in **walk order** (only lanes that have tickets), then **Done** last when it has tickets.

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
| Interaction | The whole row is a button; Enter or click calls `OpenIssue(key)`. Hover/focus shows a `--color-rule-soft` background. |

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
- Shown only when `scroll` is true. It's 36px, `position:sticky; bottom:0`, 8px margin-top, `--color-more-bar-bg/fg`,
  radius 3px, centred: "⌄ Scroll for more" (15px bold) plus a mono 13 summary at 80% opacity.
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
- Legend on the right.

### 6.2 Lane columns (`layout/board.ts: balanceLanes`)
- **Columns:** `cols = mainW ≥ 1150 ? 2 : 1`, with a 40px gap.
- **Column assignment:** *Unassigned* (if it has tickets) goes to column 1 first. Then walk speakers in **speaking
  order** (excluded speakers with tickets go after included ones), putting each lane into the column with the smaller
  height so far. Lane height = `30 + rows × laneRowH + 8`. Within a column, order is preserved.
  The prototype sorted by size; this is a deliberate change.
- **Lane row height:** 23px, or 20px if the tallest column exceeds the available height at 23px.
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
  initials (mono 12, right). Laid out in grid columns, filling top-to-bottom.
- **Backlog:** "BACKLOG" plus "top 12 by rank", with a 3px `--color-rule`. Rows are icon, key (muted) and title. If
  more exist: "Showing the top 12 as set in Configure · 28 more ranked below in Jira" (13px muted). Hidden entirely
  when backlog rows = 0.

### 6.4 More bar
Same component as §5.6. Summary: lanes whose header is below the fold (first names), then "Done (15)" and "Backlog (12)"
if those sections start below the fold.

## 7. Pulse track (`layout/track.ts`; inputs from Go `Pulse`)

- **Units:** everything is positioned in **day units** within an N-day window. Day `i ∈ [0, N)`, where `N − 1` is today.
  CSS: `left: calc(var(--x) / var(--days) * 100%)`, `width: calc(var(--w) / var(--days) * 100% − 2px)`.
- **Backdrop** (drawn once per list, behind the rows): weekend days filled with `--color-weekend` when shading is on, plus
  the today line (3px person view, 2px board, `--color-today`) at the right edge.
- **Segments:** one per contiguous lane span, colour `--color-lane-<lane>`, radius 2px, 2px gap between segments.
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
| `←` / `→` | Previous / next speaker (whole board after the last, first speaker after the whole board) | |
| `B` | Whole board | |
| `1`–`9` | Switch standup by position | |
| `R` / `Shift+R` | Resync incremental / full | |
| `/` | Focus the standup switcher (opens the menu in menu mode, else the first tab) | |
| `,` | Open Configure | |
| `?` | Key legend popover (keys + pulse legend) | |
| `Esc` | Close popover or menu. In Configure with no popup open, the same as Done. | |
| `Enter` | On a focused ticket, open it in Jira | |

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
| **Status mapping** | One row per board column in board order: column name → lane Combobox (Backlog / To Do / In Progress / In Review / QA / Done / Hidden) with a lane swatch. Auto-matched rows show a small "auto" tag until changed. If statuses aren't mapped, show a warning line. When two or more columns map to the same working or Done lane, an **Order within sections** block follows: one numbered list per shared lane with ↑/↓ (like the walk order); tickets in that lane are grouped in this order, rank order within a column. It defaults to board order. New standup: an explanatory paragraph until a board is chosen. |
| **Person view** | Walk order: an ordered list of the 4 lanes (swatch, name, "Jira: ‹columns›" mono 12 muted), presets **Right to left** and **Left to right**, then a locked Done row ("always last, so it's first below the fold"). The subtitle shows "Right to left (QA first)", "Left to right (To Do first)" or "Custom order". Rows: Segmented, *Fit when possible* / *Always roomy*, with the hint "Tighten before scrolling". |
| **Speakers** | Toolbar: Shuffle, A–Z, Most active, then the *Reshuffle daily* switch. An ordered list with include checkboxes (excluded rows muted, number "—") and a **Remove** button on every row (Undo toast). A removed person is not re-added by sync; adding them again restores them. Choosing a different board starts a fresh list from the next sync. Then *Unassigned as a speaker* (First / Last / Skip) in a dashed box. Then "Add someone from this space…" (Combobox, async `SearchUsers`, clears on select). Footnote: "Listed: anyone with tickets on this board in the last 30 days." After a sort action, a transient note on the right ("Shuffled just now"). New standup: an empty state until a board is chosen. |
| **Pulse** | Days shown (Segmented 7 / 14 / 21 / 28). A live **preview track** of a sample ticket from this board (axis + track, updates instantly). Marks: Commits / PRs / Comments switches. Status limits: 4 steppers in walk order with swatches, 0–99 (0 = none). Weekends: Shade / Plain. If dev-status is unavailable, a one-line warning: "Commit and PR marks unavailable — Jira's development info isn't reachable for this site." |
| **Whole board** | Done window: Sprint / 14 days / Since yesterday (Sprint is disabled on Kanban). Backlog rows: Stepper 0–50, with the hint "top of the ranked backlog · 0 turns it off". |
| **Display** *(app-wide)* | Theme: Light / Dark / System. Text size: Standard / Large (TV). Sync on open: When stale / Always / Never, plus "Stale after" Stepper in hours (1–48, default 6), shown only when *When stale* is selected. |
| **Export** *(per standup)* | One row per destination scoped to this standup or to all standups: name, kind ("File (JSON)"), destination detail (folder path, mono 13, middle-ellipsis), *Send on* Segmented (Manual / After each sync), **Send now**, and a status line ("Delivered 2 min ago · cuisle-MOB-2026-11-23.json", or "Failed · Retry" in alert text). **+ Add destination** opens a Combobox of sink kinds (v1: File (JSON)), then the kind's fields, rendered from its `ConfigSchema` (folder uses the OS folder picker). Everything autosaves. Delete uses Undo. |
| **Connection** *(app-wide)* | Site, account email, "Connected" tag, **Reconnect** (opens the Connect form inline) and **Disconnect** (confirm). |

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
  dark both verified in CI (axe in Playwright).
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
`--scale: 1.25` multiplies every font, row height and spacing token. Breakpoints are compared after scaling, so a 1920
window at Large behaves like a 1536 window at Standard.

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
| Board lanes ordered by ticket count | Speaking order, balanced across columns (§6.2) |
| Person view at 1920 uses 34px side-heading rows | 38px rows with side headings (`compact-side`, §5.5) |
| Mockup dropdowns look like plain selects | Every dropdown is an autocomplete Combobox |
| No Connection section | Configure → Connection (app-wide), plus the first-run Connect screen |
| No Export section | Configure → Export (per standup), §9.4 |
| Footer hint "press D or K to open them" (old round) | Removed; lists are always drawn |
