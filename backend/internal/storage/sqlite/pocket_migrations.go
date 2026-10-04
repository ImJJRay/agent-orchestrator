package sqlite

import (
	"database/sql"
	"fmt"
	"io/fs"
	"strings"

	"github.com/pressly/goose/v3"
)

const pocketMigrationTable = "pocket_goose_db_version"

// migrationTrackFS separates migration discovery without moving or rewriting
// released SQL files. Upstream and Pocket can retain the same version numbers.
type migrationTrackFS struct{ pocket bool }

func (f migrationTrackFS) accepts(name string) bool {
	return !strings.HasSuffix(name, ".sql") || strings.Contains(name, "_pocket_") == f.pocket
}

func (f migrationTrackFS) Open(name string) (fs.File, error) {
	if !f.accepts(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return embeddedMigrationsFS.Open(name)
}

func (f migrationTrackFS) ReadFile(name string) ([]byte, error) {
	if !f.accepts(name) {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	return embeddedMigrationsFS.ReadFile(name)
}

func (f migrationTrackFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := embeddedMigrationsFS.ReadDir(name)
	if err != nil {
		return nil, err
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if f.accepts(entry.Name()) {
			out = append(out, entry)
		}
	}
	return out, nil
}

// Called only while gooseMu is held. Always restore the upstream globals,
// including on errors, so the next startup/test cannot use the wrong ledger.
func migratePocket(db *sql.DB) error {
	goose.SetBaseFS(pocketMigrationsFS)
	goose.SetTableName(pocketMigrationTable)
	defer goose.SetBaseFS(migrationsFS)
	defer goose.SetTableName("goose_db_version")
	return goose.Up(db, "migrations", goose.WithAllowMissing())
}

func pocketMigrationVersion() (int64, error) {
	entries, err := pocketMigrationsFS.ReadDir("migrations")
	if err != nil {
		return 0, err
	}
	var latest int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		version, err := goose.NumericComponent(entry.Name())
		if err != nil {
			return 0, err
		}
		if version > latest {
			latest = version
		}
	}
	return latest, nil
}

func verifyPocketMigrationVersion(db *sql.DB) error {
	want, err := pocketMigrationVersion()
	if err != nil {
		return err
	}
	var got int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(version_id),0) FROM pocket_goose_db_version WHERE is_applied=1`).Scan(&got); err != nil {
		return fmt.Errorf("read Pocket migration version: %w", err)
	}
	if got != want {
		return fmt.Errorf("pocket schema version mismatch: database has %d, binary expects %d", got, want)
	}
	return nil
}

func hasPocketMigrationLedger(db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='pocket_goose_db_version'`).Scan(&count)
	return count == 1, err
}

// separatePocketMigrationHistory adopts complete, physically present Pocket
// schemas into their own ledger. The transaction changes metadata only; task,
// attempt, validation and immutable audit rows are never rewritten. Shared
// markers are removed only when the colliding upstream schema is absent.
func separatePocketMigrationHistory(db *sql.DB) error {
	if exists, err := hasPocketMigrationLedger(db); err != nil || exists {
		return err
	}
	var base, lifecycle, command, policy int
	for _, check := range []struct {
		query string
		value *int
	}{
		{`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('pocket_tasks','pocket_workers','pocket_executions','pocket_validation_requirements','pocket_validation_results')`, &base},
		{`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='pocket_lifecycle_state'`, &lifecycle},
		{`SELECT COUNT(*) FROM pragma_table_info('pocket_validation_requirements') WHERE name='command'`, &command},
		{`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('pocket_dependencies','pocket_policy_configs','pocket_decisions','pocket_actions','pocket_action_events')`, &policy},
	} {
		if err := db.QueryRow(check.query).Scan(check.value); err != nil {
			return err
		}
	}
	if base == 0 && lifecycle == 0 && command == 0 && policy == 0 {
		return nil
	}
	if base != 5 || lifecycle != command || (policy != 0 && policy != 5) || (policy == 5 && lifecycle != 1) {
		return fmt.Errorf("incomplete Pocket schema: refusing migration-history adoption")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`CREATE TABLE pocket_goose_db_version (id INTEGER PRIMARY KEY AUTOINCREMENT, version_id INTEGER NOT NULL, is_applied BOOLEAN NOT NULL, tstamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP); INSERT INTO pocket_goose_db_version(version_id,is_applied) VALUES (0,1)`); err != nil {
		return err
	}
	for _, step := range []struct {
		version       int64
		present       bool
		upstreamProbe string
	}{
		{169, true, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='report_outputs_pr_created_cdc'`},
		{170, lifecycle == 1, `SELECT COUNT(*) FROM pragma_table_info('notifications') WHERE name='source_key'`},
		{171, policy == 5, `SELECT COUNT(*) FROM pragma_table_info('shell_terminals') WHERE name='preview_capability_verifier'`},
	} {
		if !step.present {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO pocket_goose_db_version(version_id,is_applied) VALUES (?,1)`, step.version); err != nil {
			return err
		}
		var upstreamPresent int
		if err := tx.QueryRow(step.upstreamProbe).Scan(&upstreamPresent); err != nil {
			return err
		}
		if upstreamPresent == 0 {
			if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id=?`, step.version); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
