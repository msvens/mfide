package dao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// schemaPlayer holds the current combined list, one row per player, with all
// three current ratings as columns (rating 0 = unrated, as FIDE sends it).
// Players missing from a later list are marked removed_at, not deleted.
const schemaPlayer = `
CREATE TABLE player (
	fideid       INT PRIMARY KEY,
	name         TEXT NOT NULL,
	country      TEXT NOT NULL,
	sex          TEXT NOT NULL,
	title        TEXT NOT NULL,
	w_title      TEXT NOT NULL,
	o_title      TEXT NOT NULL,
	foa_title    TEXT NOT NULL,
	birth_year   SMALLINT,
	flag         TEXT NOT NULL,
	std_rating   SMALLINT NOT NULL,
	std_games    SMALLINT NOT NULL,
	std_k        SMALLINT NOT NULL,
	rapid_rating SMALLINT NOT NULL,
	rapid_games  SMALLINT NOT NULL,
	rapid_k      SMALLINT NOT NULL,
	blitz_rating SMALLINT NOT NULL,
	blitz_games  SMALLINT NOT NULL,
	blitz_k      SMALLINT NOT NULL,
	updated_at   TIMESTAMPTZ NOT NULL,
	removed_at   TIMESTAMPTZ
);
CREATE TABLE import_run (
	id             SERIAL PRIMARY KEY,
	source_file_id INT NOT NULL REFERENCES source_file(id),
	started_at     TIMESTAMPTZ NOT NULL,
	finished_at    TIMESTAMPTZ NOT NULL,
	players        INT NOT NULL,
	inserted       INT NOT NULL,
	updated        INT NOT NULL,
	removed        INT NOT NULL,
	changes        JSONB NOT NULL
);
`

type Player struct {
	FideID      int        `db:"fideid"`
	Name        string     `db:"name"`
	Country     string     `db:"country"`
	Sex         string     `db:"sex"`
	Title       string     `db:"title"`
	WTitle      string     `db:"w_title"`
	OTitle      string     `db:"o_title"`
	FoaTitle    string     `db:"foa_title"`
	BirthYear   *int       `db:"birth_year"`
	Flag        string     `db:"flag"`
	StdRating   int        `db:"std_rating"`
	StdGames    int        `db:"std_games"`
	StdK        int        `db:"std_k"`
	RapidRating int        `db:"rapid_rating"`
	RapidGames  int        `db:"rapid_games"`
	RapidK      int        `db:"rapid_k"`
	BlitzRating int        `db:"blitz_rating"`
	BlitzGames  int        `db:"blitz_games"`
	BlitzK      int        `db:"blitz_k"`
	UpdatedAt   time.Time  `db:"updated_at"`
	RemovedAt   *time.Time `db:"removed_at"`
}

// values returns the data columns (playerCols order) for COPY.
func (p *Player) values() []any {
	return []any{p.FideID, p.Name, p.Country, p.Sex, p.Title, p.WTitle, p.OTitle, p.FoaTitle,
		p.BirthYear, p.Flag, p.StdRating, p.StdGames, p.StdK, p.RapidRating, p.RapidGames, p.RapidK,
		p.BlitzRating, p.BlitzGames, p.BlitzK}
}

// The data columns, i.e. everything FIDE sends, grouped the way the import
// reports changes.
var (
	profileCols = []string{"name", "country", "sex", "title", "w_title", "o_title", "foa_title", "birth_year"}
	ratingCols  = []string{"std_rating", "std_games", "std_k", "rapid_rating", "rapid_games", "rapid_k",
		"blitz_rating", "blitz_games", "blitz_k"}
	playerCols = append(append(append([]string{"fideid"}, profileCols...), "flag"), ratingCols...)
	dataCols   = playerCols[1:] // without the key
)

// PlayerSource yields player rows, then io.EOF. fide.PlayerReader is one.
type PlayerSource interface {
	NextRow() (*Player, error)
}

// ImportResult is what one import of the current list did.
type ImportResult struct {
	Players  int // rows in the file
	Inserted int
	Updated  int // includes players who came back after being removed
	Removed  int
	// Changes counts matched players per kind of change (a player can count in
	// several): profile (name, country, sex, titles, birth year), rating (any
	// rating, games or K), flag (active/inactive), returned (was removed).
	Changes map[string]int
}

// minKeepRatio guards against a truncated or broken file: an import that would
// leave fewer than this share of the current players is refused.
const minKeepRatio = 0.9

