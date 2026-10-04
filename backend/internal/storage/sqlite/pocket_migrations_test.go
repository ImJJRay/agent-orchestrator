package sqlite

import (
	"database/sql"
	"fmt"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

// Recreate the shipped single-ledger Pocket database, not a hand-renumbered
// approximation. All original SQL files remain byte-for-byte unchanged.
func seedSharedPocketHistory(t *testing.T, db *sql.DB, version int64) {
	t.Helper()
	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(pocketMigrationsFS)
	defer goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", version, goose.WithAllowMissing()); err != nil {
		t.Fatal(err)
	}
}

func TestPocketMigrationTrackUpgradesPreserveEvidence(t *testing.T) {
	for _, version := range []int64{169, 170, 171} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 168)
			seedSharedPocketHistory(t, db, version)
			mustExec(t, db, `INSERT INTO pocket_tasks(id,objective,created_at,updated_at) VALUES ('task','retained objective',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
			mustExec(t, db, `INSERT INTO pocket_workers(id,session_id,created_at) VALUES ('worker','session',CURRENT_TIMESTAMP)`)
			mustExec(t, db, `INSERT INTO pocket_executions(id,task_id,worker_id,attempt_number,session_id,created_at,updated_at) VALUES ('attempt','task','worker',1,'session',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
			if version == 171 {
				mustExec(t, db, `INSERT INTO pocket_decisions(id,task_id,fingerprint,facts_json,outcome_json,created_at) VALUES ('decision','task','fingerprint','{}','{}',CURRENT_TIMESTAMP)`)
				mustExec(t, db, `INSERT INTO pocket_actions(decision_id,execution_id,task_id,session_id,prompt,kind,status) VALUES ('decision','attempt','task','session','do work','READY','uncertain')`)
			}
			for range 3 {
				if err := migrate(db); err != nil {
					t.Fatal(err)
				}
			}
			assertPocketIntegratedSchema(t, db)
			if err := verifyPocketMigrationVersion(db); err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{
				`SELECT COUNT(*) FROM pocket_tasks WHERE id='task' AND objective='retained objective'`,
				`SELECT COUNT(*) FROM pocket_executions WHERE id='attempt' AND task_id='task'`,
				`SELECT COUNT(*) FROM sqlite_master WHERE name='report_outputs_pr_created_cdc'`,
				`SELECT COUNT(*) FROM pragma_table_info('notifications') WHERE name='source_key'`,
				`SELECT COUNT(*) FROM pragma_table_info('shell_terminals') WHERE name='preview_capability_verifier'`,
			} {
				var count int
				if err := db.QueryRow(query).Scan(&count); err != nil || count != 1 {
					t.Fatalf("%s: count=%d err=%v", query, count, err)
				}
			}
			if version == 171 {
				var status string
				if err := db.QueryRow(`SELECT status FROM pocket_actions WHERE decision_id='decision'`).Scan(&status); err != nil || status != "uncertain" {
					t.Fatalf("action: %s %v", status, err)
				}
				var count int
				if err := db.QueryRow(`SELECT COUNT(*) FROM pocket_action_events`).Scan(&count); err != nil || count != 1 {
					t.Fatalf("audit changed: %d %v", count, err)
				}
				if _, err := db.Exec(`UPDATE pocket_decisions SET fingerprint='changed'`); err == nil {
					t.Fatal("audit immutability lost")
				}
			}
		})
	}
}

func TestPocketMigrationTracksAcceptUpstreamAndRejectPartialPocket(t *testing.T) {
	t.Run("upstream174", func(t *testing.T) {
		db := openMigratedDatabaseCopy(t, 174)
		if err := migrate(db); err != nil {
			t.Fatal(err)
		}
		assertPocketIntegratedSchema(t, db)
		if err := verifyPocketMigrationVersion(db); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("incomplete", func(t *testing.T) {
		db := openMigratedDatabaseCopy(t, 168)
		mustExec(t, db, `CREATE TABLE pocket_tasks(id TEXT PRIMARY KEY)`)
		if err := migrate(db); err == nil {
			t.Fatal("partial Pocket schema accepted")
		}
		if exists, err := hasPocketMigrationLedger(db); err != nil || exists {
			t.Fatalf("partial adoption persisted: %v %v", exists, err)
		}
	})
}

func TestPocketMigrationTrackDiscovery(t *testing.T) {
	for _, track := range []migrationTrackFS{migrationsFS, pocketMigrationsFS} {
		files, err := fs.Glob(track, "migrations/*.sql")
		if err != nil {
			t.Fatal(err)
		}
		seen := map[int64]bool{}
		for _, file := range files {
			version, err := goose.NumericComponent(file)
			if err != nil {
				t.Fatal(err)
			}
			if seen[version] {
				t.Fatalf("duplicate version in track: %s", file)
			}
			seen[version] = true
		}
		if len(files) == 0 {
			t.Fatal("empty migration track")
		}
	}
}
