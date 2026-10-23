package dao

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestSourceFile(t *testing.T) {
	pg := openUpgradedTestDB(t)
	const url = "https://example.org/standard_sep26frl_xml.zip"

	if _, err := pg.SourceFile.Latest(url); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("latest on empty table: got %v, want sql.ErrNoRows", err)
	}

	std := "standard"
	sep := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	lm := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	first := &SourceFile{Kind: SourceArchive, RatingType: &std, Period: &sep, Url: url,
		Path: "downloads/archive/2026-09/standard_sep26frl_xml.zip", Size: 10, Sha256: "aaa",
		Etag: `"e1"`, LastModified: &lm}
	if err := pg.SourceFile.Insert(first); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if first.Id == 0 || first.FetchedAt.IsZero() {
		t.Errorf("insert should fill id and timestamps, got %+v", first)
	}

	second := *first
	second.Sha256, second.Path = "bbb", first.Path+".bbb"
	if err := pg.SourceFile.Insert(&second); err != nil {
		t.Fatalf("insert second: %v", err)
	}
	latest, err := pg.SourceFile.Latest(url)
	if err != nil || latest.Sha256 != "bbb" {
		t.Fatalf("latest = (%+v, %v), want the second file", latest, err)
	}
	if latest.Period == nil || !latest.Period.Equal(sep) || *latest.RatingType != std || !latest.LastModified.Equal(lm) {
		t.Errorf("round trip lost fields: %+v", latest)
	}

	before := latest.CheckedAt
	if err := pg.SourceFile.Touch(latest.Id); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if again, _ := pg.SourceFile.Latest(url); !again.CheckedAt.After(before) {
		t.Error("touch should advance checked_at")
	}

	current := &SourceFile{Kind: SourceCurrent, Url: "https://example.org/players_list_xml.zip",
		Path: "downloads/current/2026-10-23/players_list_xml.zip", Size: 50, Sha256: "ccc"}
	if err := pg.SourceFile.Insert(current); err != nil {
		t.Fatalf("insert current: %v", err)
	}
	if all, _ := pg.SourceFile.List(""); len(all) != 3 {
		t.Errorf("list all = %d files, want 3", len(all))
	}
	if cur, _ := pg.SourceFile.List(SourceCurrent); len(cur) != 1 || cur[0].RatingType != nil || cur[0].Period != nil {
		t.Errorf("list current = %+v", cur)
	}
}
