# ADR 0012 — Provider-neutral core with inbound (source) and outbound (sink) adapters

Status: Accepted (interfaces, schema and one reference sink in v1; other adapters later)

## Context
Cuisle starts with Jira Cloud, but Jira should not be hard-wired: the owner wants to be able to send standup data to
other project-management systems, and possibly read from another one later. Jira types had leaked into table names
(`jira_*`), the sync orchestrator and the service API.

## Decision
- **Neutral model** `internal/model` (Person, Space, Board, Column, Status, IssueType, Sprint, Issue, StatusChange,
  AssigneeChange, FlagChange, Comment (metadata), DevEvent, BoardSnapshot). stdlib only.
- **Neutral store**: tables lose the `jira_` prefix (`connection`, `space`, `board`, `board_column`,
  `board_column_status`, `status`, `issue_type`, `sprint`, `person`, `issue`, `status_change`, `assignee_change`,
  `flag_change`, `comment`, `dev_commit`, `dev_pr_event`). `connection.source` names the provider; ids are the provider's
  ids as text (`issue.source_id`); provider extras go in `source_meta` JSON. User-facing names follow suit
  (`ConnectionService`, `connection:auth`, `extra_filter`, `ValidateFilter`, error codes `source_http`, `filter_invalid`).
- **Inbound**: `internal/source.Source` (Test, SearchSpaces, SearchBoards, BoardConfig, ActiveSprints, Reference,
  SearchUsers, ValidateFilter, Plan, Scan, Hydrate, DevActivity, IssueURL) with `Capabilities` flags and a registry.
  `internal/source/jira` is the only implementation; `jiratest` remains its HTTP test double. `internal/sync` imports
  the interface only; membership queries are produced by the adapter's `Plan` and are opaque to the core.
- **Outbound**: `internal/sink.Sink` (Kind, Capabilities, ConfigSchema, Validate, Deliver) with a registry and an
  **outbox** (`sink`, `sink_outbox`): payload frozen at enqueue, idempotency key, bounded retries, no timers. Triggers are
  explicit only: manual Send, or opt-in "after each successful sync" (consistent with ADR 0004). Secrets in the keychain.
- **Export contract**: `cuisle.export.v1` JSON document (`architecture.md` §3.5), additive-only within v1, built by
  `internal/export` from the same `StandupView` the UI shows (current or, later, a historical range via ADR 0011).
- **v1 scope**: interfaces, schema, dispatcher, `SinkService`, and one reference sink — `file` (JSON). Later: webhook,
  CSV/Markdown file formats, Linear, GitHub Projects, Asana, Notion, Azure Boards; additional sources.

## Consequences
- Adding a destination is one package implementing `Sink` plus registration in `main`; no core change.
- The source abstraction is shaped by one provider and will likely need adjusting for a second (risk R21); it is an
  internal Go interface, not a plugin ABI, so that is a refactor.
- A CI layering check forbids `sync`, `cache`, `view`, `pulse`, `history`, `export` from importing `source/jira`.
- Slight indirection cost in the Jira path (DTO → model mapping); negligible.
- Sinks see exactly what the user sees (the view model), not raw provider data — simple and privacy-preserving, but a
  sink that needs raw fields must extend the export schema first.
