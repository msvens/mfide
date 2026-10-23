package fide

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// writeZip zips content as one entry named name, the way FIDE ships its lists.
func writeZip(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "list.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixtureZip(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/players.xml")
	if err != nil {
		t.Fatal(err)
	}
	return writeZip(t, "players_list_xml_foa.xml", string(b))
}

func readAll(t *testing.T, path string) []*Player {
	t.Helper()
	r, err := OpenZip(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = r.Close() }()
	var players []*Player
	for {
		p, err := r.Next()
		if errors.Is(err, io.EOF) {
			return players
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		players = append(players, p)
	}
}

func TestParseFixture(t *testing.T) {
	players := readAll(t, fixtureZip(t))
	if len(players) != 5 {
		t.Fatalf("parsed %d players, want 5", len(players))
	}

	want := Player{FideID: 9000001, Name: "Testsson, Anna", Country: "SWE", Sex: "F", Title: "GM", WTitle: "WGM",
		Rating: 2701, Games: 9, K: 10, RapidRating: 2650, RapidGames: 11, RapidK: 20,
		BlitzRating: 2688, BlitzK: 20, Birthday: "1995", Flag: "w"}
	if *players[0] != want {
		t.Errorf("first player\n got %+v\nwant %+v", *players[0], want)
	}

	row, err := players[0].Row()
	if err != nil {
		t.Fatal(err)
	}
	if row.BirthYear == nil || *row.BirthYear != 1995 || row.Title != "GM" || row.WTitle != "WGM" || row.Country != "SWE" {
		t.Errorf("row %+v", row)
	}

	// No comma in the name, empty birthday, a FOA title.
	kumar, err := players[2].Row()
	if err != nil {
		t.Fatal(err)
	}
	if kumar.Name != "Sample Kumar" || kumar.BirthYear != nil || kumar.FoaTitle != "AFM" {
		t.Errorf("unrated player without birthday: %+v", kumar)
	}
	if players[3].OTitle != "SI,LSI,FA" || players[3].Flag != "wi" {
		t.Errorf("multi-value o_title / wi flag: %+v", players[3])
	}
}

func TestParseArchiveFormat(t *testing.T) {
	// Archive files carry one rating type in rating/games/k and no rapid_/blitz_ fields.
	xml := `<playerslist><player><fideid>9000009</fideid><name>Archive, Only</name><country>SWE</country>
<sex>M</sex><title></title><w_title></w_title><o_title></o_title><foa_title></foa_title>
<rating>1850</rating><games>3</games><k>40</k><birthday>2001</birthday><flag></flag></player></playerslist>`
	players := readAll(t, writeZip(t, "standard_oct26frl_xml.xml", xml))
	if len(players) != 1 || players[0].Rating != 1850 || players[0].Games != 3 || players[0].RapidRating != 0 {
		t.Errorf("archive player %+v", players)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := OpenZip(writeZip(t, "readme.txt", "hello")); err == nil {
		t.Error("a zip without an .xml entry should fail to open")
	}

	r, err := OpenZip(writeZip(t, "list.xml", "<playerslist><player><fideid>12</fideid><name>Broken"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if _, err := r.Next(); err == nil || errors.Is(err, io.EOF) {
		t.Errorf("truncated XML: got %v, want a parse error", err)
	}

	bad := &Player{FideID: 1, Birthday: "19x5"}
	if _, err := bad.Row(); err == nil {
		t.Error("a non-numeric birthday should be an error")
	}
}
