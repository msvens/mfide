package dao

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/msvens/mfide/internal/config"
)

// openTestDB connects to the database in config_test.yaml and wipes it to an
// empty public schema. It refuses any database whose name doesn't end in _test,
// so a misconfigured run can't wipe a real one.
func openTestDB(t *testing.T) *PGDB {
	t.Helper()
	if err := config.NewConfig("config_test"); err != nil {
		t.Fatalf("read config_test.yaml: %v", err)
	}
	if name := config.DbName(); !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to reset database %q: test database names must end in _test", name)
	}
	pg, err := NewPGDB()
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	if _, err := pg.db.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatalf("reset test database: %v", err)
	}
	return pg
}

var testSteps = []migration{
	sqlMigration("create t1", "CREATE TABLE t1 (id INT)"),
	sqlMigration("add t1.name", "ALTER TABLE t1 ADD COLUMN name TEXT"),
}

func tableExists(t *testing.T, db *sqlx.DB, table string) bool {
	t.Helper()
	var rel sql.NullString
	if err := db.Get(&rel, "SELECT to_regclass($1)", "public."+table); err != nil {
		t.Fatalf("to_regclass(%s): %v", table, err)
	}
	return rel.Valid
}

func TestUpgradeFreshAndRerun(t *testing.T) {
	pg := openTestDB(t)

	if _, err := readVersion(pg.db); err == nil {
		t.Fatal("expected an error reading the version of an uninitialized database")
	}

	from, to, err := upgrade(pg.db, testSteps)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if from != 0 || to != 2 {
		t.Errorf("upgrade moved %d -> %d, want 0 -> 2", from, to)
	}
	if _, err := pg.db.Exec("SELECT id, name FROM t1"); err != nil {
		t.Errorf("both steps should have applied: %v", err)
	}

	from, to, err = upgrade(pg.db, testSteps)
	if err != nil || from != 2 || to != 2 {
		t.Errorf("rerun = (%d, %d, %v), want a no-op at 2", from, to, err)
	}
}

func TestUpgradeFailedStepRollsBack(t *testing.T) {
	pg := openTestDB(t)

	broken := migration{desc: "broken", up: func(tx *sqlx.Tx) error {
		if _, err := tx.Exec("CREATE TABLE t2 (id INT)"); err != nil {
			return err
		}
		return errors.New("boom")
	}}
	steps := append(append([]migration{}, testSteps...), broken)

	if _, to, err := upgrade(pg.db, steps); err == nil {
		t.Fatal("expected the broken step to fail the upgrade")
	} else if to != 2 {
		t.Errorf("upgrade stopped at %d, want 2", to)
	}
	if tableExists(t, pg.db, "t2") {
		t.Error("the failed step's DDL should have been rolled back")
	}
	if v, err := readVersion(pg.db); err != nil || v != 2 {
		t.Errorf("version = (%d, %v), want 2", v, err)
	}
}

func TestUpgradeDatabaseAhead(t *testing.T) {
	pg := openTestDB(t)

	if _, _, err := upgrade(pg.db, testSteps); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if _, _, err := upgrade(pg.db, testSteps[:1]); err == nil {
		t.Error("expected an error when the database is newer than the binary")
	}
}

func TestCheck(t *testing.T) {
	pg := openTestDB(t)

	if err := check(pg.db, 0); err == nil {
		t.Error("expected an uninitialized database to fail the check")
	}
	if _, _, err := upgrade(pg.db, testSteps); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if err := check(pg.db, 2); err != nil {
		t.Errorf("equal versions: %v", err)
	}
	if err := check(pg.db, 3); err == nil || !strings.Contains(err.Error(), "db upgrade") {
		t.Errorf("behind: got %v, want a hint to run db upgrade", err)
	}
	if err := check(pg.db, 1); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("ahead: got %v, want a newer-than-binary error", err)
	}
}

// TestMigrations runs the real migration list on an empty database.
func TestMigrations(t *testing.T) {
	pg := openTestDB(t)

	if _, to, err := pg.Upgrade(); err != nil || to != DbVersion {
		t.Fatalf("Upgrade = (%d, %v), want %d", to, err, DbVersion)
	}
	if err := pg.Check(); err != nil {
		t.Errorf("Check after Upgrade: %v", err)
	}
}

// TestCustomPlans checks the connection setting that keeps parameterized top
// lists on custom plans (see NewPGDB).
func TestCustomPlans(t *testing.T) {
	pg := openTestDB(t)
	var mode string
	if err := pg.db.Get(&mode, "SHOW plan_cache_mode"); err != nil {
		t.Fatal(err)
	}
	if mode != "force_custom_plan" {
		t.Errorf("plan_cache_mode = %q, want force_custom_plan", mode)
	}
}
