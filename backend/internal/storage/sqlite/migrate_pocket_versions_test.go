package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

const legacyPocket0166SQL = `-- Pocket policy state extends AO's durable control plane without replacing AO
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
        CHECK (state IN ('unknown', 'queued', 'running', 'completed', 'recovered', 'failed', 'interrupted', 'cancelled')),
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
    ON pocket_validation_results(requirement_id, observed_at DESC, created_at DESC);`

const legacyPocket0167SQL = `-- Deterministic Pocket validation commands are task/execution policy facts.
-- The lifecycle watermark prevents upgrade-time reconciliation from turning
-- historical AO Chat turns into Pocket tasks unless a later retry explicitly
-- pulls one of those turns into a new attempt lineage.
ALTER TABLE pocket_validation_requirements
    ADD COLUMN command TEXT NOT NULL DEFAULT '';

CREATE TABLE pocket_lifecycle_state (
    singleton              INTEGER PRIMARY KEY CHECK (singleton = 1),
    automation_started_at  TIMESTAMP NOT NULL
);

INSERT INTO pocket_lifecycle_state (singleton, automation_started_at)
VALUES (1, CURRENT_TIMESTAMP);`

func TestPocketMigrationFreshDatabase(t *testing.T) {
	db := openMigratedTestDB(t)
	assertPocketIntegratedSchema(t, db)
}

func TestPocketMigrationRepairsLegacyPocket166167(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 165)
	seedLegacyPocket166167(t, db)

	if err := migrate(db); err != nil {
		t.Fatalf("migrate legacy Pocket database: %v", err)
	}
	assertPocketIntegratedSchema(t, db)
}

func TestPocketMigrationAppliesAfterUpstream168(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 168)

	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'pocket_tasks'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("pocket_tasks exists before Pocket migrations: %d", before)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate upstream-168 database: %v", err)
	}
	assertPocketIntegratedSchema(t, db)
}

func TestPocketMigrationRepeatStartupIsIdempotent(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 168)

	for pass := 1; pass <= 3; pass++ {
		if err := migrate(db); err != nil {
			t.Fatalf("migrate pass %d: %v", pass, err)
		}
	}
	assertPocketIntegratedSchema(t, db)
}

func seedLegacyPocket166167(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, legacyPocket0166SQL)
	mustExec(t, db, legacyPocket0167SQL)
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (166, 1)`)
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (167, 1)`)
}

func assertPocketIntegratedSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	for _, table := range []string{
		"cues",
		"pocket_tasks",
		"pocket_workers",
		"pocket_executions",
		"pocket_validation_requirements",
		"pocket_validation_results",
		"pocket_lifecycle_state",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %s count = %d, want 1", table, count)
		}
	}

	var commandColumn int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('pocket_validation_requirements') WHERE name = 'command'`).Scan(&commandColumn); err != nil {
		t.Fatal(err)
	}
	if commandColumn != 1 {
		t.Fatalf("pocket_validation_requirements.command count = %d, want 1", commandColumn)
	}

	var sessionsSQL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&sessionsSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sessionsSQL, "'deepseek-harness'") {
		t.Fatal("upstream DeepSeek Harness migration 0166 was skipped")
	}
	if !strings.Contains(sessionsSQL, "'opencode-v2'") {
		t.Fatal("upstream OpenCode 2 migration 0167 was skipped")
	}

	for _, version := range []int64{166, 167, 168, 169, 170} {
		var applied int
		if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied
    FROM goose_db_version
    WHERE version_id = ?
    ORDER BY id DESC
    LIMIT 1
), 0)`, version).Scan(&applied); err != nil {
			t.Fatal(err)
		}
		if applied != 1 {
			t.Fatalf("migration %d applied = %d, want 1", version, applied)
		}
	}
}
