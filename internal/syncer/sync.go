package syncer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/msvens/mfide/internal/dao"
	"github.com/msvens/mfide/internal/fide"
)

// Summary is what a sync run did, one line per step.
type Summary struct {
	Lines  []string
	Failed bool // a step failed; the others still ran
}

// run is one sync in progress.
type run struct {
	ctx     context.Context
	pg      *dao.PGDB
	o       Options
	now     time.Time
	report  func(string)
	s       *Summary
	changed bool // rating changed, so it needs a vacuum
}

func (r *run) step(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	r.s.Lines = append(r.s.Lines, line)
	r.report(line)
}

func (r *run) fail(format string, args ...any) {
	r.s.Failed = true
	r.step("FAILED: "+format, args...)
}

// Run does one sync: fetch and import what FIDE changed, then apply the
// retention rules. report gets each summary line as it happens. Step failures
// are in the summary (Failed) and the later steps still run; the error is for
// ctx being cancelled (the step in progress finishes or rolls back) or the lock.
// A run waits for another sync, fetch or import to finish; what that one already
// did is then skipped.
func Run(ctx context.Context, pg *dao.PGDB, o Options, report func(string)) (*Summary, error) {
	unlock, err := pg.SyncLock(ctx, func() { report("waiting for another sync, fetch or import to finish") })
	if err != nil {
		return nil, fmt.Errorf("sync lock: %w", err)
	}
	defer unlock()
	id, err := pg.SyncRun.Start()
	if err != nil {
		return nil, err
	}
	r := &run{ctx: ctx, pg: pg, o: o, now: time.Now(), report: report, s: &Summary{}}
	for _, step := range []func(){r.profiles, r.ratings, r.purge, r.capHistory, r.prune, r.vacuum} {
		if err = ctx.Err(); err != nil {
			r.step("interrupted")
			break
		}
		step()
	}
	ok := err == nil && !r.s.Failed
	if ferr := pg.SyncRun.Finish(id, ok, strings.Join(r.s.Lines, "\n")); ferr != nil && err == nil {
		err = ferr
	}
	return r.s, err
}

// profiles fetches the current list and imports the latest one if it isn't yet
// (which also covers a fetch whose import failed last time).
func (r *run) profiles() {
	if f := FetchCurrent(r.pg, r.o); f.Err != nil {
		r.fail("current list: %v", f.Err)
	} else {
		r.step("current list: %s", f.Outcome)
	}
	f, err := LatestCurrent(r.pg, 0)
	if err != nil {
		r.fail("current list: %v", err)
		return
	}
	if done, err := r.pg.ImportRun.Imported(f.Id); err != nil {
		r.fail("current list: %v", err)
		return
	} else if done {
		return
	}
	start := time.Now()
	res, err := ImportCurrentFile(r.pg, r.o.Root, f)
	if err != nil {
		r.fail("import current list %d: %v", f.Id, err)
		return
	}
	r.step("imported current list %d: %d players, %d new, %d changed, %d removed in %s",
		f.Id, res.Players, res.Inserted, res.Updated, res.Removed, since(start))
}

// ratings checks last month's and this month's archive files and imports the
// latest of each that isn't imported yet. Last month is included to see its
// final version after the rollover (FIDE stops changing it around the 30th, so
// then it's a cheap 304). This month's files 404 until FIDE publishes them in
// the first days of the month; the current month stays the last complete one.
func (r *run) ratings() {
	if err := sleep(r.ctx, r.o.Delay); err != nil {
		return
	}
	now := r.now.UTC()
	this := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := this.AddDate(0, -1, 0)
	counts := map[string]int{}
	err := FetchArchive(r.ctx, r.pg, r.o, []time.Time{last, this}, fide.RatingTypes, true, func(f Fetched) {
		switch {
		case f.Err != nil && errors.Is(f.Err, fide.ErrNotFound) && f.Period.Equal(this):
			counts["not published yet"]++
		case f.Err != nil:
			r.fail("%s: %v", f.Name, f.Err)
		default:
			counts[f.Outcome.String()]++
		}
	})
	if err != nil {
		if r.ctx.Err() == nil {
			r.fail("archive files: %v", err)
		}
		return
	}
	var parts []string
	for _, k := range []string{fide.New.String(), fide.Unchanged.String(), fide.NotModified.String(), "not published yet"} {
		if counts[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
		}
	}
	r.step("archive files %s and %s: %s", last.Format("2006-01"), this.Format("2006-01"), strings.Join(parts, ", "))

	files, err := LatestArchives(r.pg, last, this, "all")
	if err != nil {
		r.fail("archive files: %v", err)
		return
	}
	for _, f := range files {
		if r.ctx.Err() != nil {
			return
		}
		if done, err := r.pg.ImportRun.Imported(f.Id); err != nil {
			r.fail("%s: %v", ArchiveKey(f), err)
			continue
		} else if done {
			continue
		}
		start := time.Now()
		res, err := ImportArchiveFile(r.pg, r.o.Root, f)
		if err != nil {
			r.fail("import %s: %v", ArchiveKey(f), err)
			continue
		}
		r.changed = r.changed || res.Updated > 0 || res.Removed > 0
		r.step("imported %s: %d players, %d new or changed, %d dropped in %s",
			ArchiveKey(f), res.Players, res.Updated, res.Removed, since(start))
	}
}

// purge deletes players FIDE removed more than the grace period ago, with their
// ratings ("deletion that follows FIDE").
func (r *run) purge() {
	cutoff := r.now.AddDate(0, 0, -r.o.RemovedPlayersDays)
	players, ratings, err := r.pg.PurgeRemoved(cutoff)
	if err != nil {
		r.fail("purge removed players: %v", err)
		return
	}
	r.changed = r.changed || ratings > 0
	r.step("purged %d players removed before %s, %d rating rows", players, cutoff.Format(time.DateOnly), ratings)
}

func (r *run) capHistory() {
	n, err := r.pg.CapHistory(r.o.HistoryMonths)
	if err != nil {
		r.fail("cap history: %v", err)
		return
	}
	r.changed = r.changed || n > 0
	r.step("history capped at %d months: %d rating rows deleted", r.o.HistoryMonths, n)
}

func (r *run) prune() {
	n, bytes, err := PruneFiles(r.pg, r.o.Root, r.o.KeepCurrentFiles)
	if err != nil {
		r.fail("prune downloads: %v", err)
	}
	r.step("pruned %d downloaded files (%.1f MB)", n, float64(bytes)/1e6)
}

func (r *run) vacuum() {
	if !r.changed {
		return
	}
	start := time.Now()
	if err := r.pg.VacuumRating(); err != nil {
		r.fail("vacuum rating: %v", err)
		return
	}
	r.step("vacuumed rating in %s", since(start))
}

func since(t time.Time) time.Duration {
	return time.Since(t).Round(100 * time.Millisecond)
}
