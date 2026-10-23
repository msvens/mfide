package dao

import (
	"database/sql"
	"errors"
	"testing"
)

// readPlayers is a small synthetic list for search and top-list tests.
func readPlayers() []*Player {
	y := func(v int) *int { return &v }
	return []*Player{
		{FideID: 1, Name: "Svensson, Olle", Country: "SWE", Sex: "M", BirthYear: y(2014), StdRating: 1536, StdGames: 2, RapidRating: 1497},
		{FideID: 2, Name: "Colle, Olle", Country: "SWE", Sex: "M", BirthYear: y(1980), StdRating: 1700},
		{FideID: 3, Name: "Soederberg, Anna", Country: "GER", Sex: "F", BirthYear: y(2008), StdRating: 2300, StdGames: 5},
		{FideID: 4, Name: "Soderberg, Erik", Country: "SWE", Sex: "M", BirthYear: y(1990), StdRating: 2300, StdGames: 5},
		{FideID: 5, Name: "Boman. Simon", Country: "SWE", Sex: "M", BirthYear: y(2007), StdRating: 2300, StdGames: 5},
		{FideID: 6, Name: "Inactive, Ivan", Country: "SWE", Sex: "M", BirthYear: y(1960), StdRating: 2600, Flag: "i"},
		{FideID: 7, Name: "Unrated, Ulla", Country: "SWE", Sex: "F", BirthYear: y(2010), BlitzRating: 1400, Flag: "w"},
		{FideID: 8, Name: "Strong, Stina", Country: "NOR", Sex: "F", BirthYear: y(1999), StdRating: 2450, Flag: "w"},
		{FideID: 9, Name: "Praggnanandhaa R", Country: "IND", Sex: "M", BirthYear: y(2005), StdRating: 2763, StdGames: 10},
		{FideID: 11, Name: "Stalmach, Richard", Country: "CZE", Sex: "M", BirthYear: y(1970), StdRating: 2456, Flag: "i"},
		{FideID: 12, Name: "Stal, Alvar", Country: "SWE", Sex: "M", BirthYear: y(2013), StdRating: 1628},
	}
}

func readDB(t *testing.T) *PGDB {
	t.Helper()
	pg := openUpgradedTestDB(t)
	importList(t, pg, sourceFile(t, pg), readPlayers())
	return pg
}

func ids(ps []Player) []int {
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
		country string
		want    []int
	}{
		{"any order", [][]string{{"olle"}, {"svensson"}}, "", []int{1}},
		{"word prefix, not substring", [][]string{{"olle"}}, "", []int{2, 1}}, // "Colle, Olle" matches on Olle, not on Colle
		{"prefix of a later word", [][]string{{"svens"}, {"o"}}, "", []int{1}},
		// Both Soderbergs have 2300; ties order by name: "Soderberg, Erik" < "Soederberg, Anna".
		{"both umlaut spellings", [][]string{{"soderberg", "soederberg"}}, "", []int{4, 3}},
		{"punctuation in the data", [][]string{{"boman"}, {"simon"}}, "", []int{5}},
		{"no comma", [][]string{{"pragg"}}, "", []int{9}},
		{"whole-word match before stronger prefix match", [][]string{{"stal"}}, "", []int{12, 11}},
		{"prefix only, by rating", [][]string{{"sta"}}, "", []int{11, 12}},
		{"country filter", [][]string{{"soderberg", "soederberg"}}, "GER", []int{3}},
	}
	for _, c := range cases {
		got, err := pg.Player.Search(c.terms, c.country, 20)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !sameIDs(ids(got), c.want) {
			t.Errorf("%s: got %v, want %v", c.name, ids(got), c.want)
		}
	}

	// Removed players are not found.
	importList(t, pg, sourceFile(t, pg), append(readPlayers()[1:], &Player{FideID: 10, Name: "Filler, One", Country: "SWE", Sex: "M"}))
	if got, _ := pg.Player.Search([][]string{{"svensson"}}, "", 20); len(got) != 0 {
		t.Errorf("removed player found: %v", ids(got))
	}
	if _, err := pg.Player.Get(1); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("get removed player: %v, want sql.ErrNoRows", err)
	}
	if p, err := pg.Player.Get(9); err != nil || p.Name != "Praggnanandhaa R" {
		t.Errorf("get = (%+v, %v)", p, err)
	}
}

func TestPlayerTop(t *testing.T) {
	pg := readDB(t)
	cases := []struct {
		q    TopQuery
		want []int
	}{
		// 2763, 2450, then three at 2300 with 5 games ordered by name, then 1700, 1628, 1536.
		// Inactive (6, 11) and unrated (7) are out.
		{TopQuery{Type: "standard", List: "open"}, []int{9, 8, 5, 4, 3, 2, 12, 1}},
		{TopQuery{Type: "standard", List: "women"}, []int{8, 3}},
		{TopQuery{Type: "standard", List: "juniors"}, []int{5, 3, 12, 1}}, // born 2006+ for 2026; 9 (2005) is out
		{TopQuery{Type: "standard", List: "girls"}, []int{3}},
		{TopQuery{Type: "standard", List: "open", Country: "SWE"}, []int{5, 4, 2, 12, 1}},
		{TopQuery{Type: "blitz", List: "open"}, []int{7}},
		{TopQuery{Type: "rapid", List: "open"}, []int{1}},
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
