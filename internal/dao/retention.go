package dao

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jmoiron/sqlx"
)

// PruneCandidates returns downloaded files that may be deleted from disk. A file
// that was never imported is kept unless a newer imported file of the same URL
// supersedes it.
//   - archive files: once imported (or superseded); the URL stays, so they can be
//     downloaded again
//   - current files: all but the newest keepCurrent imported ones
func (dao *SourceFilePG) PruneCandidates(keepCurrent int) ([]SourceFile, error) {
	files := []SourceFile{}
	err := dao.db.Select(&files, `
		WITH imported AS (
			SELECT sf.* FROM source_file sf
			WHERE EXISTS (SELECT 1 FROM import_run ir WHERE ir.source_file_id = sf.id)
		), keep AS (
			SELECT id FROM imported WHERE kind = 'current' ORDER BY fetched_at DESC, id DESC LIMIT $1
		)
		SELECT sf.* FROM source_file sf
		WHERE sf.deleted_at IS NULL AND sf.id NOT IN (SELECT id FROM keep) AND (
			sf.id IN (SELECT id FROM imported)
			OR EXISTS (SELECT 1 FROM imported i WHERE i.url = sf.url AND i.fetched_at > sf.fetched_at))
		ORDER BY sf.id`, keepCurrent)
	return files, err
}

// MarkDeleted records that a file was deleted from disk; the row stays.
func (dao *SourceFilePG) MarkDeleted(id int) error {
	_, err := dao.db.Exec("UPDATE source_file SET deleted_at = now() WHERE id = $1", id)
	return err
}

// PurgeRemoved deletes players FIDE removed from its list before olderThan,
// together with all their ratings. It also deletes ratings older than olderThan
// of ids that have no player row at all (players who left FIDE's list before our
// first import). Newer such rows are left alone: a player new in a monthly file
// can show up there before the daily list has them.
func (pg *PGDB) PurgeRemoved(olderThan time.Time) (players, ratings int, err error) {
	err = pg.inPgxTx(func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM rating r WHERE
			r.fideid IN (SELECT fideid FROM player WHERE removed_at < $1)
			OR (r.period < $1::date AND NOT EXISTS (SELECT 1 FROM player p WHERE p.fideid = r.fideid))`, olderThan)
		if err != nil {
			return err
		}
		ratings = int(tag.RowsAffected())
		if tag, err = tx.Exec(ctx, "DELETE FROM player WHERE removed_at < $1", olderThan); err != nil {
			return err
		}
		players = int(tag.RowsAffected())
		return nil
	})
	return players, ratings, err
}

// CapHistory deletes ratings so at most keepMonths months remain, counted back
// from the current month (which counts as one). 0 keeps everything, and so does
// a database without a current month.
func (pg *PGDB) CapHistory(keepMonths int) (int, error) {
	if keepMonths <= 0 {
		return 0, nil
	}
	res, err := pg.db.Exec(`DELETE FROM rating
		WHERE period <= `+currentPeriodSQL+` - make_interval(months => $1)`, keepMonths)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// syncLockKey is the advisory lock that keeps everything that writes FIDE data
// (the service's sync, `mfide sync`, `fetch`, `import`, a second instance) from
// overlapping. Any constant works; this is "mfide" in ASCII.
const syncLockKey = 0x6d66696465

// SyncLock takes the write lock, waiting while another process holds it; then it
// calls waiting first, so the caller can say why it's paused. Cancelling ctx stops
// the wait. The lock belongs to one connection, held until unlock is called (or
// the process dies, which releases it too).
func (pg *PGDB) SyncLock(ctx context.Context, waiting func()) (unlock func(), err error) {
	conn, err := pg.db.Connx(ctx)
	if err != nil {
		return nil, err
	}
	// Polls rather than blocking in pg_advisory_lock: a cancelled blocking call
	// leaves the server-side wait queued, and it would briefly grab the lock later.
	for first := true; ; first = false {
		var ok bool
		if err := conn.GetContext(ctx, &ok, "SELECT pg_try_advisory_lock($1)", syncLockKey); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if ok {
			break
		}
		if first && waiting != nil {
			waiting()
		}
		select {
		case <-ctx.Done():
			_ = conn.Close()
			return nil, ctx.Err()
		case <-time.After(syncLockPoll):
		}
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", syncLockKey)
		_ = conn.Close()
	}, nil
}

// syncLockPoll is how often a waiting SyncLock retries.
const syncLockPoll = time.Second

// schemaSyncRun records sync runs, so the service knows when it last synced
// (catch-up after downtime) and /health can report it.
const schemaSyncRun = `
CREATE TABLE sync_run (
	id          SERIAL PRIMARY KEY,
	started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
	finished_at TIMESTAMPTZ,
	ok          BOOLEAN,
	summary     TEXT NOT NULL DEFAULT ''
);
`

type SyncRun struct {
	Id         int        `db:"id"`
	StartedAt  time.Time  `db:"started_at"`
	FinishedAt *time.Time `db:"finished_at"`
	Ok         *bool      `db:"ok"`
	Summary    string     `db:"summary"`
}

type SyncRunDAO interface {
	// Start records a run that has begun and returns its id.
	Start() (int, error)
	// Finish records how a run ended, with its summary.
	Finish(id int, ok bool, summary string) error
	// Last returns the latest finished run, or nil if there is none.
	Last() (*SyncRun, error)
	// LastOK returns the latest successful run, or nil if there is none.
	LastOK() (*SyncRun, error)
}

type SyncRunPG struct {
	db *sqlx.DB
}

func NewSyncRunPG(db *sqlx.DB) *SyncRunPG {
	return &SyncRunPG{db: db}
}

func (dao *SyncRunPG) Start() (int, error) {
	var id int
	return id, dao.db.Get(&id, "INSERT INTO sync_run DEFAULT VALUES RETURNING id")
}

func (dao *SyncRunPG) Finish(id int, ok bool, summary string) error {
	_, err := dao.db.Exec("UPDATE sync_run SET finished_at = now(), ok = $2, summary = $3 WHERE id = $1", id, ok, summary)
	return err
}

func (dao *SyncRunPG) Last() (*SyncRun, error) {
	return dao.last("finished_at IS NOT NULL")
}

func (dao *SyncRunPG) LastOK() (*SyncRun, error) {
	return dao.last("ok")
}

func (dao *SyncRunPG) last(where string) (*SyncRun, error) {
	r := SyncRun{}
	err := dao.db.Get(&r, "SELECT * FROM sync_run WHERE "+where+" ORDER BY finished_at DESC, id DESC LIMIT 1")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Status is what /health reports about the data's freshness.
type Status struct {
	RatingsMonth *time.Time // the current month; nil before any complete month
	LastImport   *time.Time // when the latest import finished
	LastSync     *SyncRun   // the latest finished sync run
}

func (pg *PGDB) Status() (*Status, error) {
	s := &Status{}
	if err := pg.db.Get(&s.RatingsMonth, "SELECT "+currentPeriodSQL); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err := pg.db.Get(&s.LastImport, "SELECT max(finished_at) FROM import_run"); err != nil {
		return nil, err
	}
	var err error
	s.LastSync, err = pg.SyncRun.Last()
	return s, err
}
