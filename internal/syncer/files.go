// Package syncer keeps the database in step with FIDE: it fetches and imports
// the published files and applies the retention rules. The fetch and import
// commands, `mfide sync` and the service's daily scheduler all use it.
package syncer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/msvens/mfide/internal/config"
	"github.com/msvens/mfide/internal/dao"
	"github.com/msvens/mfide/internal/fide"
)

// Options are the settings a sync needs; OptionsFromConfig reads them.
type Options struct {
	Root               string        // service.root; downloads go under it
	BaseURL            string        // FIDE's download location
	Delay              time.Duration // pause between downloads
	KeepCurrentFiles   int
	RemovedPlayersDays int
	HistoryMonths      int
}

func OptionsFromConfig() Options {
	return Options{
		Root:               config.ServiceRoot(),
		BaseURL:            config.FideBaseURL(),
		Delay:              config.FideFetchDelay(),
		KeepCurrentFiles:   config.RetentionCurrentFiles(),
		RemovedPlayersDays: config.RetentionRemovedPlayersDays(),
		HistoryMonths:      config.RetentionHistoryMonths(),
	}
}

// Fetched is the result of checking one file.
type Fetched struct {
	Name    string
	Period  time.Time // archive files only
	Outcome fide.Outcome
	File    *dao.SourceFile
	Skipped bool // not requested: we already have this (frozen) file
	Err     error
}

// FetchCurrent downloads the combined current list if it changed.
func FetchCurrent(pg *dao.PGDB, o Options) Fetched {
	url := fide.CurrentURL(o.BaseURL)
	r := Fetched{Name: path.Base(url)}
	r.Outcome, r.File, r.Err = fide.Download(fide.NewFetcher(o.Root), pg.SourceFile, fide.Target{
		Kind:    dao.SourceCurrent,
		Url:     url,
		DestRel: path.Join("downloads", "current", time.Now().Format(time.DateOnly), r.Name),
	})
	return r
}

// FetchArchive checks the archive files for each month and type, one at a time
// with o.Delay between requests, and calls each with every result. FIDE keeps
// regenerating a month's file until the month is over (October's changed on
// 22 Oct; September's last on 29 Sep), so the current month is always requested;
// earlier months we already have only if recheck. The error is for the
// database or ctx; failed downloads are reported through each.
func FetchArchive(ctx context.Context, pg *dao.PGDB, o Options, months []time.Time, types []string, recheck bool, each func(Fetched)) error {
	f := fide.NewFetcher(o.Root)
	now := time.Now().UTC()
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	requested := false
	for _, p := range months {
		for _, typ := range types {
			url := fide.ArchiveURL(o.BaseURL, typ, p)
			r := Fetched{Name: path.Base(url), Period: p}
			if !recheck && p.Before(thisMonth) {
				if _, err := pg.SourceFile.Latest(url); err == nil {
					r.Skipped = true
					each(r)
					continue
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
			if requested {
				if err := sleep(ctx, o.Delay); err != nil { // be polite: one file at a time, with a pause
					return err
				}
			}
			requested = true
			period, ratingType := p, typ
			r.Outcome, r.File, r.Err = fide.Download(f, pg.SourceFile, fide.Target{
				Kind:       dao.SourceArchive,
				RatingType: &ratingType,
				Period:     &period,
				Url:        url,
				DestRel:    path.Join("downloads", "archive", p.Format("2006-01"), r.Name),
			})
			each(r)
		}
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// LatestCurrent is the current-list file with that id, or the latest one if id is 0.
func LatestCurrent(pg *dao.PGDB, id int) (*dao.SourceFile, error) {
	files, err := pg.SourceFile.List(dao.SourceCurrent)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("no current list downloaded yet; run `fetch current` first")
	}
	if id == 0 {
		return &files[len(files)-1], nil // List is oldest first
	}
	for i := range files {
		if files[i].Id == id {
			return &files[i], nil
		}
	}
	return nil, fmt.Errorf("no current-list file with id %d (see `fetch list`)", id)
}

// LatestArchives returns the latest download per (month, type) in [from, to],
// oldest month first: a regenerated file supersedes the older one. typ is a
// rating type or "all". Files already deleted from disk are left out (pruning
// only deletes imported or superseded files).
func LatestArchives(pg *dao.PGDB, from, to time.Time, typ string) ([]*dao.SourceFile, error) {
	files, err := pg.SourceFile.List(dao.SourceArchive)
	if err != nil {
		return nil, err
	}
	latest := map[string]*dao.SourceFile{}
	var order []string
	for i := range files {
		f := &files[i]
		if f.Period.Before(from) || f.Period.After(to) || (typ != "all" && *f.RatingType != typ) {
			continue
		}
		key := ArchiveKey(f)
		if _, seen := latest[key]; !seen {
			order = append(order, key)
		}
		latest[key] = f // List is ordered by fetched_at within (period, type)
	}
	out := []*dao.SourceFile{}
	for _, key := range order {
		if f := latest[key]; f.DeletedAt == nil {
			out = append(out, f)
		}
	}
	return out, nil
}

// ArchiveKey names an archive file by month and type, e.g. "2026-10 standard".
func ArchiveKey(f *dao.SourceFile) string {
	return f.Period.Format("2006-01") + " " + *f.RatingType
}

// ImportCurrentFile imports a downloaded current list into player.
func ImportCurrentFile(pg *dao.PGDB, root string, f *dao.SourceFile) (*dao.ImportResult, error) {
	r, err := fide.OpenZip(filepath.Join(root, f.Path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return pg.ImportCurrent(f.Id, r)
}

// ImportArchiveFile imports a downloaded archive file into rating.
func ImportArchiveFile(pg *dao.PGDB, root string, f *dao.SourceFile) (*dao.ImportResult, error) {
	r, err := fide.OpenZip(filepath.Join(root, f.Path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return pg.ImportArchive(f.Id, *f.RatingType, *f.Period, r)
}

// PruneFiles deletes the downloaded files the retention rule allows (see
// dao.SourceFilePG.PruneCandidates) and marks them deleted. A file that is
// already gone from disk is just marked.
func PruneFiles(pg *dao.PGDB, root string, keepCurrent int) (files int, bytes int64, err error) {
	candidates, err := pg.SourceFile.PruneCandidates(max(keepCurrent, 1))
	if err != nil {
		return 0, 0, err
	}
	for _, f := range candidates {
		if err := os.Remove(filepath.Join(root, f.Path)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return files, bytes, err
		}
		if err := pg.SourceFile.MarkDeleted(f.Id); err != nil {
			return files, bytes, err
		}
		files++
		bytes += f.Size
	}
	return files, bytes, nil
}
