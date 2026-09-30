-- +goose Up
-- +goose StatementBegin
-- Pocket policy state extends AO's durable control plane without replacing AO
-- session/conversation/worktree ownership. Cross-AO identifiers are retained as
-- historical snapshots rather than foreign keys so normal AO cleanup cannot
-- destroy execution evidence.
CREATE TABLE pocket_tasks (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL DEFAULT '',
    objective   TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT 'active'
        CHECK (state IN ('pending', 'active', 'completed', 'failed', 'cancelled', 'blocked')),
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);

CREATE TABLE pocket_workers (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMP NOT NULL
);

CREATE TABLE pocket_executions (
    id                  TEXT PRIMARY KEY,
    task_id             TEXT NOT NULL REFERENCES pocket_tasks(id) ON DELETE RESTRICT,
    worker_id           TEXT NOT NULL REFERENCES pocket_workers(id) ON DELETE RESTRICT,
    attempt_number      INTEGER NOT NULL CHECK (attempt_number >= 1),
    prior_execution_id  TEXT REFERENCES pocket_executions(id) ON DELETE RESTRICT,
    session_id          TEXT NOT NULL,
    conversation_id     TEXT NOT NULL DEFAULT '',
    turn_id             TEXT NOT NULL DEFAULT '',
    project_id          TEXT NOT NULL DEFAULT '',
    workspace_path      TEXT NOT NULL DEFAULT '',
    workspace_repo_path TEXT NOT NULL DEFAULT '',
    state               TEXT NOT NULL DEFAULT 'unknown'
        CHECK (state IN ('unknown', 'queued', 'running', 'completed', 'failed', 'interrupted')),
    started_at          TIMESTAMP,
    completed_at        TIMESTAMP,
    created_at          TIMESTAMP NOT NULL,
    updated_at          TIMESTAMP NOT NULL,
    CHECK ((conversation_id = '' AND turn_id = '') OR (conversation_id <> '' AND turn_id <> '')),
    CHECK (prior_execution_id IS NULL OR prior_execution_id <> id)
);

CREATE UNIQUE INDEX idx_pocket_executions_task_root
    ON pocket_executions(task_id)
    WHERE prior_execution_id IS NULL;
CREATE UNIQUE INDEX idx_pocket_executions_retry_source
    ON pocket_executions(prior_execution_id)
    WHERE prior_execution_id IS NOT NULL;
CREATE UNIQUE INDEX idx_pocket_executions_session_turn
    ON pocket_executions(session_id, turn_id)
    WHERE turn_id <> '';
CREATE INDEX idx_pocket_executions_session_created
    ON pocket_executions(session_id, created_at DESC);

CREATE TABLE pocket_validation_requirements (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES pocket_tasks(id) ON DELETE RESTRICT,
    execution_id    TEXT REFERENCES pocket_executions(id) ON DELETE RESTRICT,
    scope           TEXT NOT NULL CHECK (scope IN ('task', 'execution')),
    check_id        TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    deterministic   BOOLEAN NOT NULL DEFAULT TRUE,
    required        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMP NOT NULL,
    CHECK ((scope = 'task' AND execution_id IS NULL)
        OR (scope = 'execution' AND execution_id IS NOT NULL))
);

CREATE UNIQUE INDEX idx_pocket_validation_requirements_task_check
    ON pocket_validation_requirements(task_id, check_id)
    WHERE scope = 'task';
CREATE UNIQUE INDEX idx_pocket_validation_requirements_execution_check
    ON pocket_validation_requirements(execution_id, check_id)
    WHERE scope = 'execution';

CREATE TABLE pocket_validation_results (
    id              TEXT PRIMARY KEY,
    requirement_id  TEXT NOT NULL REFERENCES pocket_validation_requirements(id) ON DELETE RESTRICT,
    task_id         TEXT NOT NULL REFERENCES pocket_tasks(id) ON DELETE RESTRICT,
    execution_id    TEXT NOT NULL REFERENCES pocket_executions(id) ON DELETE RESTRICT,
    state           TEXT NOT NULL CHECK (state IN ('pass', 'fail', 'unknown')),
    source_kind     TEXT NOT NULL CHECK (source_kind IN ('deterministic', 'semantic', 'external')),
    source          TEXT NOT NULL,
    detail          TEXT NOT NULL DEFAULT '',
    observed_at     TIMESTAMP NOT NULL,
    created_at      TIMESTAMP NOT NULL
);

CREATE INDEX idx_pocket_validation_results_execution
    ON pocket_validation_results(execution_id, observed_at DESC, created_at DESC);
CREATE INDEX idx_pocket_validation_results_requirement
    ON pocket_validation_results(requirement_id, observed_at DESC, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE pocket_validation_results;
DROP TABLE pocket_validation_requirements;
DROP TABLE pocket_executions;
DROP TABLE pocket_workers;
DROP TABLE pocket_tasks;
-- +goose StatementEnd
