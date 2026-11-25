# Handoff: command palette with ticket finder (F14)

**For Claude Code.** This is a new feature on the shipped app, designed with Chris on 2026-11-25 (design canvas Rounds 9
and 10). **Build F13 (board grouping, `docs/features/board-grouping/`) first**: this palette registers its "Group
board by…" command.

## What Chris asked for
"A button to launch a ticket finder. It should fuzzy find based on the ticket ID. When the ticket is found, it should
bring it up in the ticket detail view." Then: "It also makes it the beginning of a command palette, which I think is
ideal… Fuzzy finder and command palette."

## What it looks like (Read these PNGs first)
| File | Shows |
|---|---|
| `design/mockups/palette-empty-1920x1080.png` | The **Find ⌘K** header button (active), and the palette when empty: recent tickets + suggested commands. Footer: key hints and the prefix legend (`>` commands, `#` tickets, `@` people). |
| `design/mockups/palette-ticket-1920x1080.png` | Query `1429`: **exact** ticket first, then tickets **one character off** (1428, 1421, 1629), then ticket commands ("Refresh MOB-1429 from Jira"). Matched characters are inked. |
| `design/mockups/palette-commands-1920x1080.png` | `>` mode, query `sta`: fuzzy command matches. Each row has an icon, the name, context detail ("now: assignee"), a category, a shortcut and `›` for nested commands. A disabled command is shown with its reason. |
| `design/mockups/palette-people-1920x1080.png` | `@` mode, query `no`: people across all standups (initials, active count, standup key), plus Unassigned. |
| `design/mockups/palette-nested-1920x1080.png` | A nested command: a `Theme ›` breadcrumb chip, options with the current one checked, and a `⌫` back hint. |

- **Canvas:** https://claude.ai/artifact/ByQrCUJ3PUPfcjdwKG9ASg#page-r10. Round 9 has the earlier finder-only states,
  including "key not synced → fetch from Jira"; that state is part of this spec.
- **Alternatives:** the header search field and the "jump to key" overlay were **not chosen**.
- **Mockup vs spec:** the mockups use Saffron data over an older person-view backdrop with five standup tabs. Where
  they differ from the real app, the spec below wins.

## Spec (add as ux-spec §15, product-spec F14, keyboard map §8, AGENTS §10 decision)

### 15.1 Opening and closing
- **Header button:** **Find** with a search icon and a `⌘K` chip (`Ctrl K` on Windows/Linux). 44px outlined, placed
  before Resync. Icon-only below 1280. Its `aria-label` is "Find a ticket or run a command (⌘K)".
- **Shortcuts:**
  - **⌘K / Ctrl+K opens it everywhere:** standup, Configure, and on top of the ticket modal, where Esc returns to the
    modal.
  - `T` opens it when no field has focus, outside the ticket modal.
  - Typing `#`, `@` or `>` as the first character switches mode. `/` stays the standup switcher.
- **Layout:**
  - A dialog 860px wide (`min(860, vw − 48)`), top 96px (64px when `vh < 800`), 3px ink border, radius 4.
  - The scrim is `--color-scrim` at the lighter `.35`; the app stays readable behind it.
  - Results scroll inside the dialog, max height `vh − top − 48`.
- **Esc unwinds:** a nested level first, then the mode chip, then it closes. Focus returns to the opener.
- **Every action closes the palette**, except entering a nested command. The palette keeps no state between opens
  except recents.

### 15.2 Input and modes
- **Input:** 62px tall, search icon, mono 22 semibold. Placeholder: "Find a ticket or run a command".
- **Mode chip:** `>`, `#` or `@`, mono 15 bold on an ink fill, sits before the text. `⌫` on empty input removes it.
- **No prefix:** search everything. Ordering:
  - If the query contains a digit, tickets come first, then commands, then people.
  - Otherwise commands first, then people, then tickets.
  - Empty sections are omitted.
- **Empty query** (no mode): RECENT TICKETS (last 3 opened, from any entry point), then suggested COMMANDS (the five
  most-run, falling back to Resync, Group board by…, Go to person…, Switch standup…, Open Configure).

### 15.3 Ticket matching (key only in v1; titles are not searched)
> **Superseded 2026-11-25:** titles are searched too (words, any order, fuzzy; key tiers first). The current rules are in
> `docs/ux-spec.md` §15.3; the text below is the original key-only design.
- **Normalise the query:** lowercase, drop spaces and `-`. Split it into a letters part and a digits part: `mob1429`
  → `mob` + `1429`, `m14` → `m` + `14`, `1429` → `` + `1429`.
  - The letters part must be a **prefix of the project key** (`m` → MOB; `pl` → PLAT).
  - Empty letters means any project.
- **Candidates:** every synced issue for the connection, across all standups, including Done and tombstoned issues.
  Tombstoned ones get a muted "not on board" note.
