package server

import (
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/msvens/mfide/internal/dao"
)

// playerStore is the part of dao.PlayerDAO the handlers need; tests fake it.
type playerStore interface {
	Get(fideid int) (*dao.Player, error)
	Search(terms [][]string, country string, limit int) ([]dao.Player, error)
	Top(q dao.TopQuery) ([]dao.Player, error)
	History(fideid int) ([]dao.RatingMonth, error)
}

type ratingJSON struct {
	Rating int `json:"rating"`
	Games  int `json:"games"`
	K      int `json:"k"`
}

type playerJSON struct {
	FideID      int         `json:"fideId"`
	Name        string      `json:"name"` // as FIDE spells it, usually "Last, First"
	Country     string      `json:"country"`
	Sex         string      `json:"sex"`
	Title       string      `json:"title"`
	WomenTitle  string      `json:"womenTitle"`
	OtherTitles []string    `json:"otherTitles"`
	FoaTitle    string      `json:"foaTitle"`
	BirthYear   *int        `json:"birthYear"`
	Active      bool        `json:"active"`
	Standard    *ratingJSON `json:"standard"` // null when unrated
	Rapid       *ratingJSON `json:"rapid"`
	Blitz       *ratingJSON `json:"blitz"`
}

type topJSON struct {
	Rank int `json:"rank"`
	playerJSON
}

func rating(r, games, k int) *ratingJSON {
	if r == 0 {
		return nil
	}
	return &ratingJSON{Rating: r, Games: games, K: k}
}

func toJSON(p *dao.Player) playerJSON {
	other := []string{}
	for _, t := range strings.Split(p.OTitle, ",") {
		if t = strings.TrimSpace(t); t != "" {
			other = append(other, t)
		}
	}
	return playerJSON{
		FideID: p.FideID, Name: p.Name, Country: p.Country, Sex: p.Sex,
		Title: p.Title, WomenTitle: p.WTitle, OtherTitles: other, FoaTitle: p.FoaTitle,
		BirthYear: p.BirthYear, Active: !strings.Contains(p.Flag, "i"),
		Standard: rating(p.StdRating, p.StdGames, p.StdK),
		Rapid:    rating(p.RapidRating, p.RapidGames, p.RapidK),
		Blitz:    rating(p.BlitzRating, p.BlitzGames, p.BlitzK),
	}
}

var countryCode = regexp.MustCompile(`^[A-Za-z]{3}$`)

func countryParam(r *http.Request) (string, error) {
	c := r.URL.Query().Get("country")
	if c == "" {
		return "", nil
	}
	if !countryCode.MatchString(c) {
		return "", BadRequestError("country must be a three-letter FIDE federation code, e.g. SWE")
	}
	return strings.ToUpper(c), nil
}

func limitParam(r *http.Request, def, max int) (int, error) {
	s := r.URL.Query().Get("limit")
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > max {
		return 0, BadRequestError("limit must be between 1 and " + strconv.Itoa(max))
	}
	return n, nil
}

func (s *mserver) handleSearch(r *http.Request) (any, error) {
	terms, err := searchTerms(r.URL.Query().Get("q"))
	if err != nil {
		return nil, err
	}
	country, err := countryParam(r)
	if err != nil {
		return nil, err
	}
	limit, err := limitParam(r, 20, 100)
	if err != nil {
		return nil, err
	}
	players, err := s.players.Search(terms, country, limit)
	if err != nil {
		return nil, err
	}
	out := make([]playerJSON, len(players))
	for i := range players {
		out[i] = toJSON(&players[i])
	}
	return out, nil
}

func (s *mserver) handlePlayer(r *http.Request) (any, error) {
	id, err := strconv.Atoi(Var(r, "fideid"))
	if err != nil || id <= 0 {
		return nil, BadRequestError("fideid must be a positive number")
	}
	p, err := s.players.Get(id)
	if err != nil {
		return nil, err // sql.ErrNoRows becomes 404
	}
	return toJSON(p), nil
}

type monthJSON struct {
	Period   string      `json:"period"` // YYYY-MM
	Standard *ratingJSON `json:"standard"`
	Rapid    *ratingJSON `json:"rapid"`
	Blitz    *ratingJSON `json:"blitz"`
}

func monthRating(r, games, k *int) *ratingJSON {
	if r == nil {
		return nil
	}
	return &ratingJSON{Rating: *r, Games: *games, K: *k}
}

// handleHistory returns a player's monthly ratings, oldest first. A type the
// player wasn't rated in that month is null; with ?type= only that type is
// returned, and only the months it was rated in.
func (s *mserver) handleHistory(r *http.Request) (any, error) {
	id, err := strconv.Atoi(Var(r, "fideid"))
	if err != nil || id <= 0 {
		return nil, BadRequestError("fideid must be a positive number")
	}
	typ := r.URL.Query().Get("type")
	if typ != "" && typ != "standard" && typ != "rapid" && typ != "blitz" {
		return nil, BadRequestError("type must be standard, rapid or blitz")
	}
	// Same visibility as the player card: unknown or removed players are 404.
	if _, err := s.players.Get(id); err != nil {
		return nil, err
	}
	months, err := s.players.History(id)
	if err != nil {
		return nil, err
	}
	out := []monthJSON{}
	for _, m := range months {
		j := monthJSON{Period: m.Period.Format("2006-01")}
		if typ == "" || typ == "standard" {
			j.Standard = monthRating(m.StdRating, m.StdGames, m.StdK)
		}
		if typ == "" || typ == "rapid" {
			j.Rapid = monthRating(m.RapidRating, m.RapidGames, m.RapidK)
		}
		if typ == "" || typ == "blitz" {
			j.Blitz = monthRating(m.BlitzRating, m.BlitzGames, m.BlitzK)
		}
		if j.Standard != nil || j.Rapid != nil || j.Blitz != nil {
			out = append(out, j)
		}
	}
	return out, nil
}

func (s *mserver) handleTop(r *http.Request) (any, error) {
	q := dao.TopQuery{Type: "standard", List: "open", Year: time.Now().Year()}
	if t := r.URL.Query().Get("type"); t != "" {
		q.Type = t
	}
	if l := r.URL.Query().Get("list"); l != "" {
		q.List = l
	}
	if q.Type != "standard" && q.Type != "rapid" && q.Type != "blitz" {
		return nil, BadRequestError("type must be standard, rapid or blitz")
	}
	if !slices.Contains(dao.TopLists, q.List) {
		return nil, BadRequestError("list must be one of " + strings.Join(dao.TopLists, ", "))
	}
	var err error
	if q.Country, err = countryParam(r); err != nil {
		return nil, err
	}
	if q.Limit, err = limitParam(r, 100, 500); err != nil {
		return nil, err
	}
	players, err := s.players.Top(q)
	if err != nil {
		return nil, err
	}
	out := make([]topJSON, len(players))
	for i := range players {
		out[i] = topJSON{Rank: i + 1, playerJSON: toJSON(&players[i])}
	}
	return out, nil
}
