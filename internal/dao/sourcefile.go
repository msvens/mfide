package dao

import (
	"time"

	"github.com/jmoiron/sqlx"
)

// schemaSourceFile records every distinct file downloaded from FIDE: one row per
// content (sha256), not per request. Checking again and finding it unchanged only
// bumps checked_at. Rows outlive the files: pruning sets deleted_at, so the record
// of what FIDE published, and when, is kept.
const schemaSourceFile = `
CREATE TABLE source_file (
	id            SERIAL PRIMARY KEY,
	kind          TEXT NOT NULL,
	rating_type   TEXT,
	period        DATE,
	url           TEXT NOT NULL,
	path          TEXT NOT NULL,
	size          BIGINT NOT NULL,
	sha256        TEXT NOT NULL,
	etag          TEXT NOT NULL DEFAULT '',
	last_modified TIMESTAMPTZ,
	fetched_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
	checked_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
	deleted_at    TIMESTAMPTZ
);
CREATE INDEX source_file_url_idx ON source_file (url, fetched_at DESC);
`

const (
	SourceCurrent = "current" // the combined list, all three rating types
	SourceArchive = "archive" // one rating type for one month
)

type SourceFile struct {
	Id           int        `db:"id"`
	Kind         string     `db:"kind"`
	RatingType   *string    `db:"rating_type"` // nil for the combined current file
	Period       *time.Time `db:"period"`      // first of the month; nil for current
	Url          string     `db:"url"`
	Path         string     `db:"path"` // relative to service.root
	Size         int64      `db:"size"`
	Sha256       string     `db:"sha256"`
	Etag         string     `db:"etag"`
	LastModified *time.Time `db:"last_modified"`
	FetchedAt    time.Time  `db:"fetched_at"`
	CheckedAt    time.Time  `db:"checked_at"`
	DeletedAt    *time.Time `db:"deleted_at"`
}

type SourceFileDAO interface {
	// Latest returns the most recently fetched file for url, or sql.ErrNoRows.
	Latest(url string) (*SourceFile, error)
	// Insert stores f and fills in its id and timestamps.
	Insert(f *SourceFile) error
	// Touch records that the file was checked again and found unchanged.
	Touch(id int) error
	// List returns all files of a kind ("" for all), oldest period first.
	List(kind string) ([]SourceFile, error)
	// PruneCandidates returns files that may be deleted from disk under the
	// retention rule (see SourceFilePG.PruneCandidates).
	PruneCandidates(keepCurrent int) ([]SourceFile, error)
	// MarkDeleted records that a file was deleted from disk.
	MarkDeleted(id int) error
}

type SourceFilePG struct {
	db *sqlx.DB
}

func NewSourceFilePG(db *sqlx.DB) *SourceFilePG {
	return &SourceFilePG{db: db}
}

func (dao *SourceFilePG) Latest(url string) (*SourceFile, error) {
	f := SourceFile{}
	if err := dao.db.Get(&f, "SELECT * FROM source_file WHERE url = $1 ORDER BY fetched_at DESC, id DESC LIMIT 1", url); err != nil {
		return nil, err
	}
	return &f, nil
}

func (dao *SourceFilePG) Insert(f *SourceFile) error {
	return dao.db.QueryRowx(`INSERT INTO source_file
		(kind, rating_type, period, url, path, size, sha256, etag, last_modified)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, fetched_at, checked_at`,
		f.Kind, f.RatingType, f.Period, f.Url, f.Path, f.Size, f.Sha256, f.Etag, f.LastModified,
	).Scan(&f.Id, &f.FetchedAt, &f.CheckedAt)
}

func (dao *SourceFilePG) Touch(id int) error {
	_, err := dao.db.Exec("UPDATE source_file SET checked_at = now() WHERE id = $1", id)
	return err
}

func (dao *SourceFilePG) List(kind string) ([]SourceFile, error) {
	files := []SourceFile{}
	err := dao.db.Select(&files, `SELECT * FROM source_file WHERE $1 = '' OR kind = $1
		ORDER BY kind, period NULLS FIRST, rating_type, fetched_at`, kind)
	return files, err
}