- **Tiers**, with in-tier order: current standup first, then active before done, then key ascending.
  1. **Exact:** the digits equal the issue number.
  2. **Starts with:** the digits are a prefix of the issue number (`m14` → 1407, 1411…). Show the first 10, then "↓ N
     more · keep typing".
  3. **Close (one character off):** the same length with one substituted digit, or two adjacent digits swapped
     (1429 → 1428, 1421, 1629, 1249). Show at most 5. Labelled "one character off".
- **Highlighting:** inked characters, ink background with paper text. Exact and starts-with highlight the matched
  digits; close highlights the matching digits and leaves the differing one plain.
- **Result row (48px):**
  - type icon, key (mono 17 bold, highlighted), title (17 semibold, ellipsis)
  - lane swatch + lane name (12 bold caps)
  - assignee initials, or the dashed `··` box when unassigned
  - days chip (red when over limit; none for Done)
  - standup key (mono 12 muted)
- **Actions:** `↵` opens the ticket modal (`openTicket`). Use the standup the issue belongs to; if it is in several,
  use the current standup when it is a member, else the first by tab order. Step order is that standup's view order.
  `⌘↵` / `Ctrl↵` opens the issue in Jira.
- **Not synced:** a fully specified key (`mob-1729`) with no exact hit puts this row first and selected:
  "**MOB-1729** isn't synced in Cuisle · ↵ fetches just this ticket from Jira (one request), then opens it".
  - It calls `TicketService.FetchByKey` (below), then opens the modal in the current standup's lane mapping.
  - Errors show inline in that row: "Couldn't reach Jira · offline", "No issue MOB-1729 in Jira", or "You don't have
    access".
  - This is the palette's **only network call**. It goes through the limiter's interactive lane (jira-sync §3.1).
- **Ticket commands:** when a ticket is matched or the modal is open, append them under COMMANDS: "Refresh MOB-1429 from
  Jira" (respects the 10s cooldown), "Open MOB-1429 in Jira", "Copy MOB-1429".

### 15.4 Commands
- **Registry:** `frontend/src/commands/registry.ts`. Every user action is registered once and used by the palette,
  the key map, and later the key legend.
  ```ts
  interface Command {
    id: string;                       // "sync.resync"
    title: string | ((c: Ctx) => string);
    category: "Navigate" | "Sync" | "View" | "Display" | "Ticket" | "Configure" | "App";
    keywords?: string[];              // aliases searched too ("refresh", "pull" for Resync; "dark" for Theme)
    shortcut?: string;                // display only, e.g. "R", "⇧R", "G", "1–9", ","
    detail?: (c: Ctx) => string | undefined;          // "synced 1 min ago", "now: assignee"
    available?: (c: Ctx) => true | string;            // string = disabled reason, shown muted ("none waiting")
    run?: (c: Ctx) => void | Promise<void>;
    children?: (c: Ctx) => Command[];                 // nested (› chevron); mutually exclusive with run
  }
  ```
- **Initial set:**
  - **Navigate:**
    - Whole board `B`
    - Go to person… `@` (opens `@` mode)
    - Unassigned
    - Switch standup… `1–9` (children = the standups; the current one checked)
    - Open Configure `,`
    - Keyboard shortcuts `?`
  - **Sync:** Resync `R`, Full resync `⇧R`, Cancel sync (only while running)
  - **View:** Group board by status / Group board by assignee `G` (title follows the current mode; F13)
  - **Display:** Theme › (System / Light / Dark), Text size › (Standard / Large (TV))
  - **Configure:** New standup, Duplicate this standup
  - **App:**
    - Copy diagnostics
    - Check for updates / Install update (ADR 0016; disabled "none waiting" when there is nothing to install)
    - Reconnect Jira (only when auth is invalid)
  - **Ticket:** see §15.3.
- **Command matching:** fuzzy subsequence over title + keywords, fzf-style scoring:
  - a bonus for contiguous runs and word starts
  - a penalty for gaps
  - ties broken by recent use, then registry order
  - the matched characters are inked
- **Command row (48px):**
  - a 20px icon
  - title, 17 semibold
  - optional detail, mono 13 muted
  - a ✓ when it is the current choice
  - category (mono 11 bold caps, muted, 92px right-aligned)
  - shortcut kbd(s), 64px
  - `›` for nested commands
  - Disabled rows are muted and not runnable, with the reason as the detail.
- **Nested commands:** the input shows a breadcrumb chip (`Theme ›`) and the placeholder "Choose a theme". The
  children list filters as you type. `⌫` on an empty input goes back one level. Choices apply and save immediately
  (same settings as Configure → Display) and close the palette.

### 15.5 People (`@`)
- **Candidates:** every speaker in every standup, deduplicated by account, plus "Unassigned".
- **Row:**
  - initials (mono 13 bold, 22px)
  - name (17 semibold, fuzzy-highlighted)
  - "N active" (mono 13 muted)
  - "PERSON" category
  - standup key: the current standup if the person speaks there, else their first standup
