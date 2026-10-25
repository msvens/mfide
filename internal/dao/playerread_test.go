package dao

import (
	"database/sql"
	"errors"
	"testing"
)

// readPlayers is a small synthetic list (profiles) for search and top-list tests.
func readPlayers() []*Player {
	y := func(v int) *int { return &v }
	return []*Player{
		{FideID: 1, Name: "Svensson, Olle", Country: "SWE", Sex: "M", BirthYear: y(2014)},
		{FideID: 2, Name: "Colle, Olle", Country: "SWE", Sex: "M", BirthYear: y(1980)},
		{FideID: 3, Name: "Soederberg, Anna", Country: "GER", Sex: "F", BirthYear: y(2008)},
		{FideID: 4, Name: "Soderberg, Erik", Country: "SWE", Sex: "M", BirthYear: y(1990)},
		{FideID: 5, Name: "Boman. Simon", Country: "SWE", Sex: "M", BirthYear: y(2007)},
		{FideID: 6, Name: "Inactive, Ivan", Country: "SWE", Sex: "M", BirthYear: y(1960)},
		{FideID: 7, Name: "Unrated, Ulla", Country: "SWE", Sex: "F", BirthYear: y(2010)},
		{FideID: 8, Name: "Strong, Stina", Country: "NOR", Sex: "F", BirthYear: y(1999)},
		{FideID: 9, Name: "Praggnanandhaa R", Country: "IND", Sex: "M", BirthYear: y(2005)},
		{FideID: 11, Name: "Stalmach, Richard", Country: "CZE", Sex: "M", BirthYear: y(1970)},
		{FideID: 12, Name: "Stal, Alvar", Country: "SWE", Sex: "M", BirthYear: y(2013)},
		{FideID: 13, Name: "Stal, Rolf", Country: "SWE", Sex: "M", BirthYear: y(1950)}, // unrated
	}
}

// importRatings imports one complete month (all three types) of readPlayers' ratings.
func importRatings(t *testing.T, pg *PGDB) {
	t.Helper()
	importArchive(t, pg, "standard", month(10),
		&ArchiveRow{FideID: 1, Rating: 1536, Games: 2}, &ArchiveRow{FideID: 2, Rating: 1700},
		&ArchiveRow{FideID: 3, Rating: 2300, Games: 5}, &ArchiveRow{FideID: 4, Rating: 2300, Games: 5},
		&ArchiveRow{FideID: 5, Rating: 2300, Games: 5}, &ArchiveRow{FideID: 6, Rating: 2600, Flag: "i"},
		&ArchiveRow{FideID: 8, Rating: 2450, Flag: "w"}, &ArchiveRow{FideID: 9, Rating: 2763, Games: 10},
		&ArchiveRow{FideID: 11, Rating: 2456, Flag: "i"}, &ArchiveRow{FideID: 12, Rating: 1628})
	// Ivan is inactive in standard but active in rapid.
	importArchive(t, pg, "rapid", month(10),
		&ArchiveRow{FideID: 1, Rating: 1497}, &ArchiveRow{FideID: 6, Rating: 2500, Games: 3})
	importArchive(t, pg, "blitz", month(10), &ArchiveRow{FideID: 7, Rating: 1400, Flag: "w"})
}

func readDB(t *testing.T) *PGDB {
	t.Helper()
	pg := openUpgradedTestDB(t)
	importList(t, pg, sourceFile(t, pg), readPlayers())
	importRatings(t, pg)
	return pg
}

func ids(ps []PlayerWithRatings) []int {
	out := make([]int, len(ps))
	for i, p := range ps {
		out[i] = p.FideID
	}
	return out
}

