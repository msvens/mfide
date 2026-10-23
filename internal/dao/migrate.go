package dao

import (
	"fmt"

	"github.com/jmoiron/sqlx"
)

// migration is one schema step. up applies only the schema/data change; upgrade
// stamps the version in the same transaction, so a step lands whole or not at all.
type migration struct {
	desc string
	up   func(tx *sqlx.Tx) error
}

// migrations is the complete schema history: version N is migrations[N-1], and a
// fresh database walks the whole list (there is no separate "latest schema").
// Append only. Never edit, reorder or delete a step, or databases already past it
// diverge from fresh ones.
var migrations = []migration{
	sqlMigration("api_key", schemaApiKey),
	sqlMigration("source_file", schemaSourceFile),
	sqlMigration("player, import_run", schemaPlayer),
	sqlMigration("player search and top-list indexes", schemaPlayerSearch),
	sqlMigration("per-country top-list indexes", schemaPlayerTopCountry),
}

// sqlMigration is a step that only runs SQL.
func sqlMigration(desc, stmt string) migration {
	return migration{desc: desc, up: func(tx *sqlx.Tx) error {
		_, err := tx.Exec(stmt)
		return err
	}}
}

// DbVersion is the schema version this binary requires.
var DbVersion = len(migrations)

// schemaVersionTable bootstraps the single-row version marker. It lives outside
// the migration list because the walk needs it before step 1.
const schemaVersionTable = `
CREATE TABLE IF NOT EXISTS schema_version (
	id         BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
	version    INT NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO schema_version (id, version) VALUES (TRUE, 0) ON CONFLICT (id) DO NOTHING;
`

// Upgrade brings the database up to DbVersion and reports the versions it moved
// between. On an empty database it creates the whole schema.
func (p *PGDB) Upgrade() (from, to int, err error) {
	return upgrade(p.db, migrations)
}

// Version reads the database's schema version.
func (p *PGDB) Version() (int, error) {
	return readVersion(p.db)
}

// Check verifies the database schema matches DbVersion without changing it. The
// server calls it at startup to refuse a mismatched schema up front instead of
// failing later on a missing table or column.
func (p *PGDB) Check() error {
	return check(p.db, DbVersion)
}

func upgrade(db *sqlx.DB, steps []migration) (from, to int, err error) {
	if _, err = db.Exec(schemaVersionTable); err != nil {
		return 0, 0, fmt.Errorf("create schema_version: %w", err)
	}
	if from, err = readVersion(db); err != nil {
		return 0, 0, err
	}
	if from > len(steps) {
		return from, from, fmt.Errorf("database schema is at version %d, newer than this binary (%d); deploy the matching build", from, len(steps))
	}
	for to = from; to < len(steps); to++ {
		if err = applyStep(db, to+1, steps[to]); err != nil {
			return from, to, err
		}
	}
	return from, to, nil
}

func applyStep(db *sqlx.DB, target int, m migration) error {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Locking the version row serializes concurrent upgrades (e.g. cron and a
	// manual run); whoever loses sees the step already applied and skips it.
	var cur int
	if err := tx.Get(&cur, "SELECT version FROM schema_version FOR UPDATE"); err != nil {
		return fmt.Errorf("lock schema_version: %w", err)
	}
	if cur >= target {
		return nil
	}
	if err := m.up(tx); err != nil {
		return fmt.Errorf("migration %d (%s): %w", target, m.desc, err)
	}
	if _, err := tx.Exec("UPDATE schema_version SET version = $1, updated_at = now()", target); err != nil {
		return fmt.Errorf("stamp version %d: %w", target, err)
	}
	return tx.Commit()
}

func readVersion(db sqlx.Queryer) (int, error) {
	var v int
	if err := sqlx.Get(db, &v, "SELECT version FROM schema_version"); err != nil {
		return 0, fmt.Errorf("could not read database version (is the database initialized? run `db upgrade`): %w", err)
	}
	return v, nil
}

func check(db sqlx.Queryer, want int) error {
	v, err := readVersion(db)
	if err != nil {
		return err
	}
	switch {
	case v == want:
		return nil
	case v < want:
		return fmt.Errorf("database schema is at version %d but this binary requires %d; run `db upgrade` before starting the server", v, want)
	default:
		return fmt.Errorf("database schema is at version %d, newer than this binary (%d); deploy the matching build", v, want)
	}
}