- `↵` switches to that standup if needed, then selects the person (`goStandup(standupId, accountId)`).

### 15.6 Keyboard inside the palette
| Key | Action |
|---|---|
| `↑` `↓` | Move the selection (wraps); `Home`/`End`; `PageUp`/`PageDown` by 8 |
| `↵` | Run / open the selection |
| `⌘↵` / `Ctrl↵` | Open the selected ticket in Jira |
| `→` | Enter a nested command (same as `↵` on a `›` row) |
| `⌫` on empty input | Remove the mode chip, or go back one nested level |
| `Esc` | Unwind (§15.1) |
| `Tab` | Nothing; focus stays in the input |

- **Footer (36px):** `↑↓ choose · ↵ run / open · ⌘↵ open in Jira`, then the prefix legend `> commands # tickets
  @ people` on the right.
- **Accessibility:**
  - The input is an ARIA combobox with `aria-expanded`, `aria-activedescendant` and `aria-controls` pointing at the
    listbox. Options have `aria-selected`; disabled ones have `aria-disabled`.
  - A live region announces "N results".

### 15.7 Recents and persistence
- **Recent tickets:** the last 8 opened tickets (from the palette, rows, inline cards or stepping), most recent first,
  stored as app setting `palette.recent_tickets` (JSON array of issue ids). The empty state shows 3.
- **Command usage:** counts in `palette.command_usage` (JSON object id → count), used to rank suggestions and break
  ties.

## Data and API
- **`TicketService.FindByKey(ctx, query string, limit int) ([]TicketHit, error)`:** **local only, zero network**.
  - Implements §15.3 in Go over the `issue` table for the connection.
  - Returns `TicketHit {IssueID, Key, Summary, Type, Lane, StatusName, Assignee, AssigneeInitials, DaysInLane,
    OverLimit, Done, Removed, StandupIDs []int64, Tier "exact"|"prefix"|"close", Match []int /* char indexes to
    ink */}`.
  - The SQL narrows by project-key prefix and number range. The tier logic runs in Go. The p95 must be under 5ms for
    5,000 issues; add a benchmark.
- **`TicketService.FetchByKey(ctx, standupID int64, key string) (TicketDetail, error)`:** **network**, an explicit
  trigger.
  - It calls `source.GetIssue` by key, then the one-issue apply (jira-sync §4.8). The issue is stored without
    standup membership.
  - It builds the detail in that standup's lane mapping (architecture §3.2 rules for non-member issues).
  - Error codes: `offline`, `not_found`, `forbidden`, `rate_limited`, `auth_invalid`.
- **`StandupService.People(ctx) ([]PersonHit, error)`:** local. Speakers across all standups with active counts and
  standup ids, for `@` mode.
- No schema change. Recents and usage are app settings (`app_setting` keys above). Register the keys in architecture
  §6.4.
- Regenerate the bindings and extend `api/mock.ts` with fixtures for find, fetch and people.

## Work packages (same gate as the agent plan; P3 and P4 can run in parallel after P1)
| WP | Model | Owns | Done when |
|---|---|---|---|
| P1 | Haiku | `internal/view` or `internal/cache` key search + `TicketService.FindByKey`, `StandupService.People`, bindings, `api/mock.ts` | table tests for normalising and tiers (`1429`, `mob1429`, `m14`, `pl41`, `14 29`, transpositions, unknown prefix); zero-network test; benchmark under 5ms at 5k issues |
| P2 | Haiku | `TicketService.FetchByKey` (adapter `GetIssue` by key, the one-issue apply without membership) | jiratest: success opens; 404 → `not_found`; offline; 403; goes through the limiter's interactive lane; exactly one request |
| P3 | Haiku | `frontend/src/commands/*` (registry, fuzzy scorer, the initial command set); key map uses the registry for ⌘K/T | scorer golden tests (`sta` ranks "Group board by status" first, as in the mockup); `available()` reasons; nested children |
| P4 | Haiku | `frontend/src/views/palette/*` (dialog, input + mode chip + breadcrumb, sections, rows, footer), the header Find button, recents | Playwright screenshots vs `palette-*-1920x1080.png`; the keyboard table §15.6; axe clean (combobox and listbox); ⌘K works over the ticket modal and Configure; Esc unwinding |

## Ground rules
- AGENTS invariants apply, especially "no network without a trigger": typing in the palette never calls Jira, and only
  the explicit not-synced fetch does.
- Tokens only. No new runtime dependency: write the fuzzy scorer yourself.
- Keep the palette lazy-loaded with the ticket chunk, or on its own. It stays inside the 70 KB JS budget.
- Update ux-spec §8 (⌘K, `T`), the key legend and the rail footer hints ("⌘K find").
- Commit as `F14: command palette with ticket finder`.
- Push to Forgejo only when Chris asks. Never push to GitHub.
