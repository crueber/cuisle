# ADR 0009 — Sync by scan-and-diff with a single atomic apply

Status: Accepted

## Context
Sync must be fast when little changed, exact about membership (sprint removals, deletions), cancellable, and must never
show a half-updated board. The conventional `updated >= lastSync − skew` delta misses issues that left the set and
depends on clock and timezone handling.

## Decision
Every sync **scans** each membership query with a minimal field set (`fields: ["updated"]`, large pages, rank order), diffs
against the cache, and **hydrates** only new/changed issues (`id in (…)` batches, bulk changelog fetch, comment top-ups,
dev-status for dev targets). A full sync is the same path with "hydrate everything" plus reference-data refresh.
All fetched data stays in memory; one `BEGIN IMMEDIATE` transaction per standup applies it (upserts — never deletes — of issues and
their change history, current-membership rewrite plus temporal membership spans, config-history row, speaker discovery,
sync state, `rev` bump; ADR 0011). Readers use WAL snapshots. Failure or cancellation before apply writes nothing.

The orchestrator talks only to `source.Source` (`Plan`/`Scan`/`Hydrate`/`DevActivity`); the Jira specifics live in the
adapter (ADR 0012).

## Consequences
- Exact membership and rank with 1–3 cheap requests; no skew logic anywhere.
- Memory holds one standup's changed issues during a run (a few MB worst case).
- The writer is blocked for the apply (< 150 ms typical); autosave tolerates this.
- Dev activity does not bump an issue's `updated`, so dev targets are re-checked every sync regardless of the diff.
