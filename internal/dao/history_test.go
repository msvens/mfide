package dao

import (
	"io"
	"testing"
	"time"
)

// archiveRows is an ArchiveSource over a slice.
type archiveRows []*ArchiveRow

func (r *archiveRows) NextArchiveRow() (*ArchiveRow, error) {
	if len(*r) == 0 {
		return nil, io.EOF
	}
	row := (*r)[0]
	*r = (*r)[1:]
	return row, nil
}

func month(m time.Month) time.Time {
	return time.Date(2026, m, 1, 0, 0, 0, 0, time.UTC)
}

func importArchive(t *testing.T, pg *PGDB, typ string, period time.Time, rows ...*ArchiveRow) *ImportResult {
	t.Helper()
	src := archiveRows(rows)
	res, err := pg.ImportArchive(archiveFile(t, pg, typ, period), typ, period, &src)
	if err != nil {
		t.Fatalf("import %s %s: %v", typ, period.Format("2006-01"), err)
	}
	return res
}

// archiveFile registers a downloaded archive file, as fetch does; the current
// month is derived from imports of these.
func archiveFile(t *testing.T, pg *PGDB, typ string, period time.Time) int {
	t.Helper()
	f := &SourceFile{Kind: SourceArchive, RatingType: &typ, Period: &period,
		Url: "https://example.org/" + typ + period.Format("200601") + ".zip", Path: "a.zip", Size: 1, Sha256: "abc"}
	if err := pg.SourceFile.Insert(f); err != nil {
		t.Fatal(err)
	}
	return f.Id
}

func val(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func TestArchiveImportAndHistory(t *testing.T) {
	pg := openUpgradedTestDB(t)

	importArchive(t, pg, "standard", month(9), &ArchiveRow{FideID: 1, Rating: 1500, Games: 3, K: 40}, &ArchiveRow{FideID: 2, Rating: 2000, K: 20})
	importArchive(t, pg, "standard", month(10), &ArchiveRow{FideID: 1, Rating: 1520, Games: 5, K: 40}, &ArchiveRow{FideID: 2, Rating: 2001, K: 20})
	// Player 1 is inactive in blitz but active in standard: flags are per type.
	importArchive(t, pg, "blitz", month(10), &ArchiveRow{FideID: 1, Rating: 1400, Games: 9, K: 20, Flag: "i"})

	h, err := pg.Player.History(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 || !h[0].Period.Equal(month(9)) || val(h[0].StdRating) != 1500 || h[0].BlitzRating != nil {
		t.Fatalf("history = %+v", h)
	}
	oct := h[1]
	if val(oct.StdRating) != 1520 || val(oct.StdGames) != 5 || *oct.StdFlag != "" ||
		val(oct.BlitzRating) != 1400 || *oct.BlitzFlag != "i" || oct.RapidRating != nil {
		t.Errorf("October = %+v", oct)
	}

	// Re-importing October standard replaces that type only; blitz stays, and a
	// player missing from the new file loses the month (no rating left in any type).
	res := importArchive(t, pg, "standard", month(10), &ArchiveRow{FideID: 1, Rating: 1525, Games: 6, K: 40})
	if res.Players != 1 || res.Updated != 1 || res.Removed != 1 {
		t.Errorf("re-import %+v, want 1 player: 1 changed, 1 cleared", res)
	}
	// Re-importing the same data again writes nothing.
	if res = importArchive(t, pg, "standard", month(10), &ArchiveRow{FideID: 1, Rating: 1525, Games: 6, K: 40}); res.Updated != 0 || res.Removed != 0 {
		t.Errorf("unchanged re-import %+v, want nothing written", res)
	}
	h, _ = pg.Player.History(1)
	if val(h[1].StdRating) != 1525 || val(h[1].BlitzRating) != 1400 {
		t.Errorf("after re-import October = %+v", h[1])
	}
	if h2, _ := pg.Player.History(2); len(h2) != 1 || !h2[0].Period.Equal(month(9)) {
		t.Errorf("player 2 should keep only September: %+v", h2)
	}
	if none, err := pg.Player.History(3); err != nil || len(none) != 0 {
		t.Errorf("unknown player history = (%+v, %v), want empty", none, err)
	}

	// A file with no players is refused, leaving the month as it was.
	empty := archiveRows{}
	if _, err := pg.ImportArchive(sourceFile(t, pg), "standard", month(9), &empty); err == nil {
		t.Error("an empty archive file should be refused")
	}
	if h2, _ := pg.Player.History(2); len(h2) != 1 {
		t.Errorf("a refused import must not change the month: %+v", h2)
	}
	if _, err := pg.ImportArchive(sourceFile(t, pg), "bullet", month(9), &empty); err == nil {
		t.Error("unknown type should fail")
	}
}
