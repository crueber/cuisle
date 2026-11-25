# Cuisle: product spec

*Cuisle* (Irish for "pulse", pronounced roughly *KUSH-la*) is a desktop app for running a team standup or sync-up
from a Jira Cloud board. It puts every ticket for the person speaking on one screen, shows how each ticket has moved
recently (its *pulse*), and keeps the whole board one keystroke away.

Companion docs:
- [ux-spec.md](ux-spec.md): screens, layout rules and interaction.
- [architecture.md](architecture.md): how it's built.
- [data-model.md](data-model.md): SQLite schema.
- [jira-sync.md](jira-sync.md): Jira API usage and sync.
- [roadmap.md](roadmap.md): milestones.
- [testing.md](testing.md): test plan.
- `design/mockups/`: approved visuals (private development repository only; see `docs/screenshots/` for the app as built).

## 1. Who it's for

| User | Need |
|---|---|
| **Facilitator** (engineering manager, team lead, scrum master) | Runs one or more standups a day, often several teams back to back. Shares the window over Zoom/Teams at 1080p. Wants to walk people in a set order, switch teams instantly, and never miss a ticket. |
| **Team member** | Sees their own tickets clearly while they talk. Is helped by the pulse ("this has been in Review for 6 days") without being shamed by it. |
| **Viewer on the call** | Reads the shared screen through video compression. Needs large, high-contrast type and nothing that relies on hue alone. |

## 2. Principles (apply these when a detail isn't specified)

1. **Nothing is hidden to make things fit.** Every ticket in scope is drawn. Layout may tighten (row density), then it
   scrolls with a clear "Scroll for more — X below" indicator. Done tickets are always last, so they're the first to
   go below the fold. Configured limits are fine (for example backlog rows = 12) as long as the screen says so.
2. **Instant from cache, sync only on request.** The app opens on the last standup in well under a second using data
   cached from the previous sync. It never polls. The user resyncs with a button or key, or optionally on open when the
   data is stale.
3. **Everything saves itself.** There's no Save button. Every change in Configure is persisted immediately; a quiet
   indicator confirms it. Destructive actions can be undone.
4. **Every dropdown is an autocomplete.** You can always type to filter.
5. **Legible over a screen share.** Minimum 13px mono and 15–16px sans for content, weights 500+, no 1px hairlines,
   and meaning carried by shape and lightness as well as hue.
6. **Light runtime.** Fast start, small memory, a small JS bundle (budgets in architecture.md §6.1).

## 3. Glossary

