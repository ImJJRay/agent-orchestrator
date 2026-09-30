-- +goose Up
-- +goose StatementBegin
CREATE TABLE pocket_dependencies (
    task_id TEXT NOT NULL REFERENCES pocket_tasks(id) ON DELETE RESTRICT,
    dependency_id TEXT NOT NULL REFERENCES pocket_tasks(id) ON DELETE RESTRICT,
    PRIMARY KEY (task_id, dependency_id),
    CHECK (task_id <> dependency_id)
);
CREATE TABLE pocket_policy_configs (
    task_id TEXT PRIMARY KEY REFERENCES pocket_tasks(id) ON DELETE RESTRICT,
    config_json TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE pocket_decisions (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES pocket_tasks(id) ON DELETE RESTRICT,
    fingerprint TEXT NOT NULL,
    facts_json TEXT NOT NULL,
    outcome_json TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    UNIQUE (task_id, fingerprint)
);
CREATE TABLE pocket_actions (
    decision_id TEXT PRIMARY KEY REFERENCES pocket_decisions(id) ON DELETE RESTRICT,
    execution_id TEXT NOT NULL UNIQUE REFERENCES pocket_executions(id) ON DELETE RESTRICT,
    task_id TEXT NOT NULL REFERENCES pocket_tasks(id) ON DELETE RESTRICT,
    session_id TEXT NOT NULL,
    prior_turn_id TEXT NOT NULL DEFAULT '',
    native_retry BOOLEAN NOT NULL DEFAULT FALSE,
    prompt TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('READY','RETRY','ESCALATE')),
    status TEXT NOT NULL CHECK (status IN ('prepared','dispatching','dispatched','uncertain','superseded')),
    error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE pocket_action_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    decision_id TEXT NOT NULL REFERENCES pocket_decisions(id) ON DELETE RESTRICT,
    status TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TRIGGER pocket_action_events_no_update BEFORE UPDATE ON pocket_action_events BEGIN SELECT RAISE(ABORT, 'Pocket action events are immutable'); END;
CREATE TRIGGER pocket_action_events_no_delete BEFORE DELETE ON pocket_action_events BEGIN SELECT RAISE(ABORT, 'Pocket action events are immutable'); END;
CREATE TRIGGER pocket_actions_audit_insert AFTER INSERT ON pocket_actions BEGIN INSERT INTO pocket_action_events(decision_id,status,detail) VALUES (NEW.decision_id,NEW.status,NEW.error); END;
CREATE TRIGGER pocket_actions_audit_update AFTER UPDATE ON pocket_actions WHEN OLD.status<>NEW.status OR OLD.error<>NEW.error BEGIN INSERT INTO pocket_action_events(decision_id,status,detail) VALUES (NEW.decision_id,NEW.status,NEW.error); END;
CREATE TRIGGER pocket_decisions_no_update BEFORE UPDATE ON pocket_decisions BEGIN SELECT RAISE(ABORT, 'Pocket decisions are immutable'); END;
CREATE TRIGGER pocket_decisions_no_delete BEFORE DELETE ON pocket_decisions BEGIN SELECT RAISE(ABORT, 'Pocket decisions are immutable'); END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER pocket_decisions_no_delete;
DROP TRIGGER pocket_decisions_no_update;
DROP TRIGGER pocket_actions_audit_update;
DROP TRIGGER pocket_actions_audit_insert;
DROP TRIGGER pocket_action_events_no_delete;
DROP TRIGGER pocket_action_events_no_update;
DROP TABLE pocket_action_events;
DROP TABLE pocket_actions;
DROP TABLE pocket_decisions;
DROP TABLE pocket_policy_configs;
DROP TABLE pocket_dependencies;
-- +goose StatementEnd
