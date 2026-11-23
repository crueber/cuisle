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
| **Lane** | Cuisle's normalised status bucket: `todo`, `prog` (In Progress), `review` (In Review), `qa`, `done`. Each Jira board column maps to a lane, or to `backlog` or `hidden`. |
| **Walk order** | The order lanes are presented in the person view. Fully user-defined across the four working lanes; Done is always last. Default is right to left: QA, In Review, In Progress, To Do. |
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
- Clicking a ticket opens it in the browser (`OpenIssue`).

### F4. Whole board
- Shown when no speaker is selected (rail "Whole board", `B`).
- **Lanes:** one per person in speaking order. *Unassigned* is pinned first with a red dashed rule and "N need an
  owner". Each row: icon, key, title, thin pulse track (segments and blocked only), days chip. Lane header: name,
  "N active · N done", and it's clickable to open the person view.
- One or two columns depending on width. Lanes are balanced across two columns by height.
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

### F9. Display and accessibility
- **Theme:** Light / Dark / System (default System). **Text size:** Standard / Large (TV), which scales the whole UI.
- Full keyboard operation.
  - ←/→ speakers, `B` whole board, `1`–`9` standups, `R` resync, `?` key legend, `/` focus the standup switcher, `,` opens
    Configure, `Esc` closes popups.
  - Global keys are ignored while typing.
- WCAG 2.2 AA contrast for text, 44px minimum hit targets for primary controls, real buttons, inputs and labels, and
  visible focus.

## 5. Out of scope for v1
- Editing Jira issues (transitions, assignment, comments). Cuisle is read-only against Jira.
- Mobile and tablet layouts; web or hosted version; multi-user sync of settings.
- OAuth 2.0 (3LO) and scoped API tokens. Data Center or Server Jira.
- Direct forge integrations (Forgejo/GitHub). Code activity comes only via Jira's dev panel.
- The History view UI, range exports, and sinks other than File (JSON). The schema, interfaces and outbox are in v1;
  the adapters and UI come later.
- Auto-update, telemetry, crash upload.
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
| D7 | Ticket click | Opens the issue in the default browser. |
