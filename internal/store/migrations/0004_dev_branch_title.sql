-- 0004: pull-request title and source branch for the ticket modal's Development group (F12 follow-up; data-model.md §2.5).
-- Forward-only, additive: no record row is changed or dropped. Existing rows keep NULL and render as before (the
-- repository as the PR text, no Branch row).
ALTER TABLE dev_pr_event ADD COLUMN title  TEXT;   -- PR title (dev-status detail pullRequests[].name); NULL = not known
ALTER TABLE dev_pr_event ADD COLUMN branch TEXT;   -- PR source branch (pullRequests[].source.branch); NULL = not known

-- Backfill: forget the dev-status fingerprints so the next sync re-reads the detail once (the requests it already makes
-- for dev targets; none are added) and fills title and branch on the rows above. The rows themselves are untouched.
UPDATE issue SET dev_fingerprint = NULL WHERE dev_fingerprint IS NOT NULL;