| Term | Meaning |
|---|---|
| **Standup** | A saved configuration: one Jira space (project) and board, plus speakers, walk order, pulse and board settings. A user can have many. Shown as header tabs. |
| **Space** | Jira project (Jira's newer UI calls them spaces). |
| **Lane** | Cuisle's normalised status bucket: `todo`, `prog` (In Progress), `review` (In Review), `qa`, `done`, plus working lanes the user names in Status mapping (ids `c1`, `c2`, …, each with its own colour). Each Jira board column maps to a lane, or to `backlog` or `hidden`. Lane names are configurable (e.g. In Review → Code Review); ids never change. |
| **Walk order** | The order lanes are presented in the person view. Fully user-defined across the working lanes (the four built-in ones and any you named); Done is always last. Default is right to left: QA, In Review, In Progress, To Do. |
| **Speaker** | A person in the standup's speaking order. Auto-discovered from assignees, can be excluded, reordered, or added manually. *Unassigned* is a pseudo-speaker. |
| **Pulse** | The per-ticket timeline track: status segments over the last N days, plus commit, PR and comment marks, blocked spans, weekends and today. |
| **Days in lane** | Calendar days since the ticket entered its current lane. Shown as a chip; red when over the lane's limit or when blocked. |
| **Over limit / stale** | Days in lane > the lane's limit (0 = no limit). |
| **Blocked** | Jira *Flagged* is set, or the status name is Blocked, Impeded or On hold. |
| **Person view** | One speaker's tickets. |
| **Whole board** | Every active ticket by person, then the Done list and the backlog. |
| **Sync** | Pulling the board's issues and history from the source (Jira) into the local store. Synced data is **kept**: it accumulates history, it isn't a disposable cache. |
| **Source** | An inbound adapter that reads a project-management system. v1 has one: Jira Cloud. The core is provider-neutral (ADR 0012). |
| **Sink** | An outbound adapter ("destination") that sends standup data to another system. v1 has one: File (JSON, `cuisle.export.v1`). |

## 4. Features and acceptance criteria

IDs are referenced from the roadmap and tests.

### F1. Connect to Jira
- First run shows a **Connect** screen with site URL (`https://<site>.atlassian.net`), email and API token, and a link to
  Atlassian's "create API token" page. Classic (unscoped) API tokens only in v1, and the screen says so.
- "Connect" verifies with `/rest/api/3/myself`, stores the token in the OS keychain, and stores site and email in the DB.
  **AC:** the token never appears in SQLite, logs or crash reports. A wrong token shows an inline error with no crash.
- Configure → Connection (app-wide) shows the connected account, with Reconnect and Disconnect.
- If the keychain is unavailable (for example a Linux session with no Secret Service), show a clear error that names the
  missing service. Never fall back to plaintext.

### F2. Standups (multiple boards)
- Create, rename, duplicate, delete (soft, with a 10 s **Undo** toast) and reorder standups. Order = tab order =
  shortcuts `1`–`9`.
- **New standup:** name, *Start from* (defaults, or copy another standup's settings), space (autocomplete), board
  (autocomplete, filtered to the space). Speakers and status mapping fill in automatically once a board is chosen.
- **Switching:** one click on a header tab, or a number key. With many standups the tabs collapse to Jira keys, then to a
  switcher menu (2 clicks, still type-to-filter). The active standup is always visible.
- **AC:** switching to a cached standup paints in ≤ 50 ms (p95), with no network call.

### F3. Person view
- Selecting a speaker (rail, ←/→, or clicking a lane title on the whole board) shows all of their tickets in scope,
  grouped by lane in **walk order**, then Done.
- **Row:** issue-type icon, key, title (ellipsis), BLOCKED tag, pulse track, days-in-lane chip.
- **Density ladder:** 46px rows with stacked headings, then 46px with side headings (lane names move to a left gutter),
  then 38px stacked, 38px side, and 34px side. It uses the first step at which everything fits, unless Rows is set to
  *Always roomy* (ux-spec §5.5). If even the last step doesn't fit, it keeps that step and scrolls. The pinned **more bar** reads e.g. "Scroll for more — 2 To Do · 6 Done below",
  then "End of list".
- **AC:** with 20 tickets (14 active + 6 done) at 1920×1080, everything is visible without scrolling, at 38px rows with
  side headings (layout as `design/mockups/person-1920x1080.png`). At 1024×640 nothing is omitted; the more bar names what's below.
- Clicking a ticket opens the ticket modal (F12). Open in Jira (`OpenIssue`) is in the modal and on `O`.

### F4. Whole board
- Shown when no speaker is selected (rail "Whole board", `B`).
- **Lanes:** one per person in speaking order. *Unassigned* is pinned first with a red dashed rule and "N need an
  owner". Each row: icon, key, title, thin pulse track (segments and blocked only), days chip. Lane header: name,
  "N active · N done", and it's clickable to open the person view.
- One, two or three columns depending on the (zoomed) width. Unassigned, then the speakers in speaking order, are cut into
  contiguous columns of the least height, so speaking order reads down, then across (F15).
- **Done list:** every Done ticket in the window (*Sprint* / *14 days* / *Since yesterday*), with assignee initials.
- **Backlog:** the top N by rank (N configurable 0–50, default 12; 0 hides the section), with "Showing the top 12 as set in
  Configure · 28 more ranked below in Jira".
- Title row: sprint name and end, or "Kanban · continuous flow", plus counts for active, unassigned (red), done and backlog.
- **AC:** nothing collapses to a count or a summary. If it doesn't fit, it scrolls with a more bar naming the lanes and
  sections below.

### F5. Pulse
- Window of N days (7/14/21/28, default 21) ending today, in the OS timezone.
- **Segments** coloured by lane from the changelog, including the state before the window.
- **Marks:** commits are ticks whose height grows with count (1–3 levels). PR activity is a diamond. Comments are short
  grey ticks. Each mark type can be toggled.
- Blocked spans are hatched. Weekends are shaded (toggle). A today line sits at the right edge. Marks only appear after the
  ticket existed.
- **Data sources:** status history and comments from Jira; commits and PRs from Jira's development information. If that's
  unavailable, show a one-line warning in Configure → Pulse and omit those marks without failing the sync.
- Per-lane limits drive the red days chip. Defaults: QA 3, In Review 2, In Progress 5, To Do 10; 0 = no limit.

### F6. Speakers
- Auto-discovered from assignees on sync and included by default. Untouched auto-added people are pruned after 30
  days unseen.
- An ordered list with up/down, include checkbox, initials, name and "N active".
- **Shuffle**, **A–Z**, **Most active**, and a *Reshuffle daily* switch (a stable seed per local date).
- *Unassigned as a speaker:* First / Last / Skip.
- "Add someone from this space…" autocomplete (assignable users).
- The rail lists included speakers in order with active counts, plus Unassigned. ←/→ walks the order; after the last
  speaker comes the whole board.

### F7. Configure
- Opened from the header (**Configure**) or the `+` next to the tabs (which creates a new draft standup).
- **Left rail:** standups (number, name, key, people count, up/down), **+ New standup**.
- **Form sections:** Source, Status mapping, Person view (walk order and Rows), Speakers, Pulse, Whole board, and
  Display (app-wide). Connection is app-wide too.
- **Autosave** on every change. Text fields debounce 400 ms. The header shows "Saving…" (only after 250 ms), then
  "Saved", or "Not saved · Retry". A single **Done** button returns to the standup.
- Changing the *source* (space, board, scope, extra JQL, column mapping) marks the standup "needs sync" and starts a
  full sync when leaving Configure. View-only settings never need a sync.
- Responsive: 3 columns, then 2, then 1. When there's more than one column, a pinned row of section chips jumps to each
  section.

### F8. Sync
- **Triggers:**
  - *Resync* button in the header, or `R` (`Shift+R` for a full sync).
  - On open, per policy: *When stale* (default, older than 6 h), *Always*, or *Never*.
  - Leaving Configure after source changes.
- Progress shows in the header (phase and count). The UI keeps showing the cached data until the new data is applied
  atomically.
- Offline or auth failures: cached data stays visible. A manual Resync that fails shows a banner ("Offline — showing data from 2 h ago" or "Jira
  rejected the token — Reconnect"). Never an empty screen.
- Header shows "synced 2 min ago" and the warning icon when the last run was partial.
- **AC:** no network activity occurs without one of the triggers above (verified by test with the fake Jira's request log).

### F10. Export to other systems (sinks)
- Configure → **Export** (per standup) lists destinations for this standup. "+ Add destination" lists the registered sink
  kinds; v1 has **File (JSON)**.
- **File (JSON) settings:** name, folder (OS folder picker), and *Send on*: Manual / After each successful sync.
- **Each destination row:** "Send now", the last delivery ("Delivered 2 min ago · cuisle-MOB-2026-11-23.json"), or
  "Failed · Retry".
- **Delivery** goes through a durable outbox with idempotency keys and up to 8 attempts. Retries happen only on an
  explicit trigger (Retry, or the next successful sync), so there are no timers or polling.
- **Payload:** the versioned `cuisle.export.v1` document (architecture §3.5). It never contains comment bodies, emails
  or secrets.
- **Later:** webhook, CSV and Markdown standup notes, Linear, GitHub Projects, Asana, Notion, Azure Boards, and historical
  range exports. The interfaces and the outbox ship in v1, so these are adapters, not rewrites.
- **AC:**
  - Send now writes the file atomically and records the delivery.
  - After-sync sends exactly once per successful sync (idempotency key).
  - No sink network or file activity happens without a trigger.

### F11. History (data kept now, view later)
- Every sync **appends**. Status, assignee and flag changes, comments metadata and dev activity are never pruned.
- Issue membership per standup is temporal (spans). Effective standup config is versioned (config history).
- Issues that leave the board are tombstoned, not deleted. Deleted standups are archived, not purged.
- View building takes an `asOf` time from day one (`view.Build(snapshot, asOf)`), so a future **History view** ("show this
  standup as of 3 Nov") reuses the same screens.
- **v1:** recording only; the `HistoryService` is bound but returns `not_implemented`. A weekly `VACUUM INTO` backup
  protects history that a resync can't rebuild.
- **AC:** after N syncs with changes, a test can reconstruct an earlier day's membership and lanes from the stored
  records (see testing.md).

### F12. Ticket detail (modal) with light edits
- Clicking a ticket anywhere (or `Enter` on a focused row) opens it in a modal over the standup (ux-spec §14).
- **Shows:**
  - summary, the full description rendered from Jira's rich text (headings, lists, tables, panels, code, mentions,
    status lozenges, inline cards, images)
  - attachments (thumbnails and file cards)
  - subtasks, linked issues and all comments
  - a dense field panel with standard and custom fields; empty fields are folded into one "Not set" line
- **Light edits, sent to Jira at once:**
  - change status (via the issue's transitions)
  - change assignee
  - flag / unflag
  - add a comment
  - Everything else goes through **Open in Jira**.
- **Refresh this ticket** (button and `R`): re-fetch just this issue from Jira without a board resync. There is a
  10-second cooldown per ticket.
- **Data:** Resync stores descriptions, comments, field values and attachment images locally (ADR 0015), so the modal
  opens instantly and works offline (edits need a connection).
- **AC:**
  - Opening a synced ticket makes no network request and renders in under 100ms (p95, fixture).
  - `J`/`K` step through the view's tickets without closing.
  - Each ADF node in ux-spec §14.4 renders as specified. Unknown nodes show their text and "unsupported block".
  - Every custom field kind in ux-spec §14.6 renders as specified, and empty fields have no rows.
  - **Status change:** the row behind the modal updates at once. On Jira failure the change reverts with the reason.
    The history records the change after the success re-fetch.
  - A comment that fails to post keeps its text with Retry / Edit / Discard. Nothing retries on its own.
  - Transitions that need a Jira screen open the browser instead.
  - A draft comment survives closing the modal and restarting the app.
  - Refresh re-fetches only that issue (the fake-Jira request log shows only that issue's requests). Changes are
    historised, the row behind updates, and a second Refresh within 10 s is refused locally with no request.
  - All Jira traffic goes through one throttle (jira-sync §3.1). A sync never exceeds the configured rate, and a 429
    pauses every request until `Retry-After`.
- **Storage:** Disconnect keeps synced content. It offers to delete it only when local content is over the storage
  cap (default 2 GB, set in Display; ux-spec §14.11). Nothing is ever deleted automatically.

### F13. Whole board: group by Assignee or Status
- A **Assignee | Status** switch in the Whole board's title row (and the `G` key) regroups the board. **Assignee** is
  today's board. **Status** shows one section per lane in walk order (QA, In Review, In Progress, To Do, whatever the
  standup defines), then Done, then Backlog, so you can see which status each ticket is in and who has it.
- Each section header carries the lane swatch, name, ticket count, and (only when non-zero) how many are over limit,
  blocked and unassigned, with the lane limit on the right. Rows gain an assignee column (initials; a dashed `··` box
  for an unassigned ticket) and sort over-limit first, then longest in the lane, then board rank.
- The choice is remembered **per standup** and restored on switching standups and on restart. It is UI state, not
  Configure: there is no save indicator.
- **AC:**
  - Nothing is hidden: status mode uses the same density, then the "Scroll for more" bar, which names the sections
    below the fold.
  - `G` does nothing on a person view, while typing, or while the ticket modal is open.
  - Ticket modal `J`/`K` follow the on-screen order (section order, then row order, then Done, then Backlog).
  - Title-row stats are the same in both modes. No new network request, no `StandupView` change (ADR 0008).
  - Option B (a column per status) was considered and rejected.

### F14. Command palette and ticket finder
- A **Find** button in the header (`⌘K` / `Ctrl+K`, or `T` with no field focused) opens one palette that finds a ticket by
  its key **or its title** and runs any app action. Typing `#`, `@` or `>` first narrows it to tickets, people or commands.
- Ticket search by key is fuzzy over the number: exact, starts with, and one character off (a typo or a swapped pair).
  Ticket search by title matches the words you type against the summary, in any order, whole words first, then word
  starts, then anywhere, then loosely ("rcd" finds "Recipe card crops"); the matched letters are inked. Keys rank before
  titles. Both search every synced issue of every standup, including Done ones. `↵` opens the ticket modal (F12);
  `⌘↵` opens Jira. Rules and examples: ux-spec §15.3.
- A fully typed key that has not been synced (`mob-1729`) offers one explicit "fetch just this ticket" request.
- It lists the recent tickets and suggested commands when empty, and ranks commands by how well they match and how often
  you run them.
- **AC:**
  - Typing in the palette never calls Jira; the only request it can make is the explicit fetch row, through the limiter's
    interactive lane, exactly one per press.
  - A title search finds the ticket by words from its summary, in any order and in part (`recipe card`, `rcd`), with the
    matched letters inked; numbers match titles too (contiguously, e.g. `404`); key matches rank first; it stays local.
  - ⌘K works over Configure and over the ticket modal (Esc returns to the modal); focus returns to where it was.
  - Nothing scrolls sideways at any size with Find in the header; it is icon-only below 1280.
  - Commands and lane names come from the data and the registry, never a fixed lane list.
  - Light and dark themes pass axe; the palette is a lazy chunk (the entry bundle stays inside its budget).

### F15. Zoom for the person view and the whole board
- A `− 100% +` control in the title row of the person view and the Whole board (and `+` `-` `0`, `⌘`/`Ctrl` with them, and
  three palette commands) zooms everything below the header between 70% and 200%. It is hard to read a standup board from a
  meeting-room screen; this makes it larger, or smaller to fit everyone in one view, without changing the OS or Text size.
- Zoom **re-runs the layout** on the effective size instead of just magnifying: the whole board goes to 3 columns when
  zoomed out, 2 or 1 when zoomed in, the rail goes compact, the person view walks its density ladder (side headings,
  tighter rows) and the more bar says what is below. Nothing is hidden to fit.
- One zoom per standup, shared by its person view and Whole board, remembered across restarts. The header does not zoom.
- **AC:**
  - At 1920 × 1080 the board has 3 / 2 / 2 / 1 columns at 80 / 100 / 150 / 175 % and the person view at 150% shows side
    headings; every change shows a 1.2 s toast ("150% · 2 columns").
  - `⌘`/`Ctrl` with `+` `=` `-` `0` zoom the screen and never page-zoom the window (the header stays its size).
  - The lane or group at the top of the scroll area stays at the top after a zoom change.
  - Nothing scrolls sideways at any of the nine levels; axe is clean in both themes; a zoom step paints inside the switch
    budget; no network, no `StandupView` change.

### F9. Display and accessibility
- **Theme:** Light / Dark / System (default System). **Text size:** Standard / Large (TV), which scales the whole UI.
- Full keyboard operation.
  - `⌘K`/`Ctrl+K` or `T` opens the command palette (F14),
    ←/→ speakers, `B` whole board, `G` group the board by Assignee or Status, `+` `-` `0` zoom (F15), `1`–`9` standups, `R` resync, `?` key legend, `/` focus the standup switcher, `,` opens
    Configure, `Esc` closes popups.
  - Global keys are ignored while typing.
- WCAG 2.2 AA contrast for text, 44px minimum hit targets for primary controls, real buttons, inputs and labels, and
  visible focus.

## 5. Out of scope for v1
- Editing Jira issues beyond the light edits in F12 (no field editing, no description editing, no comment editing or
  deleting, no transitions that need a Jira screen, no creating issues).
- Mobile and tablet layouts; web or hosted version; multi-user sync of settings.
- OAuth 2.0 (3LO) and scoped API tokens. Data Center or Server Jira.
- Direct forge integrations (Forgejo/GitHub). Code activity comes only via Jira's dev panel.
- The History view UI, range exports, and sinks other than File (JSON). The schema, interfaces and outbox are in v1;
  the adapters and UI come later.
- Telemetry and crash upload. (Auto-update was added 2026-11-24, ADR 0016: a daily check of the download channel, no telemetry.)
- Timers or "time per speaker" (a candidate for v1.x).

## 6. Product decisions (defaults chosen; revisit after first use)

| # | Decision | Default |
|---|---|---|
| D1 | Do lane limits apply to To Do? | Yes, all four lanes; set 0 to disable per lane. |
| D2 | Days counted | Calendar days (chips can turn red over a weekend). A "working days" toggle is a v1.x candidate. |
| D3 | "Since yesterday" Done window | Since the previous working day (Monday looks back to Friday). |
| D4 | Multiple active sprints on a board | All in scope; the header names the one ending first. |
| D5 | Header wordmark | "CUISLE" (mockups say "STANDUP"). |
| D6 | Module path / bundle id | **Decided:** `github.com/crueber/cuisle`, `crueber.cuisle`. |
| D8 | Code signing | **Decided:** self-signed for now (ADR 0013). |
| D9 | Deleted standups | Archived, never purged (history is kept). Archive/restore list is post-v1. |
| D10 | History granularity | Status, assignee, flag, membership and config are historised. Summary, type and rank show current values in a past view. |
| D7 | Ticket click | **Changed 2026-11-23:** opens the ticket modal (F12). Open in Jira is a button and `O`. |
| D12 | Deleting synced content | **Decided:** only offered at Disconnect, and only when local content exceeds the storage cap (default 2 GB). |
| D11 | Ticket content | **Decided:** sync it all up front (ADR 0015); comments newest first; light edits only. Layout A "Docket" only; the side-sheet and three-column variants were rejected as too busy. |
