-- testdata/v4.sql: a populated database taken when schema version 4 was current (data only; apply on top of 0001–0004).
-- Every record, config and operational family has at least one row. Counts are asserted in migrate_test.go
-- (see v1Counts there), so changing this file means changing those counts.

INSERT INTO app_setting (key, value, updated_at) VALUES ('theme', 'dark', 1000), ('sync_stale_hours', '6', 1000);

INSERT INTO connection (id, source, base_url, account, account_id, display_name, auth_state, created_at)
VALUES (1, 'jira', 'https://acme.atlassian.net', 'me@acme.com', 'acc-me', 'Me', 'ok', 1000);

INSERT INTO standup (id, connection_id, name, position, space_id, space_key, space_name, board_id, board_name, board_type, scope, created_at, updated_at)
VALUES (1, 1, 'Platform', 0, '10', 'MOB', 'Services', '7', 'MOB board', 'scrum', 'sprint', 1000, 1000),
       (2, 1, 'Archived team', 1, '10', 'MOB', 'Services', '8', 'Old board', 'kanban', 'board', 1000, 1000);
UPDATE standup SET deleted_at = 5000 WHERE id = 2;

INSERT INTO standup_settings (standup_id) VALUES (1), (2);
INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days) VALUES
  (1, 'qa', 0, 3), (1, 'review', 1, 2), (1, 'prog', 2, 5), (1, 'todo', 3, 10);
INSERT INTO standup_column_map (standup_id, column_name, position, lane) VALUES (1, 'In Progress', 1, 'prog');
INSERT INTO standup_status_lane (standup_id, status_id, status_name, column_name, lane) VALUES (1, '10', 'In Progress', 'In Progress', 'prog');
INSERT INTO standup_speaker (standup_id, account_id, display_name, position, created_at) VALUES (1, 'acc-ada', 'Ada Lovelace', 0, 1000);
UPDATE ui_state SET last_standup_id = 1, updated_at = 2000 WHERE id = 1;
INSERT INTO standup_ui_state (standup_id, last_person, updated_at) VALUES (1, 'acc-ada', 2000);

INSERT INTO space (connection_id, id, key, name, fetched_at) VALUES (1, '10', 'MOB', 'Services', 1000);
INSERT INTO board (connection_id, id, name, type, space_id, fetched_at) VALUES (1, '7', 'MOB board', 'scrum', '10', 1000);
INSERT INTO board_column (connection_id, board_id, position, name) VALUES (1, '7', 1, 'In Progress');
INSERT INTO board_column_status (connection_id, board_id, position, status_id) VALUES (1, '7', 1, '10');

INSERT INTO status (connection_id, id, name, category) VALUES (1, '10', 'In Progress', 'indeterminate'), (1, '11', 'Done', 'done');
INSERT INTO issue_type (connection_id, id, name, kind) VALUES (1, '1', 'Story', 'story');
INSERT INTO sprint (connection_id, id, board_id, name, state, start_at, end_at, fetched_at) VALUES (1, '5', '7', 'Sprint 5', 'active', 1000, 9000, 1000);
INSERT INTO person (connection_id, account_id, display_name, fetched_at) VALUES (1, 'acc-ada', 'Ada Lovelace', 1000), (1, 'acc-bob', 'Bob', 1000);

INSERT INTO issue (id, connection_id, source_id, key, space_id, summary, issue_type_id, status_id, assignee_account_id, sprint_ids, created_at, updated_at, hydrated_at, last_seen_at)
VALUES (1, 1, '9001', 'MOB-1', '10', 'First ticket', '1', '10', 'acc-ada', '["5"]', 1000, 3000, 3000, 3000),
       (2, 1, '9002', 'MOB-2', '10', 'Second ticket', '1', '11', 'acc-bob', '["5"]', 1000, 3000, 3000, 3000);
UPDATE issue SET removed_at = 4000 WHERE id = 2;

INSERT INTO status_change (issue_id, history_id, changed_at, from_status_id, to_status_id) VALUES
  (1, 'h1', 1500, '9', '10'), (1, 'h2', 2500, '10', '11'), (2, 'h3', 1600, '9', '10');
INSERT INTO assignee_change (issue_id, history_id, changed_at, from_account_id, to_account_id) VALUES
  (1, 'a1', 1200, NULL, 'acc-ada'), (1, 'a2', 2200, 'acc-ada', NULL);
INSERT INTO flag_change (issue_id, history_id, changed_at, flagged) VALUES (1, 'f1', 1800, 1), (1, 'f2', 1900, 0);
INSERT INTO comment (issue_id, comment_id, author_account_id, created_at, updated_at, body_adf, visibility_type, visibility_value, deleted_at) VALUES
  (1, 'c1', 'acc-bob', 1700, 1700, '{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Looks good"}]}]}', NULL, NULL, NULL),
  (1, 'c2', NULL, 1710, 1790, '{"type":"doc","version":1,"content":[]}', 'role', 'Developers', NULL),
  (2, 'c3', 'acc-ada', 1720, 1720, NULL, NULL, NULL, 2900);
