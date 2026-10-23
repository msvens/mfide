// Package dao is the Postgres access layer. The schema is not designed yet —
// see docs/findings.md ("Open design questions") before adding tables.
package dao

import (
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/msvens/mfide/internal/config"
)

type PGDB struct {
	db         *sqlx.DB
	ApiKey     ApiKeyDAO
	SourceFile SourceFileDAO
	ImportRun  ImportRunDAO
	Player     PlayerDAO
}

// NewPGDB opens and pings the configured database.
func NewPGDB() (*PGDB, error) {
	// pgx/v5 treats "password=" (empty value) as consuming the next key/value pair,
	// so it is left out of the DSN when no password is configured.
	//
	// plan_cache_mode: pgx reuses prepared statements per connection, and after
	// five executions Postgres may switch to a generic plan that ignores the
	// parameters. For top lists the best plan depends on them (a small federation
	// plus women must start from player, the world list from rating): the generic
	// plan made NOR women go from 5 ms to 890 ms. Planning these queries is cheap.
	dataSource := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=disable plan_cache_mode=force_custom_plan",
		config.DbHost(), config.DbPort(), config.DbUser(), config.DbName())
	if pw := config.DbPassword(); pw != "" {
		dataSource += " password=" + pw
	}
	db, err := sqlx.Open("pgx", dataSource)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &PGDB{db: db, ApiKey: NewApiKeyPG(db), SourceFile: NewSourceFilePG(db), ImportRun: NewImportRunPG(db), Player: NewPlayerPG(db)}, nil
}

func (p *PGDB) Ping() error {
	return p.db.Ping()
}

func (p *PGDB) Close() error {
	return p.db.Close()
}
