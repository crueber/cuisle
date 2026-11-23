# ADR 0015: Ticket detail: sync full content, allow light writes

Status: Accepted (2026-11-23, Chris)

## Context
Chris wants any ticket to open inside Cuisle as a modal: summary, description and all comments, plus a dense field
panel at the right that renders custom fields properly. Descriptions must render Jira's rich content faithfully,
including images and attachments. During standup the group also wants to act on what they see: move a ticket,
reassign it, flag it, or leave a comment.

Three earlier decisions are in the way:
- Cuisle stored only comment *metadata* (AGENTS.md §7; architecture `model.Comment`).
- Cuisle was read-only against Jira (product-spec §5).
- Ticket click opened the browser (product-spec D7).

## Options considered
1. **Fetch on open, never store.** Keeps the old privacy rule. Opening a ticket is slow, nothing works offline, and the
   content can't feed history.
2. **Fetch on open and cache.** Fast after the first open, but the first open during standup still waits on Jira.
3. **Sync everything up front (chosen by Chris).** Resync pulls the description, all comments, attachment metadata and
   images, and every field value, including custom fields. The modal opens instantly and offline.

For editing: read-only plus an Open in Jira button; read plus comment; or **read plus light edits (chosen)**. Light
edits means: add a comment, change status via transitions, change the assignee, and flag or unflag.

## Decision
- **Sync stores ticket content locally.**
  - The description as ADF JSON, and comment bodies as ADF JSON with author, created/updated times and visibility.
  - Attachment metadata, plus image bytes for image attachments, within a cache cap. Thumbnails are always kept;
    full-size images are evicted least-recently-viewed past the cap.
  - Every field value (standard and custom) as typed neutral `FieldValue`s, with a per-connection field registry built
    from field discovery.
  - Non-image files are metadata only; they download on click.
- **Exports and logs still never contain comment bodies, emails or secrets.** Content stays in the local database. The
  `cuisle.export.v1` contract is unchanged.
- **Cuisle renders ADF itself**, in Go → a typed node tree → Preact components. It never injects Jira's `renderedFields`
  HTML. Unknown nodes degrade to their text plus "unsupported block ↗".
- **Light writes are explicit user actions**, so they are allowed network triggers under ADR 0004:
  - `TicketService.Transition`, `Assign`, `SetFlag` and `AddComment`. Each write is optimistic in the UI.
  - On success Cuisle re-fetches that one issue, applies it atomically, and records history (status, assignee and flag
    changes) exactly as a sync would.
  - On failure the UI reverts. A comment keeps its text with Retry / Edit / Discard. There is no automatic retry.
- **Transitions are fetched when the Status combobox opens** (an explicit trigger) and cached per issue.
  - Offline, the cached list is shown but disabled.
  - Transitions that need a Jira screen are listed with "↗ in Jira" and open the browser.
  - Assignee search uses the existing `SearchUsers` (Jira-backed typing).
- **Comment drafts autosave locally per issue.** They are never sent without Send.
- **History:** comments append; an edited comment updates its body and keeps `updated_at`. The description and other
  field values keep their current value only (like summary under D10). Status, assignee and flag stay fully historised.
- **Source interface:** content and writes are adapter capabilities (`Capabilities.Content`, `Capabilities.Writes`), so a
  future source can ship read-only. Sinks are unaffected.

## Consequences
- **JS budget:** the modal and ADF renderer add roughly 20 KB gzip. Chris raised the all-JS budget (ADR 0014) from
  50 KB to **70 KB gzip** for this feature (2026-11-23).
- **Deleting content:** never automatic. Disconnect offers "Also delete synced content" (unchecked) only when local
  content exceeds `storage.cleanup_offer_mb` (default 2048). History rows are never deleted.
- The DB grows. Estimate about 3–8 KB of ADF per issue plus comments, and images up to the cap (default 1 GB). Settings
  show cache size and a "Clear image cache" action.
- Resync does more work: description and fields come with the search page, comments page per issue, and images download
  in a bounded pool. The sync progress UI gains a "content" phase. Atomic apply still holds, so images land
  before the snapshot swap or are fetched lazily on first view.
- Privacy wording changes: comment bodies are stored locally (like summaries). Invariant 12 still forbids them in
  exports, logs and crash output.
- Write paths need fake-Jira support and tests (transition, assign, flag, comment, failure, 401, 409/400 with screen).
- Supersedes: product-spec D7 (ticket click now opens the modal; Open in Jira is a button and `O`) and the "read-only
  against Jira" out-of-scope line.
