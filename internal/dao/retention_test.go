package dao

import (
	"context"
	"testing"
	"time"
)

func TestPruneCandidates(t *testing.T) {
	pg := openUpgradedTestDB(t)
	insert := func(kind, url string) int {
		t.Helper()
		f := &SourceFile{Kind: kind, Url: url, Path: "p", Size: 1, Sha256: "abc"}
		if kind == SourceArchive {
			typ, p := "standard", month(9)
			f.RatingType, f.Period = &typ, &p
		}
		if err := pg.SourceFile.Insert(f); err != nil {
			t.Fatal(err)
		}
		return f.Id
	}
	imported := func(id int) {
		t.Helper()
		if _, err := pg.db.Exec(`INSERT INTO import_run (source_file_id, started_at, finished_at, players, inserted, updated, removed, changes)
			VALUES ($1, now(), now(), 1, 0, 0, 0, '{}')`, id); err != nil {
			t.Fatal(err)
		}
	}
	const cur, arch = "https://example.org/players_list_xml.zip", "https://example.org/standard_sep26frl_xml.zip"

	archDone := insert(SourceArchive, arch)
	imported(archDone)
	archSuperseded := insert(SourceArchive, arch) // never imported, then a newer one was
	archNewest := insert(SourceArchive, arch)
	imported(archNewest)
	archPending := insert(SourceArchive, "https://example.org/standard_oct26frl_xml.zip")

	c1 := insert(SourceCurrent, cur)
	imported(c1)
	c2 := insert(SourceCurrent, cur) // never imported, superseded by c3
	c3 := insert(SourceCurrent, cur)
	imported(c3)
	c4 := insert(SourceCurrent, cur)
	imported(c4)
	c5 := insert(SourceCurrent, cur) // newest, not imported yet

	got, err := pg.SourceFile.PruneCandidates(2)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{archDone, archSuperseded, archNewest, c1, c2}
	if !sameIDs(fileIDs(got), want) {
		t.Errorf("candidates = %v, want %v (kept: pending archive %d, current %d %d %d)", fileIDs(got), want, archPending, c3, c4, c5)
	}

	if err := pg.SourceFile.MarkDeleted(c1); err != nil {
		t.Fatal(err)
	}
	if got, _ = pg.SourceFile.PruneCandidates(1); !sameIDs(fileIDs(got), []int{archDone, archSuperseded, archNewest, c2, c3}) {
		t.Errorf("after deleting %d, keeping 1: %v", c1, fileIDs(got))
	}
}

func fileIDs(fs []SourceFile) []int {
	out := make([]int, len(fs))
	for i, f := range fs {
		out[i] = f.Id
	}
	return out
}

func TestPurgeRemoved(t *testing.T) {
	pg := openUpgradedTestDB(t)
	importList(t, pg, sourceFile(t, pg), testList(20))
	importArchive(t, pg, "standard", month(9), &ArchiveRow{FideID: 1, Rating: 1500}, &ArchiveRow{FideID: 2, Rating: 1600},
		&ArchiveRow{FideID: 3, Rating: 1700}, &ArchiveRow{FideID: 99, Rating: 1800})
	importArchive(t, pg, "standard", month(10), &ArchiveRow{FideID: 1, Rating: 1510}, &ArchiveRow{FideID: 99, Rating: 1810})

	// Players 1 and 2 leave FIDE's list; 1 left long ago, 2 recently. 99 never was in it.
	importList(t, pg, sourceFile(t, pg), testList(20)[2:])
	if _, err := pg.db.Exec("UPDATE player SET removed_at = now() - interval '100 days' WHERE fideid = 1"); err != nil {
		t.Fatal(err)
	}

	players, ratings, err := pg.PurgeRemoved(month(10))
	if err != nil {
		t.Fatal(err)
	}
	// Player 1 with both months; 99's September (October is newer than the cutoff).
	if players != 1 || ratings != 3 {
		t.Errorf("purged %d players, %d ratings; want 1, 3", players, ratings)
	}
	var left []int
	if err := pg.db.Select(&left, "SELECT fideid FROM player WHERE fideid <= 3 ORDER BY fideid"); err != nil {
		t.Fatal(err)
	}
	if !sameIDs(left, []int{2, 3}) {
		t.Errorf("players left = %v, want [2 3]", left)
	}
	if h, _ := pg.Player.History(2); len(h) != 1 {
		t.Errorf("recently removed player lost history: %+v", h)
	}
	if h, _ := pg.Player.History(99); len(h) != 1 || !h[0].Period.Equal(month(10)) {
		t.Errorf("unknown id 99 should keep only October: %+v", h)
	}
}