// ImportCurrent replaces the player table's contents with src, in one
// transaction: rows stream from src straight into COPY, so the list is never held
// in memory. New players are inserted, changed ones updated, missing ones marked
// removed, and an import_run row records it all.
func (pg *PGDB) ImportCurrent(sourceFileID int, src PlayerSource) (*ImportResult, error) {
	ctx := context.Background()
	started := time.Now()
	conn, err := pg.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	var res *ImportResult
	err = conn.Raw(func(driverConn any) error {
		c := driverConn.(*stdlib.Conn).Conn()
		tx, err := c.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if res, err = importCurrent(ctx, tx, src); err != nil {
			return err
		}
		changes, _ := json.Marshal(res.Changes)
		if _, err := tx.Exec(ctx, `INSERT INTO import_run
			(source_file_id, started_at, finished_at, players, inserted, updated, removed, changes)
			VALUES ($1, $2, clock_timestamp(), $3, $4, $5, $6, $7)`, // now() is the transaction start
			sourceFileID, started, res.Players, res.Inserted, res.Updated, res.Removed, changes); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func importCurrent(ctx context.Context, tx pgx.Tx, src PlayerSource) (*ImportResult, error) {
	stageCols := strings.Join(playerCols, ", ")
	if _, err := tx.Exec(ctx, "CREATE TEMP TABLE player_stage ON COMMIT DROP AS SELECT "+stageCols+" FROM player WITH NO DATA"); err != nil {
		return nil, err
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"player_stage"}, playerCols, pgx.CopyFromFunc(func() ([]any, error) {
		p, err := src.NextRow()
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return p.values(), nil
	}))
	if err != nil {
		return nil, fmt.Errorf("stage players: %w", err)
	}
	// The key both catches duplicate ids in the file and speeds up the joins.
	if _, err := tx.Exec(ctx, "ALTER TABLE player_stage ADD PRIMARY KEY (fideid)"); err != nil {
		return nil, fmt.Errorf("index staged players (duplicate fideid in the file?): %w", err)
	}

	var active int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM player WHERE removed_at IS NULL").Scan(&active); err != nil {
		return nil, err
	}
	if active > 0 && float64(n) < minKeepRatio*float64(active) {
		return nil, fmt.Errorf("file has %d players but %d are current; refusing an import that would remove more than %.0f%% (truncated file?)",
			n, active, (1-minKeepRatio)*100)
	}

	res := &ImportResult{Players: int(n), Changes: map[string]int{}}
	var profile, rating, flag, returned int
	err = tx.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE `+distinct(profileCols)+`),
		count(*) FILTER (WHERE `+distinct(ratingCols)+`),
		count(*) FILTER (WHERE p.flag IS DISTINCT FROM s.flag),
		count(*) FILTER (WHERE p.removed_at IS NOT NULL)
		FROM player p JOIN player_stage s USING (fideid)`).Scan(&profile, &rating, &flag, &returned)
	if err != nil {
		return nil, fmt.Errorf("count changes: %w", err)
	}
	res.Changes = map[string]int{"profile": profile, "rating": rating, "flag": flag, "returned": returned}

	tag, err := tx.Exec(ctx, `INSERT INTO player (`+stageCols+`, updated_at)
		SELECT `+prefixed("s", playerCols)+`, now() FROM player_stage s
		WHERE NOT EXISTS (SELECT 1 FROM player p WHERE p.fideid = s.fideid)`)
	if err != nil {
		return nil, fmt.Errorf("insert new players: %w", err)
	}
	res.Inserted = int(tag.RowsAffected())

	tag, err = tx.Exec(ctx, `UPDATE player p SET (`+strings.Join(dataCols, ", ")+`, updated_at, removed_at)
		= (`+prefixed("s", dataCols)+`, now(), NULL)
		FROM player_stage s
		WHERE p.fideid = s.fideid AND (`+distinct(dataCols)+` OR p.removed_at IS NOT NULL)`)
	if err != nil {
		return nil, fmt.Errorf("update changed players: %w", err)
	}
	res.Updated = int(tag.RowsAffected())

	tag, err = tx.Exec(ctx, `UPDATE player p SET removed_at = now()
		WHERE removed_at IS NULL AND NOT EXISTS (SELECT 1 FROM player_stage s WHERE s.fideid = p.fideid)`)
	if err != nil {
		return nil, fmt.Errorf("mark removed players: %w", err)
	}
	res.Removed = int(tag.RowsAffected())
	return res, nil
}

// distinct is "(p.a, p.b) IS DISTINCT FROM (s.a, s.b)".
func distinct(cols []string) string {
	return "(" + prefixed("p", cols) + ") IS DISTINCT FROM (" + prefixed("s", cols) + ")"
}

func prefixed(alias string, cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = alias + "." + c
	}
	return strings.Join(out, ", ")
}

type ImportRun struct {
	Id           int       `db:"id"`
	SourceFileId int       `db:"source_file_id"`
	StartedAt    time.Time `db:"started_at"`
	FinishedAt   time.Time `db:"finished_at"`
	Players      int       `db:"players"`
	Inserted     int       `db:"inserted"`
	Updated      int       `db:"updated"`
	Removed      int       `db:"removed"`
	Changes      string    `db:"changes"`
}

type ImportRunDAO interface {
	// Imported reports whether a source file has been imported.
	Imported(sourceFileID int) (bool, error)
	List() ([]ImportRun, error)
}

type ImportRunPG struct {
	db *sqlx.DB
}

func NewImportRunPG(db *sqlx.DB) *ImportRunPG {
	return &ImportRunPG{db: db}
}

func (dao *ImportRunPG) Imported(sourceFileID int) (bool, error) {
	var ok bool
	err := dao.db.Get(&ok, "SELECT EXISTS (SELECT 1 FROM import_run WHERE source_file_id = $1)", sourceFileID)
	return ok, err
}

func (dao *ImportRunPG) List() ([]ImportRun, error) {
	runs := []ImportRun{}
	err := dao.db.Select(&runs, "SELECT id, source_file_id, started_at, finished_at, players, inserted, updated, removed, changes::text AS changes FROM import_run ORDER BY id")
	return runs, err
}
