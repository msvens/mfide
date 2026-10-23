package dao

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// schemaRating is the one place for rating numbers: one row per player and month,
// the three rating types as columns (NULL = not rated in that type that month).
// It's filled from FIDE's monthly per-type archive files; the current month is
// the latest period. Activity is per type in FIDE's data (a player can be
// inactive in standard but active in rapid), hence a flag per type.
const schemaRating = `
CREATE TABLE rating (
	fideid       INT      NOT NULL,
	period       DATE     NOT NULL,
	std_rating   SMALLINT,
	std_games    SMALLINT,
	std_k        SMALLINT,
	std_flag     TEXT,
	rapid_rating SMALLINT,
	rapid_games  SMALLINT,
	rapid_k      SMALLINT,
	rapid_flag   TEXT,
	blitz_rating SMALLINT,
	blitz_games  SMALLINT,
	blitz_k      SMALLINT,
	blitz_flag   TEXT,
	PRIMARY KEY (fideid, period)
);
`

// ArchiveRow is one player in a monthly per-type archive list.
type ArchiveRow struct {
	FideID int
	Rating int
	Games  int
	K      int
	Flag   string
}

// ArchiveSource yields archive rows, then io.EOF. fide.PlayerReader is one.
type ArchiveSource interface {
	NextArchiveRow() (*ArchiveRow, error)
}

// ImportArchive sets one rating type's columns for one month from src, in one
// transaction, and records the import. Only differences are written: players
// missing from the file lose that type for the month (rows left with no rating
// in any type are removed), new or changed ratings are upserted, and unchanged
// rows aren't touched, so re-importing a regenerated but unchanged file (the
// current month, nightly) writes nothing.
func (pg *PGDB) ImportArchive(sourceFileID int, ratingType string, period time.Time, src ArchiveSource) (*ImportResult, error) {
	p, ok := ratingPrefix[ratingType]
	if !ok {
		return nil, fmt.Errorf("unknown rating type %q", ratingType)
	}
	cols := []string{p + "_rating", p + "_games", p + "_k", p + "_flag"}
	colList := strings.Join(cols, ", ")
	started := time.Now()
	res := &ImportResult{Changes: map[string]int{}}
	err := pg.inPgxTx(func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE rating_stage
			(fideid INT, rating SMALLINT, games SMALLINT, k SMALLINT, flag TEXT) ON COMMIT DROP`); err != nil {
			return err
		}
		n, err := tx.CopyFrom(ctx, pgx.Identifier{"rating_stage"}, []string{"fideid", "rating", "games", "k", "flag"},
			pgx.CopyFromFunc(func() ([]any, error) {
				r, err := src.NextArchiveRow()
				if errors.Is(err, io.EOF) {
					return nil, nil
				}
				if err != nil {
					return nil, err
				}
				return []any{r.FideID, r.Rating, r.Games, r.K, r.Flag}, nil
			}))
		if err != nil {
			return fmt.Errorf("copy %s %s: %w", ratingType, period.Format("2006-01"), err)
		}
		if n == 0 {
			return fmt.Errorf("%s %s: the file has no players", ratingType, period.Format("2006-01"))
		}
		// The key catches duplicate ids in the file and speeds up the joins below.
		if _, err := tx.Exec(ctx, "ALTER TABLE rating_stage ADD PRIMARY KEY (fideid)"); err != nil {
			return fmt.Errorf("index staged %s %s (duplicate fideid in the file?): %w", ratingType, period.Format("2006-01"), err)
		}

		tag, err := tx.Exec(ctx, `UPDATE rating r SET (`+colList+`) = (NULL, NULL, NULL, NULL)
			WHERE r.period = $1 AND r.`+p+`_rating IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM rating_stage s WHERE s.fideid = r.fideid)`, period)
		if err != nil {
			return err
		}
		res.Removed = int(tag.RowsAffected())

		set := make([]string, len(cols))
		old := make([]string, len(cols))
		for i, c := range cols {
			set[i] = c + " = EXCLUDED." + c
			old[i] = "rating." + c
		}
		tag, err = tx.Exec(ctx, `INSERT INTO rating (fideid, period, `+colList+`)
			SELECT fideid, $1, rating, games, k, flag FROM rating_stage
			ON CONFLICT (fideid, period) DO UPDATE SET `+strings.Join(set, ", ")+`
			WHERE (`+strings.Join(old, ", ")+`) IS DISTINCT FROM (`+prefixed("EXCLUDED", cols)+`)`, period)
		if err != nil {
			return fmt.Errorf("upsert %s %s: %w", ratingType, period.Format("2006-01"), err)
		}
		res.Updated = int(tag.RowsAffected()) // new or changed
		if res.Removed > 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM rating WHERE period = $1
				AND std_rating IS NULL AND rapid_rating IS NULL AND blitz_rating IS NULL`, period); err != nil {
				return err
			}
		}
		res.Players = int(n)
		return recordImport(ctx, tx, sourceFileID, started, res)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// VacuumRating makes the space left by an import's upserts reusable. Each file
// updates rows the previous file of the month created, leaving dead row versions
// (2.8 M after bootstrapping 6 months); without a vacuum they'd wait for
// autovacuum, and the next import would grow the table instead of reusing them.
func (pg *PGDB) VacuumRating() error {
	_, err := pg.db.Exec("VACUUM (ANALYZE) rating")
	return err
}

// RatingMonth is a player's ratings in one month; nil means not rated in that type.
type RatingMonth struct {
	FideID      int       `db:"fideid"`
	Period      time.Time `db:"period"`
	StdRating   *int      `db:"std_rating"`
	StdGames    *int      `db:"std_games"`
	StdK        *int      `db:"std_k"`
	StdFlag     *string   `db:"std_flag"`
	RapidRating *int      `db:"rapid_rating"`
	RapidGames  *int      `db:"rapid_games"`
	RapidK      *int      `db:"rapid_k"`
	RapidFlag   *string   `db:"rapid_flag"`
	BlitzRating *int      `db:"blitz_rating"`
	BlitzGames  *int      `db:"blitz_games"`
	BlitzK      *int      `db:"blitz_k"`
	BlitzFlag   *string   `db:"blitz_flag"`
}

// History returns a player's monthly ratings, oldest first.
func (dao *PlayerPG) History(fideid int) ([]RatingMonth, error) {
	months := []RatingMonth{}
	return months, dao.db.Select(&months, "SELECT * FROM rating WHERE fideid = $1 ORDER BY period", fideid)
}