INSERT INTO dev_commit (issue_id, commit_id, repo, authored_at, is_merge) VALUES (1, 'abc123', 'svc', 1750, 0), (1, 'def456', 'svc', 1760, 1);
INSERT INTO dev_pr_event (issue_id, pr_key, status, at, title, branch) VALUES
  (1, 'svc#12', 'OPEN', 1770, 'Versioned image URLs', 'feature/MOB-1-versioned-images'),
  (1, 'svc#12', 'MERGED', 1780, 'Versioned image URLs', 'feature/MOB-1-versioned-images');
UPDATE issue SET dev_fingerprint = 'fp-dev', dev_checked_at = 3500 WHERE id = 1;

INSERT INTO standup_issue (standup_id, issue_id, role, rank_index) VALUES (1, 1, 'board', 0), (1, 2, 'backlog', 0);
INSERT INTO standup_issue_span (standup_id, issue_id, role, valid_from, valid_to) VALUES
  (1, 1, 'board', 1000, 2000), (1, 1, 'board', 3000, NULL), (1, 2, 'backlog', 1000, NULL);
INSERT INTO standup_config_history (standup_id, valid_from, valid_to, fingerprint, config_json) VALUES
  (1, 1000, 2000, 'fp-a', '{"v":1}'), (1, 2000, NULL, 'fp-b', '{"v":2}');
INSERT INTO standup_sync_state (standup_id, needs_sync, last_attempt_at, last_success_at, history_from) VALUES (1, 0, 3000, 3000, 1000);

INSERT INTO sync_run (id, standup_id, mode, trigger, status, started_at, finished_at, issues_scanned) VALUES (1, 1, 'full', 'manual', 'ok', 1000, 1100, 2);
INSERT INTO sync_log (run_id, at, level, phase, message) VALUES (1, 1050, 'info', 'scan', 'scanned 2 issues');
INSERT INTO sink (id, kind, name, standup_id, config, created_at, updated_at) VALUES (1, 'file', 'Nightly JSON', 1, '{"dir":"/tmp/x"}', 1000, 1000);
INSERT INTO sink_outbox (id, sink_id, standup_id, cause, payload_kind, as_of, idempotency_key, payload, status, created_at, updated_at, delivered_at)
VALUES (1, 1, 1, 'manual', 'current', 3000, 'key-1', '{}', 'delivered', 3000, 3100, 3100);
-- v2 additions (0002_lane_order_removed_speakers)
UPDATE standup_column_map SET lane_order = 0 WHERE standup_id = 1;
INSERT INTO standup_speaker_removed (standup_id, account_id, removed_at) VALUES (1, 'acc-gone', 1000);
-- v3 additions (0003_ticket_content)
INSERT INTO issue_content (issue_id, description_adf, environment_adf, watchers, content_hash, updated_at) VALUES
  (1, '{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Do the thing"}]}]}', NULL, 3, 'hash-1', 3000);
INSERT INTO field_def (connection_id, field_id, name, kind, custom, custom_type, schema_json, fetched_at) VALUES
  (1, 'priority', 'Priority', 'option', 0, NULL, '{"type":"priority"}', 1000),
  (1, 'customfield_10042', 'Platforms', 'options', 1, 'com.atlassian.jira.plugin.system.customfieldtypes:multiselect', '{}', 1000);
INSERT INTO issue_field_value (issue_id, field_id, kind, value_json) VALUES
  (1, 'priority', 'option', '{"text":"High"}'), (1, 'customfield_10042', 'options', '{"items":["iOS","Android"]}');
INSERT INTO issue_link (issue_id, link_id, relation, label, position, other_source_id, other_key, other_summary, other_status_id, other_type_id, other_assignee_account_id) VALUES
  (1, 'l1', 'link', 'is blocked by', 0, '9002', 'MOB-2', 'Second ticket', '11', '1', 'acc-bob'),
  (1, 'sub:9100', 'subtask', '', 0, '9100', 'MOB-3', 'Not synced subtask', NULL, NULL, NULL);
INSERT INTO attachment (id, issue_id, source_id, media_file_id, name, mime, bytes, author_account_id, created_at, is_image, width, height, thumb_path, file_path, cached_bytes, last_viewed_at) VALUES
  (1, 1, 'att-1', 'media-uuid-1', 'share-preview.png', 'image/png', 83018, 'acc-ada', 2000, 1, 1200, 720, '1/1/1.thumb.jpg', '1/1/1.orig', 83018, 2500),
  (2, 1, 'att-2', NULL, 'notes.pdf', 'application/pdf', 1000, 'acc-bob', 2100, 0, NULL, NULL, NULL, NULL, 0, NULL);
INSERT INTO transition_cache (issue_id, json, fetched_at) VALUES (1, '[{"ID":"31","Name":"In Review"}]', 3000);
INSERT INTO ticket_draft (issue_id, body_text, mentions_json, updated_at) VALUES (1, 'Hi @Bob', '[{"start":3,"end":7,"accountId":"acc-bob","name":"Bob"}]', 3000);
INSERT INTO ticket_field_pref (connection_id, field_id, position, hidden) VALUES (1, 'customfield_10042', 0, 0), (1, 'priority', NULL, 1);
