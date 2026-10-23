package dao

import (
	"io"
	"testing"
)

// rows is a PlayerSource over a slice.
type rows []*Player

func (r *rows) NextRow() (*Player, error) {
	if len(*r) == 0 {
		return nil, io.EOF
	}
	p := (*r)[0]
	*r = (*r)[1:]
	return p, nil
}

func testPlayer(id int, name string) *Player {
	y := 1990
	return &Player{FideID: id, Name: name, Country: "SWE", Sex: "M", BirthYear: &y}
}

// testList is n players with ids 1..n.
func testList(n int) []*Player {
	ps := make([]*Player, n)
	for i := range ps {
		ps[i] = testPlayer(i+1, "Player, Number")
	}
	return ps
}

func importList(t *testing.T, pg *PGDB, fileID int, ps []*Player) *ImportResult {
	t.Helper()
	src := rows(ps)
	res, err := pg.ImportCurrent(fileID, &src)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return res
}

func sourceFile(t *testing.T, pg *PGDB) int {
	t.Helper()
	f := &SourceFile{Kind: SourceCurrent, Url: "https://example.org/players_list_xml.zip", Path: "x.zip", Size: 1, Sha256: "abc"}
	if err := pg.SourceFile.Insert(f); err != nil {
		t.Fatal(err)
	}
	return f.Id
}

func getPlayer(t *testing.T, pg *PGDB, id int) Player {
	t.Helper()
	var p Player
	if err := pg.db.Get(&p, "SELECT * FROM player WHERE fideid = $1", id); err != nil {
		t.Fatalf("get player %d: %v", id, err)
	}
	return p
}

func TestImportCurrent(t *testing.T) {
	pg := openUpgradedTestDB(t)
	fileID := sourceFile(t, pg)

	res := importList(t, pg, fileID, testList(20))
	if res.Players != 20 || res.Inserted != 20 || res.Updated != 0 || res.Removed != 0 {
		t.Errorf("first import %+v", res)
	}

	if res = importList(t, pg, fileID, testList(20)); res.Inserted+res.Updated+res.Removed != 0 {
		t.Errorf("identical re-import changed something: %+v", res)
	}

	// Next list: 21 is new, 20 is gone, 1 renamed, 2 changed federation, 3 got a title.
	next := testList(19)
	next[0].Name = "Player, Renamed"
	next[1].Country = "NOR"
	next[2].Title = "FM"
	next = append(next, testPlayer(21, "Player, New"))
	res = importList(t, pg, fileID, next)
	if res.Players != 20 || res.Inserted != 1 || res.Updated != 3 || res.Removed != 1 {
		t.Errorf("second import %+v, want 1 inserted, 3 updated, 1 removed", res)
	}
	want := map[string]int{"profile": 3, "returned": 0}
	for k, v := range want {
		if res.Changes[k] != v {
			t.Errorf("changes[%s] = %d, want %d (all: %v)", k, res.Changes[k], v, res.Changes)
		}
	}
	if p := getPlayer(t, pg, 20); p.RemovedAt == nil {
		t.Error("player 20 should be marked removed")
	}
	if p := getPlayer(t, pg, 1); p.Name != "Player, Renamed" || p.RemovedAt != nil {
		t.Errorf("player 1 = %+v", p)
	}

	// Player 20 comes back.
	back := append(next, testPlayer(20, "Player, Number"))
	res = importList(t, pg, fileID, back)
	if res.Updated != 1 || res.Changes["returned"] != 1 || res.Removed != 0 {
		t.Errorf("return import %+v", res)
	}
	if p := getPlayer(t, pg, 20); p.RemovedAt != nil {
		t.Error("a returning player should have removed_at cleared")
	}

	runs, err := pg.ImportRun.List()
	if err != nil || len(runs) != 4 {
		t.Fatalf("runs = (%d, %v), want 4", len(runs), err)
	}
	if ok, _ := pg.ImportRun.Imported(fileID); !ok {
		t.Error("source file should count as imported")
	}
	if ok, _ := pg.ImportRun.Imported(fileID + 1); ok {
		t.Error("an unknown source file should not count as imported")
	}
}

func TestImportCurrentGuards(t *testing.T) {
	pg := openUpgradedTestDB(t)
	fileID := sourceFile(t, pg)
	importList(t, pg, fileID, testList(20))

	// 17 of 20 is below the 90% guard: refused, nothing changed, no run recorded.
	src := rows(testList(17))
	if _, err := pg.ImportCurrent(fileID, &src); err == nil {
		t.Fatal("expected the truncated-file guard to refuse the import")
	}
	var active, runs int
	_ = pg.db.Get(&active, "SELECT count(*) FROM player WHERE removed_at IS NULL")
	_ = pg.db.Get(&runs, "SELECT count(*) FROM import_run")
	if active != 20 || runs != 1 {
		t.Errorf("after a refused import: %d active players, %d runs; want 20 and 1", active, runs)
	}

	dup := rows(append(testList(20), testPlayer(5, "Player, Duplicate")))
	if _, err := pg.ImportCurrent(fileID, &dup); err == nil {
		t.Error("a duplicate fideid in the file should fail the import")
	}
}
