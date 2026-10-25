package syncer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/msvens/mfide/internal/dao"
	"go.uber.org/zap"
)

// Clock is a time of day.
type Clock struct {
	Hour, Minute int
}

// ParseClock parses "HH:MM" (24-hour).
func ParseClock(s string) (Clock, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return Clock{}, fmt.Errorf("sync.at must look like 03:00, got %q", s)
	}
	return Clock{t.Hour(), t.Minute()}, nil
}

// NextRun is the first time after now that the clock shows at, in now's
// location. On the day clocks spring forward a time in the gap runs an hour
// later (02:30 becomes 03:30).
func NextRun(now time.Time, at Clock) time.Time {
	y, m, d := now.Date()
	t := time.Date(y, m, d, at.Hour, at.Minute, 0, 0, now.Location())
	if !t.After(now) {
		t = time.Date(y, m, d+1, at.Hour, at.Minute, 0, 0, now.Location())
	}
	return t
}

// catchUpAfter is how old the last successful sync may be at startup before the
// scheduler runs one right away instead of waiting for the next slot.
const catchUpAfter = 24 * time.Hour

// needsCatchUp reports whether the service should sync at startup.
func needsCatchUp(lastOK *dao.SyncRun, now time.Time) bool {
	return lastOK == nil || lastOK.FinishedAt == nil || now.Sub(*lastOK.FinishedAt) > catchUpAfter
}

// catchUpDelay lets the service finish starting before a catch-up sync.
const catchUpDelay = 5 * time.Second

// Schedule runs a sync every day at at (local time) until ctx is cancelled, plus
// one at startup if the last successful sync is older than a day. A failed run
// is logged and the next slot tries again; HTTP is never affected. The returned
// channel is closed when the scheduler has stopped (after a run in progress
// noticed the cancel).
func Schedule(ctx context.Context, pg *dao.PGDB, o Options, at Clock, l *zap.SugaredLogger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		last, err := pg.SyncRun.LastOK()
		if err != nil {
			l.Errorw("sync: reading the last run", zap.Error(err))
		}
		if err == nil && needsCatchUp(last, time.Now()) {
			l.Info("sync: last successful sync is over a day old; catching up")
			if sleep(ctx, catchUpDelay) != nil {
				return
			}
			runLogged(ctx, pg, o, l)
		}
		for ctx.Err() == nil {
			next := NextRun(time.Now(), at)
			l.Infow("sync: next run", "at", next.Format(time.DateTime))
			if sleep(ctx, time.Until(next)) != nil {
				return
			}
			runLogged(ctx, pg, o, l)
		}
	}()
	return done
}

// runLogged does one sync and logs it; a panic is logged, not fatal.
func runLogged(ctx context.Context, pg *dao.PGDB, o Options, l *zap.SugaredLogger) {
	defer func() {
		if p := recover(); p != nil {
			l.Errorw("sync: panic", "panic", p)
		}
	}()
	start := time.Now()
	s, err := Run(ctx, pg, o, func(line string) { l.Info("sync: " + line) })
	switch {
	case errors.Is(err, context.Canceled):
		l.Info("sync: interrupted by shutdown; the next start catches up")
	case err != nil:
		l.Errorw("sync: stopped", zap.Error(err))
	case s.Failed:
		l.Warnw("sync: finished with failures", "took", since(start))
	default:
		l.Infow("sync: finished", "took", since(start))
	}
}
