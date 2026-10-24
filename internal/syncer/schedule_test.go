package syncer

import (
	"testing"
	"time"

	"github.com/msvens/mfide/internal/dao"
)

func TestParseClock(t *testing.T) {
	if c, err := ParseClock("03:00"); err != nil || c != (Clock{3, 0}) {
		t.Errorf("03:00 = (%v, %v)", c, err)
	}
	if c, err := ParseClock("23:45"); err != nil || c != (Clock{23, 45}) {
		t.Errorf("23:45 = (%v, %v)", c, err)
	}
	for _, bad := range []string{"", "3", "24:00", "03:60", "3am", "03:00:00"} {
		if _, err := ParseClock(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestNextRun(t *testing.T) {
	sthlm, err := time.LoadLocation("Europe/Stockholm")
	if err != nil {
		t.Skipf("no tz data: %v", err)
	}
	at := func(s string) time.Time {
		t.Helper()
		v, err := time.ParseInLocation(time.DateTime, s, sthlm)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		name  string
		now   string
		clock Clock
		want  string
	}{
		{"later today", "2026-10-24 01:00:00", Clock{3, 0}, "2026-10-24 03:00:00"},
		{"already passed", "2026-10-24 12:00:00", Clock{3, 0}, "2026-10-25 03:00:00"},
		{"exactly now runs tomorrow", "2026-10-24 03:00:00", Clock{3, 0}, "2026-10-25 03:00:00"},
		{"month end", "2026-10-31 23:30:00", Clock{3, 0}, "2026-11-01 03:00:00"},
		// Clocks go back on 25 Oct 2026 at 03:00: 02:30 happens twice, the run is the first.
		{"fall back", "2026-10-25 00:00:00", Clock{2, 30}, "2026-10-25 02:30:00"},
		// Clocks spring forward on 29 Mar 2026 at 02:00: 02:30 doesn't exist.
		{"spring forward gap", "2026-03-29 00:00:00", Clock{2, 30}, "2026-03-29 03:30:00"},
	}
	for _, c := range cases {
		got := NextRun(at(c.now), c.clock)
		if !got.Equal(at(c.want)) {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
		if !got.After(at(c.now)) {
			t.Errorf("%s: %s is not after now", c.name, got)
		}
	}
}

func TestNeedsCatchUp(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	run := func(ago time.Duration) *dao.SyncRun {
		f := now.Add(-ago)
		return &dao.SyncRun{FinishedAt: &f}
	}
	if !needsCatchUp(nil, now) {
		t.Error("never synced should catch up")
	}
	if !needsCatchUp(run(25*time.Hour), now) {
		t.Error("25 h ago should catch up")
	}
	if needsCatchUp(run(9*time.Hour), now) {
		t.Error("9 h ago should wait for the next slot")
	}
}
