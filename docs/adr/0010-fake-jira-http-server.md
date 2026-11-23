# ADR 0010 — Fixture mode is a fake Jira HTTP server, not a mocked client

Status: Accepted

## Context
Development and CI must not depend on a real Jira site, and the riskiest code is the HTTP client itself (token paging,
429 handling, decoding of loosely specified payloads).

## Decision
`internal/source/jira/jiratest` serves the endpoints listed in `jira-sync.md` §2 from a fixture generated from
`design/reference/sample-data.json` (`cmd/fixturegen`), with dates relative to "today", a small JQL subset, and fault
injection (latency, 429s, dev-status failures, 401, tiny pages, dataset mutation). `cuisle --fake-jira` runs the real app
against it with an in-memory keyring and a separate data dir; integration tests use the same server.
The frontend additionally has `api/mock.ts` (static view-model JSON) for browser-only UI and performance tests.

## Consequences
- The production client and sync code run unchanged in dev and tests; request counts are assertable.
- The fake must be kept in step with the endpoints we use (small, explicit surface) and is not a JQL engine.
- The mockup scenes are reproducible on any date and double as golden view-model tests.
