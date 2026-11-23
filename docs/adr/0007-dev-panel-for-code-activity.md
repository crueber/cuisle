# ADR 0007 — Commit/PR marks from Jira's development panel

Status: Accepted

## Context
Pulse tracks show commit ticks and PR diamonds. Sources: each forge's API (GitHub/GitLab/Bitbucket — separate auth,
repo discovery, issue-key matching) or Jira's development-information data, which already links commits/PRs to issues for
whatever forge the site has connected.

## Decision
Read code activity **only** from Jira's dev panel: `GET /rest/dev-status/latest/issue/summary` and `.../issue/detail`
(`jira-sync.md` §2.11). Query only "dev targets" (tickets in In Progress / In Review / QA, plus recently moved ones),
skip detail calls when the summary fingerprint is unchanged, and store raw events (`dev_commit`, `dev_pr_event`).
The API is internal and undocumented, so it is treated as best effort: any failure marks `dev_activity_state='unavailable'`,
hides commit/PR marks and their legend entries, records a warning, and never fails a sync.

## Consequences
- One credential, forge-agnostic, no repo configuration.
- No stability guarantee; shape changes are absorbed in one decoder with fixture tests.
- Fidelity limits: commit lists may be truncated by Jira; PRs expose `lastUpdate` and status, not opened/merged times, so
  diamonds mean "PR activity that day" (risk R3).
- Costs one request per dev target per sync (the dominant incremental cost); bounded by the 4-request concurrency limit.