func sameIDs(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPlayerSearch(t *testing.T) {
	pg := readDB(t)
	cases := []struct {
		name    string
		terms   [][]string
		whole   bool
		country string
		want    []int
	}{
		{"any order", [][]string{{"olle"}, {"svensson"}}, false, "", []int{1}},
		{"word prefix, not substring", [][]string{{"olle"}}, false, "", []int{2, 1}}, // "Colle, Olle" matches on Olle, not on Colle
		{"prefix of a later word", [][]string{{"svens"}, {"o"}}, false, "", []int{1}},
		// Both Soderbergs have 2300; ties order by name: "Soderberg, Erik" < "Soederberg, Anna".
		{"both umlaut spellings", [][]string{{"soderberg", "soederberg"}}, false, "", []int{4, 3}},
		{"punctuation in the data", [][]string{{"boman"}, {"simon"}}, false, "", []int{5}},
		{"no comma", [][]string{{"pragg"}}, false, "", []int{9}},
		{"whole-word match before stronger prefix match", [][]string{{"alvar"}, {"stal"}}, false, "", []int{12}},
		// "stal" is complete (not the last word): Stal, Rolf (unrated) before Stalmach, Richard (2456).
		{"a complete word ranks first", [][]string{{"stal"}, {"r"}}, false, "", []int{13, 11}},
		{"the last word is still being typed: by rating", [][]string{{"stal"}}, false, "", []int{11, 12, 13}},
		{"prefix only, by current rating", [][]string{{"sta"}}, false, "", []int{11, 12, 13}},
		{"country filter", [][]string{{"soderberg", "soederberg"}}, false, "GER", []int{3}},
		{"whole words only", [][]string{{"olle"}}, true, "", []int{2, 1}},
		{"whole words: a prefix is not enough", [][]string{{"svens"}, {"olle"}}, true, "", nil},
		{"whole words: no substring of a longer word", [][]string{{"stal"}}, true, "", []int{12, 13}},
		{"whole words: umlaut spellings still alternatives", [][]string{{"soderberg", "soederberg"}}, true, "", []int{4, 3}},
	}
	for _, c := range cases {
		got, err := pg.Player.Search(c.terms, c.whole, c.country, 20)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !sameIDs(ids(got), c.want) {
			t.Errorf("%s: got %v, want %v", c.name, ids(got), c.want)
		}
	}

	if p, err := pg.Player.Get(1); err != nil || val(p.StdRating) != 1536 || val(p.RapidRating) != 1497 || p.BlitzRating != nil {
		t.Errorf("get with current ratings = (%+v, %v)", p, err)
	}

	// Removed players are not found, even though their rating rows remain.
	importList(t, pg, sourceFile(t, pg), append(readPlayers()[1:], &Player{FideID: 10, Name: "Filler, One", Country: "SWE", Sex: "M"}))
	if got, _ := pg.Player.Search([][]string{{"svensson"}}, false, "", 20); len(got) != 0 {
		t.Errorf("removed player found: %v", ids(got))
	}
	if _, err := pg.Player.Get(1); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("get removed player: %v, want sql.ErrNoRows", err)
	}
	if p, err := pg.Player.Get(10); err != nil || p.StdRating != nil {
		t.Errorf("player without ratings = (%+v, %v), want null ratings", p, err)
	}
}

func TestPlayerTop(t *testing.T) {
	pg := readDB(t)
	cases := []struct {
		q    TopQuery
		want []int
	}{
		// 2763, 2450, then three at 2300 with 5 games ordered by name, then 1700, 1628, 1536.
		// Inactive in standard (6, 11) and unrated (7) are out.
		{TopQuery{Type: "standard", List: "open"}, []int{9, 8, 5, 4, 3, 2, 12, 1}},
		{TopQuery{Type: "standard", List: "women"}, []int{8, 3}},
		{TopQuery{Type: "standard", List: "juniors"}, []int{5, 3, 12, 1}}, // born 2006+ for 2026; 9 (2005) is out
		{TopQuery{Type: "standard", List: "girls"}, []int{3}},
		{TopQuery{Type: "standard", List: "open", Country: "SWE"}, []int{5, 4, 2, 12, 1}},
		// Ivan (6) is inactive in standard but active in rapid.
		{TopQuery{Type: "rapid", List: "open"}, []int{6, 1}},
		{TopQuery{Type: "blitz", List: "open"}, []int{7}},
	}
	for _, c := range cases {
		c.q.Limit, c.q.Year = 100, 2026
		got, err := pg.Player.Top(c.q)
		if err != nil {
			t.Fatalf("%+v: %v", c.q, err)
		}
		if !sameIDs(ids(got), c.want) {
			t.Errorf("%+v: got %v, want %v", c.q, ids(got), c.want)
		}
	}
	if got, _ := pg.Player.Top(TopQuery{Type: "standard", List: "open", Limit: 2, Year: 2026}); !sameIDs(ids(got), []int{9, 8}) {
		t.Errorf("limit: got %v", ids(got))
	}
	if _, err := pg.Player.Top(TopQuery{Type: "bullet", List: "open", Limit: 1}); err == nil {
		t.Error("unknown type should fail")
	}
}

func TestCurrentMonth(t *testing.T) {
	pg := openUpgradedTestDB(t)
	importList(t, pg, sourceFile(t, pg), readPlayers())
	open := TopQuery{Type: "standard", List: "open", Limit: 100, Year: 2026}

	// No ratings imported yet: profiles with null ratings, empty top lists.
	if p, err := pg.Player.Get(9); err != nil || p.StdRating != nil {
		t.Errorf("before any month: (%+v, %v)", p, err)
	}
	if got, _ := pg.Player.Top(open); len(got) != 0 {
		t.Errorf("before any month: top = %v", ids(got))
	}

	importRatings(t, pg) // October, complete
	// November, standard only: not current until all three types are in.
	importArchive(t, pg, "standard", month(11), &ArchiveRow{FideID: 9, Rating: 2780})
	if p, _ := pg.Player.Get(9); val(p.StdRating) != 2763 {
		t.Errorf("half-imported November must not be current: std = %d", val(p.StdRating))
	}

	importArchive(t, pg, "rapid", month(11), &ArchiveRow{FideID: 9, Rating: 2700})
	importArchive(t, pg, "blitz", month(11), &ArchiveRow{FideID: 9, Rating: 2750})
	if p, _ := pg.Player.Get(9); val(p.StdRating) != 2780 || val(p.BlitzRating) != 2750 {
		t.Errorf("complete November should be current: %+v", p.CurrentRatings)
	}
	if got, _ := pg.Player.Top(open); !sameIDs(ids(got), []int{9}) {
		t.Errorf("November top = %v, want only 9", ids(got))
	}
}
