package server

import (
	"database/sql"
	"errors"
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
	Get(fideid int) (*dao.PlayerWithRatings, error)
	Search(terms [][]string, wholeWords bool, country string, limit int) ([]dao.PlayerWithRatings, error)
	Top(q dao.TopQuery) ([]dao.PlayerWithRatings, error)
	History(fideid int) ([]dao.RatingMonth, error)
}

type ratingJSON struct {
	Rating int  `json:"rating"`
	Games  int  `json:"games"`
	K      int  `json:"k"`
	Active bool `json:"active"` // FIDE's activity is per rating type
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
	Standard    *ratingJSON `json:"standard"` // null when unrated
	Rapid       *ratingJSON `json:"rapid"`
	Blitz       *ratingJSON `json:"blitz"`
}

type topJSON struct {
	Rank int `json:"rank"`
	playerJSON
}

// rating is one type's rating, or nil when the player isn't rated in it.
func rating(r, games, k *int, flag *string) *ratingJSON {
	if r == nil {
		return nil
	}
	return &ratingJSON{Rating: *r, Games: *games, K: *k, Active: flag == nil || !strings.Contains(*flag, "i")}
}

func toJSON(p *dao.PlayerWithRatings) playerJSON {
	other := []string{}
	for _, t := range strings.Split(p.OTitle, ",") {
		if t = strings.TrimSpace(t); t != "" {
			other = append(other, t)
		}
	}
	return playerJSON{
		FideID: p.FideID, Name: p.Name, Country: p.Country, Sex: p.Sex,
		Title: p.Title, WomenTitle: p.WTitle, OtherTitles: other, FoaTitle: p.FoaTitle,
		BirthYear: p.BirthYear,
		Standard:  rating(p.StdRating, p.StdGames, p.StdK, p.StdFlag),
		Rapid:     rating(p.RapidRating, p.RapidGames, p.RapidK, p.RapidFlag),
		Blitz:     rating(p.BlitzRating, p.BlitzGames, p.BlitzK, p.BlitzFlag),
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

var digitsOnly = regexp.MustCompile(`^[0-9]+$`)

// handleSearch finds players by name. Words match as word prefixes by default,
// for type-ahead; match=word wants whole words. A query of digits only is a
// FIDE id: that player, or an empty list.
func (s *mserver) handleSearch(r *http.Request) (any, error) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	country, err := countryParam(r)
	if err != nil {
		return nil, err
	}
	limit, err := limitParam(r, 20, 100)
	if err != nil {
		return nil, err
	}
	var wholeWords bool
	switch m := r.URL.Query().Get("match"); m {
	case "", "prefix":
	case "word":
		wholeWords = true
	default:
		return nil, BadRequestError("match must be prefix or word")
	}
	if digitsOnly.MatchString(q) {
		return s.searchID(q, country)
	}
	terms, err := searchTerms(q)
	if err != nil {
		return nil, err
	}
	players, err := s.players.Search(terms, wholeWords, country, limit)
	if err != nil {
		return nil, err
	}
	out := make([]playerJSON, len(players))
	for i := range players {
		out[i] = toJSON(&players[i])
	}
	return out, nil
}

// searchID is a search for a FIDE id: a list of that player, or an empty one if
// the id is unknown or the player isn't in country.
func (s *mserver) searchID(q, country string) (any, error) {
	out := []playerJSON{}
	id, err := strconv.Atoi(q)
	if err != nil || id <= 0 {
		return out, nil // too many digits for an id
	}
	p, err := s.players.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if country == "" || p.Country == country {
		out = append(out, toJSON(p))
	}
	return out, nil
}

func (s *mserver) handlePlayer(r *http.Request) (any, error) {
	id, err := strconv.Atoi(Var(r, "fideid"))
	if err != nil || id <= 0 {
		return nil, BadRequestError("fideid must be a positive number")
	}
	p, err := s.getPlayer(id)
	if err != nil {
		return nil, err
	}
	return toJSON(p), nil
}

// getPlayer is players.Get with a 404 that doesn't leak the database error.
func (s *mserver) getPlayer(id int) (*dao.PlayerWithRatings, error) {
	p, err := s.players.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NotFoundError("player not found")
	}
	return p, err
}

type monthJSON struct {
	Period   string      `json:"period"` // YYYY-MM
	Standard *ratingJSON `json:"standard"`
	Rapid    *ratingJSON `json:"rapid"`
	Blitz    *ratingJSON `json:"blitz"`
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
	if _, err := s.getPlayer(id); err != nil {
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
			j.Standard = rating(m.StdRating, m.StdGames, m.StdK, m.StdFlag)
		}
		if typ == "" || typ == "rapid" {
			j.Rapid = rating(m.RapidRating, m.RapidGames, m.RapidK, m.RapidFlag)
		}
		if typ == "" || typ == "blitz" {
			j.Blitz = rating(m.BlitzRating, m.BlitzGames, m.BlitzK, m.BlitzFlag)
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
