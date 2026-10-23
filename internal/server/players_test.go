package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/msvens/mfide/internal/dao"
	"go.uber.org/zap"
)

// fakePlayers records the last query and returns canned players.
type fakePlayers struct {
	players     []dao.PlayerWithRatings
	lastTerms   [][]string
	lastCountry string
	lastLimit   int
	lastTop     dao.TopQuery
	history     []dao.RatingMonth
}

func (f *fakePlayers) Get(id int) (*dao.PlayerWithRatings, error) {
	for i := range f.players {
		if f.players[i].FideID == id {
			return &f.players[i], nil
		}
	}
	return nil, sql.ErrNoRows
}

func (f *fakePlayers) Search(terms [][]string, country string, limit int) ([]dao.PlayerWithRatings, error) {
	f.lastTerms, f.lastCountry, f.lastLimit = terms, country, limit
	return f.players, nil
}

func (f *fakePlayers) History(id int) ([]dao.RatingMonth, error) {
	return f.history, nil
}

func (f *fakePlayers) Top(q dao.TopQuery) ([]dao.PlayerWithRatings, error) {
	f.lastTop = q
	return f.players, nil
}

func playersServer() (*mserver, *fakePlayers) {
	by := 2013
	n := func(v int) *int { return &v }
	f := func(v string) *string { return &v }
	fp := &fakePlayers{players: []dao.PlayerWithRatings{
		{Player: dao.Player{FideID: 1786644, Name: "Stal, Alvar", Country: "SWE", Sex: "M", BirthYear: &by},
			CurrentRatings: dao.CurrentRatings{StdRating: n(1628), StdGames: n(0), StdK: n(40), StdFlag: f(""),
				RapidRating: n(1708), RapidGames: n(3), RapidK: n(40), RapidFlag: f("")}},
		// Inactive in standard, active in blitz: activity is per type.
		{Player: dao.Player{FideID: 9000002, Name: "Example, Bertil", Country: "SWE", Sex: "M", OTitle: "FT,NA"},
			CurrentRatings: dao.CurrentRatings{StdRating: n(2450), StdGames: n(0), StdK: n(20), StdFlag: f("i"),
				BlitzRating: n(2300), BlitzGames: n(5), BlitzK: n(20), BlitzFlag: f("")}},
	}}
	return newServer("/api", nil, newAuth(defaultKeys(), time.Minute, 600), fp, zap.NewNop().Sugar()), fp
}

