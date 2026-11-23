# ADR 0005 — Autosave everything; undo instead of confirm

Status: Accepted (supersedes "Save & back to standup" / "Cancel" in the mockups)

## Context
Configure has dozens of small controls. A Save button invites lost changes and a Cancel button needs draft state.

## Decision
Every control persists on change through a bound call that runs one SQLite transaction and returns `SaveAck{rev, savedAt,
needsSync}`. Text inputs debounce 400 ms and flush on blur/Enter/navigation/window close; everything else saves
immediately. Writes are serialised per field key with last-write-wins coalescing; the UI is optimistic.
A quiet indicator shows `Saving…` (only after 250 ms), `Saved`, or `Not saved · Retry`. Configure's header has a single
**Done** button back to the standup. "New standup" inserts a draft row immediately.
Destructive actions are not confirmed with dialogs: **Delete standup** is a soft delete (`standup.deleted_at`) with a
10-second Undo toast (`ConfigService.Restore`); afterwards the standup stays archived (never purged, ADR 0011). Full pipeline: `architecture.md` §5.3.

## Consequences
- No draft/dirty model, no "unsaved changes" prompts; there is no way to discard a batch of edits (accepted).
- Validation must be per-field and non-blocking: invalid values stay in the control with an inline message and are not
  persisted; the last valid value remains in the DB.
- Each mutation needs a narrow bound method and bumps `standup.rev`.
- Source-affecting edits mark the standup `needs_sync` rather than syncing on each keystroke (ADR 0004).
- Exception: **Disconnect** (Configure → Connection) uses a confirm dialog, because deleting the keychain token can't be undone.