func TestCapHistory(t *testing.T) {
	pg := openUpgradedTestDB(t)
	if n, err := pg.CapHistory(1); err != nil || n != 0 {
		t.Errorf("no current month: (%d, %v), want a no-op", n, err)
	}
	for m := time.July; m <= time.October; m++ {
		for _, typ := range []string{"standard", "rapid", "blitz"} {
			importArchive(t, pg, typ, month(m), &ArchiveRow{FideID: 1, Rating: 1500})
		}
	}
	if n, _ := pg.CapHistory(0); n != 0 {
		t.Errorf("0 should keep everything, deleted %d", n)
	}
	n, err := pg.CapHistory(2)
	if err != nil || n != 2 {
		t.Errorf("cap 2 = (%d, %v), want July and August deleted", n, err)
	}
	if h, _ := pg.Player.History(1); len(h) != 2 || !h[0].Period.Equal(month(9)) {
		t.Errorf("history after cap = %+v, want September and October", h)
	}
}

func TestSyncLockAndRuns(t *testing.T) {
	pg := openUpgradedTestDB(t)
	ctx := context.Background()
	unlock, err := pg.SyncLock(ctx, func() { t.Error("free lock: shouldn't wait") })
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	// A second taker waits until the first unlocks.
	waiting := make(chan struct{})
	got := make(chan func())
	go func() {
		u, err := pg.SyncLock(ctx, func() { close(waiting) })
		if err != nil {
			t.Errorf("second lock: %v", err)
		}
		got <- u
	}()
	<-waiting
	select {
	case <-got:
		t.Fatal("second lock granted while the first is held")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	unlock2 := <-got
	if unlock2 == nil {
		t.Fatal("second lock not granted after unlock")
	}

	// A waiter whose context ends gives up without the lock.
	cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err := pg.SyncLock(cctx, nil); err == nil {
		t.Error("lock with an expired context should fail while held")
	}
	unlock2()
	unlock3, err := pg.SyncLock(ctx, func() { t.Error("lock is free again: shouldn't wait") })
	if err != nil {
		t.Fatalf("lock after the cancelled waiter: %v", err)
	}
	unlock3()

	if r, err := pg.SyncRun.Last(); err != nil || r != nil {
		t.Errorf("no runs: (%+v, %v)", r, err)
	}
	good, _ := pg.SyncRun.Start()
	_ = pg.SyncRun.Finish(good, true, "all fine")
	bad, _ := pg.SyncRun.Start()
	_ = pg.SyncRun.Finish(bad, false, "fetch failed")
	_, _ = pg.SyncRun.Start() // still running
	if r, _ := pg.SyncRun.Last(); r == nil || r.Id != bad {
		t.Errorf("last = %+v, want the failed run", r)
	}
	if r, _ := pg.SyncRun.LastOK(); r == nil || r.Id != good || r.Summary != "all fine" {
		t.Errorf("last ok = %+v, want the good run", r)
	}

	s, err := pg.Status()
	if err != nil || s.RatingsMonth != nil || s.LastImport != nil || s.LastSync.Id != bad {
		t.Errorf("status = (%+v, %v)", s, err)
	}
}