// getData does a keyed GET and decodes data or error.code.
func getData(t *testing.T, s *mserver, path string, data any) int {
	t.Helper()
	rec := get(s, path, goodKey)
	var resp struct {
		Data json.RawMessage `json:"data"`
		Err  *ApiError       `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("%s: decode: %v", path, err)
	}
	if resp.Err != nil {
		return resp.Err.Code
	}
	if err := json.Unmarshal(resp.Data, data); err != nil {
		t.Fatalf("%s: decode data: %v", path, err)
	}
	return 0
}

func TestPlayerJSON(t *testing.T) {
	s, _ := playersServer()
	var p map[string]any
	if code := getData(t, s, "/api/v1/players/1786644", &p); code != 0 {
		t.Fatalf("error %d", code)
	}
	std := p["standard"].(map[string]any)
	if std["rating"].(float64) != 1628 || std["active"] != true || p["blitz"] != nil {
		t.Errorf("player %v", p)
	}
	if _, ok := p["active"]; ok {
		t.Error("activity is per rating type; there should be no top-level active")
	}

	var bertil playerJSON
	getData(t, s, "/api/v1/players/9000002", &bertil)
	if !reflect.DeepEqual(bertil.OtherTitles, []string{"FT", "NA"}) || bertil.Standard.Active || !bertil.Blitz.Active || bertil.Rapid != nil {
		t.Errorf("bertil %+v", bertil)
	}

	if code := getData(t, s, "/api/v1/players/123", &p); code != http.StatusNotFound {
		t.Errorf("unknown player: code %d, want 404", code)
	}
	if code := getData(t, s, "/api/v1/players/abc", &p); code != http.StatusBadRequest {
		t.Errorf("non-numeric id: code %d, want 400", code)
	}
}

func TestSearchHandler(t *testing.T) {
	s, fp := playersServer()
	var out []playerJSON
	if code := getData(t, s, "/api/v1/players/search?q="+url.QueryEscape("Stål alvar")+"&country=swe&limit=5", &out); code != 0 {
		t.Fatalf("error %d", code)
	}
	if !reflect.DeepEqual(fp.lastTerms, [][]string{{"stal"}, {"alvar"}}) || fp.lastCountry != "SWE" || fp.lastLimit != 5 {
		t.Errorf("store got terms %v, country %q, limit %d", fp.lastTerms, fp.lastCountry, fp.lastLimit)
	}
	if len(out) != 2 || out[0].Name != "Stal, Alvar" {
		t.Errorf("out %+v", out)
	}

	for _, q := range []string{"q=", "q=x", "q=stal&country=SWEDEN", "q=stal&limit=0", "q=stal&limit=101"} {
		if code := getData(t, s, "/api/v1/players/search?"+q, &out); code != http.StatusBadRequest {
			t.Errorf("%s: code %d, want 400", q, code)
		}
	}
}

func TestTopHandler(t *testing.T) {
	s, fp := playersServer()
	var out []topJSON
	if code := getData(t, s, "/api/v1/top", &out); code != 0 {
		t.Fatalf("error %d", code)
	}
	if fp.lastTop.Type != "standard" || fp.lastTop.List != "open" || fp.lastTop.Limit != 100 || fp.lastTop.Year != time.Now().Year() {
		t.Errorf("defaults: %+v", fp.lastTop)
	}
	if len(out) != 2 || out[0].Rank != 1 || out[1].Rank != 2 || out[0].FideID != 1786644 {
		t.Errorf("ranks: %+v", out)
	}

	getData(t, s, "/api/v1/top?type=blitz&list=girls&country=nor&limit=10", &out)
	if fp.lastTop != (dao.TopQuery{Type: "blitz", List: "girls", Country: "NOR", Limit: 10, Year: time.Now().Year()}) {
		t.Errorf("params: %+v", fp.lastTop)
	}

	for _, q := range []string{"type=bullet", "list=seniors", "limit=501", "country=S"} {
		if code := getData(t, s, "/api/v1/top?"+q, &out); code != http.StatusBadRequest {
			t.Errorf("%s: code %d, want 400", q, code)
		}
	}
}

func TestHistoryHandler(t *testing.T) {
	s, fp := playersServer()
	n := func(v int) *int { return &v }
	sep := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	oct := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	fp.history = []dao.RatingMonth{
		{Period: sep, StdRating: n(1600), StdGames: n(2), StdK: n(40)},
		{Period: oct, StdRating: n(1628), StdGames: n(4), StdK: n(40), BlitzRating: n(1597), BlitzGames: n(9), BlitzK: n(40)},
	}
	var out []monthJSON
	if code := getData(t, s, "/api/v1/players/1786644/history", &out); code != 0 {
		t.Fatalf("error %d", code)
	}
	if len(out) != 2 || out[0].Period != "2026-09" || out[0].Standard.Rating != 1600 || out[0].Blitz != nil ||
		out[1].Period != "2026-10" || out[1].Blitz.Rating != 1597 || out[1].Rapid != nil {
		t.Errorf("history %+v", out)
	}

	// ?type= keeps only that type, and only the months it was rated in.
	getData(t, s, "/api/v1/players/1786644/history?type=blitz", &out)
	if len(out) != 1 || out[0].Period != "2026-10" || out[0].Standard != nil || out[0].Blitz.Rating != 1597 {
		t.Errorf("blitz only %+v", out)
	}
	if code := getData(t, s, "/api/v1/players/1786644/history?type=bullet", &out); code != http.StatusBadRequest {
		t.Errorf("bad type: code %d, want 400", code)
	}
	if code := getData(t, s, "/api/v1/players/123/history", &out); code != http.StatusNotFound {
		t.Errorf("unknown player: code %d, want 404", code)
	}

	fp.history = nil
	var empty []monthJSON
	if code := getData(t, s, "/api/v1/players/1786644/history", &empty); code != 0 || empty == nil || len(empty) != 0 {
		t.Errorf("no history: code %d, data %v; want an empty list", code, empty)
	}
}

func TestReadAPIRequiresKey(t *testing.T) {
	s, _ := playersServer()
	for _, path := range []string{"/api/v1/players/search?q=stal", "/api/v1/players/1786644", "/api/v1/players/1786644/history", "/api/v1/top"} {
		if rec := get(s, path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without key: status %d, want 401", path, rec.Code)
		}
	}
}
