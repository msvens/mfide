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
	players     []dao.Player
	lastTerms   [][]string
	lastCountry string
	lastLimit   int
	lastTop     dao.TopQuery
}

func (f *fakePlayers) Get(id int) (*dao.Player, error) {
	for i := range f.players {
		if f.players[i].FideID == id {
			return &f.players[i], nil
		}
	}
	return nil, sql.ErrNoRows
}

func (f *fakePlayers) Search(terms [][]string, country string, limit int) ([]dao.Player, error) {
	f.lastTerms, f.lastCountry, f.lastLimit = terms, country, limit
	return f.players, nil
}

func (f *fakePlayers) Top(q dao.TopQuery) ([]dao.Player, error) {
	f.lastTop = q
	return f.players, nil
}

func playersServer() (*mserver, *fakePlayers) {
	by := 2013
	fp := &fakePlayers{players: []dao.Player{
		{FideID: 1786644, Name: "Stal, Alvar", Country: "SWE", Sex: "M", BirthYear: &by,
			StdRating: 1628, StdK: 40, RapidRating: 1708, RapidGames: 3, RapidK: 40},
		{FideID: 9000002, Name: "Example, Bertil", Country: "SWE", Sex: "M", OTitle: "FT,NA", Flag: "i",
			StdRating: 2450, StdK: 20},
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
	if p["standard"].(map[string]any)["rating"].(float64) != 1628 || p["blitz"] != nil || p["active"] != true {
		t.Errorf("player %v", p)
	}

	var bertil playerJSON
	getData(t, s, "/api/v1/players/9000002", &bertil)
	if !reflect.DeepEqual(bertil.OtherTitles, []string{"FT", "NA"}) || bertil.Active || bertil.Rapid != nil {
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

func TestReadAPIRequiresKey(t *testing.T) {
	s, _ := playersServer()
	for _, path := range []string{"/api/v1/players/search?q=stal", "/api/v1/players/1786644", "/api/v1/top"} {
		if rec := get(s, path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without key: status %d, want 401", path, rec.Code)
		}
	}
}
