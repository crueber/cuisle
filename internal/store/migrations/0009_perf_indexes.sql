-- 0009_perf_indexes: additive indexes for ticket-modal field-in-use lookups and flag history ordering.
-- Forward-only; touches no record or config rows.

CREATE INDEX IF NOT EXISTS issue_space ON issue(connection_id, space_id);
CREATE INDEX IF NOT EXISTS issue_field_value_field ON issue_field_value(field_id, issue_id);
CREATE INDEX IF NOT EXISTS flag_change_time ON flag_change(issue_id, changed_at);
