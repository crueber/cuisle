# ADR 0011 — Keep synced data forever; make membership and config temporal; build every view "as of"

Status: Accepted (schema and recording in v1; historical view UI later)

## Context
A future "historical view" should show any standup as it stood on a past date: who had what, in which lane, for how long,
with its pulse. Jira cannot answer that after the fact — sprint/board membership, the board's column mapping and Cuisle's
own settings at that time are not recoverable, and Jira's dev panel only ever exposes the latest state. The first design
treated synced data as a disposable cache (membership replaced per sync, activity pruned after 35 days).

## Decision
1. **Record tables are never pruned** (`issue`, `status_change`, `assignee_change`, `flag_change`, `comment`,
   `dev_commit`, `dev_pr_event`, `person`, `sprint`, `status`, `issue_type`). Change rows are upserted by natural key and
   never deleted, including on re-hydration. Issues that leave every board keep their row (`removed_at`, `last_seen_at`).
2. **Assignee history is stored** (`assignee_change`, from the same bulk changelog call as status and flag).
3. **Membership is temporal**: `standup_issue` stays as the current-state table for the hot path, and
   `standup_issue_span(standup_id, issue_id, role, valid_from, valid_to)` is maintained in the same apply transaction.
4. **Effective config is temporal**: `standup_config_history` gets a new JSON row (status→lane map, limits, walk order,
   speakers, scope, options, active sprint ids) whenever its fingerprint changes at a sync. No per-day snapshot tables.
5. **Every view is built as of a time**: `view.Build(snapshot, asOf)`; `pulse` takes `asOf` instead of "now". Loaders:
   `cache.LoadCurrent` (v1) and `history.LoadAsOf` (stub in v1). `HistoryService` is bound as a placeholder.
6. **Retention default is forever.** `app_setting.history_retention_days` is reserved (`0`); no cap is implemented in v1.
   Only operational data is trimmed (autocomplete caches, sync runs/log, outbox payloads).

Not historised (accepted): issue summary, type and rank (current values are shown); anything before a standup's first sync.

## Consequences
- The historical view becomes a loader plus UI, not a rewrite; exports of historical ranges reuse it (ADR 0012).
- Fidelity is per sync run for membership/config and exact for status, assignee, flag and activity timestamps.
- Growth ≈ 4–5 MB per busy board per year (`data-model.md` §5.1); the live path is unaffected because it reads
  current-state tables. A benchmark fixture with 3 simulated years guards this.
- Synced tables are no longer "drop and resync" in migrations: they must be migrated in place, like config.
- Deleting a standup archives it (hidden, not synced); its spans and config history are kept, so a future history view can still show it.
