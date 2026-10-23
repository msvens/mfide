package dao

import (
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

// schemaPlayerSearch adds what the read API needs:
//   - name_norm: the name lowercased with every run of punctuation/spaces turned
//     into one space ("Boman. Simon" -> "boman simon"). FIDE names are pure ASCII,
//     so queries are folded in Go and nothing here needs unaccent.
//   - a trigram index on it, which serves the word-prefix regexes search uses.
//   - one partial index per rating type matching the top-list rule exactly, so a
//     top list reads only as many index entries as it returns.
const schemaPlayerSearch = `
CREATE EXTENSION IF NOT EXISTS pg_trgm;
ALTER TABLE player ADD COLUMN name_norm TEXT
	GENERATED ALWAYS AS (lower(regexp_replace(name, '[^A-Za-z0-9]+', ' ', 'g'))) STORED;
CREATE INDEX player_name_trgm ON player USING gin (name_norm gin_trgm_ops);
CREATE INDEX player_top_std ON player (std_rating DESC, std_games DESC, name)
	WHERE std_rating > 0 AND removed_at IS NULL AND flag NOT LIKE '%i%';
CREATE INDEX player_top_rapid ON player (rapid_rating DESC, rapid_games DESC, name)
	WHERE rapid_rating > 0 AND removed_at IS NULL AND flag NOT LIKE '%i%';
CREATE INDEX player_top_blitz ON player (blitz_rating DESC, blitz_games DESC, name)
	WHERE blitz_rating > 0 AND removed_at IS NULL AND flag NOT LIKE '%i%';
`

// schemaPlayerTopCountry adds country-first variants of the top-list indexes.
// Without them a federation list walks the whole per-type index whenever the
// federation has fewer matching players than the limit (top women of NOR took
// 145 ms); with them it reads only that federation's players.
const schemaPlayerTopCountry = `
CREATE INDEX player_top_std_country ON player (country, std_rating DESC, std_games DESC, name)
	WHERE std_rating > 0 AND removed_at IS NULL AND flag NOT LIKE '%i%';
CREATE INDEX player_top_rapid_country ON player (country, rapid_rating DESC, rapid_games DESC, name)
	WHERE rapid_rating > 0 AND removed_at IS NULL AND flag NOT LIKE '%i%';
CREATE INDEX player_top_blitz_country ON player (country, blitz_rating DESC, blitz_games DESC, name)
	WHERE blitz_rating > 0 AND removed_at IS NULL AND flag NOT LIKE '%i%';
`

// TopQuery selects a FIDE-style top list.
type TopQuery struct {
	Type    string // standard, rapid or blitz
	List    string // open, women, juniors or girls
	Country string // optional federation, e.g. SWE
	Limit   int
	Year    int // the list year; juniors are born in Year-20 or later
}

// ratingPrefix maps a rating type to its column prefix. Column names come only
// from here, never from input.
var ratingPrefix = map[string]string{"standard": "std", "rapid": "rapid", "blitz": "blitz"}

// TopLists are the supported list variants.
var TopLists = []string{"open", "women", "juniors", "girls"}

type PlayerDAO interface {
	// Get returns a current (not removed) player, or sql.ErrNoRows.
	Get(fideid int) (*Player, error)
	// Search returns current players whose name has, for every term, a word
	// starting with one of the term's alternatives (any order). Names where more
	// terms match a whole word come first ("stal" ranks "Stal, Alvar" above
	// "Stalmach, Richard"), then the strongest players.
	Search(terms [][]string, country string, limit int) ([]Player, error)
	// Top returns a top list in FIDE's order: rating, then games, then name.
	Top(q TopQuery) ([]Player, error)
}

type PlayerPG struct {
	db *sqlx.DB
}

func NewPlayerPG(db *sqlx.DB) *PlayerPG {
	return &PlayerPG{db: db}
}

const playerSelect = "SELECT * FROM player"

func (dao *PlayerPG) Get(fideid int) (*Player, error) {
	p := Player{}
	if err := dao.db.Get(&p, playerSelect+" WHERE fideid = $1 AND removed_at IS NULL", fideid); err != nil {
		return nil, err
	}
	return &p, nil
}

func (dao *PlayerPG) Search(terms [][]string, country string, limit int) ([]Player, error) {
	if len(terms) == 0 {
		return nil, fmt.Errorf("search needs at least one term")
	}
	where := []string{"removed_at IS NULL"}
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	var exact []string
	for _, alts := range terms {
		prefix := make([]string, len(alts))
		whole := make([]string, len(alts))
		for i, a := range alts {
			// Terms are [a-z0-9] only (see server.searchTerms), so no regex escaping is needed.
			prefix[i] = "name_norm ~ " + arg("(^| )"+a)
			whole[i] = "name_norm ~ " + arg("(^| )"+a+"( |$)")
		}
		where = append(where, "("+strings.Join(prefix, " OR ")+")")
		exact = append(exact, "("+strings.Join(whole, " OR ")+")::int")
	}
	if country != "" {
		where = append(where, "country = "+arg(country))
	}
	q := playerSelect + " WHERE " + strings.Join(where, " AND ") +
		" ORDER BY " + strings.Join(exact, " + ") + " DESC, greatest(std_rating, rapid_rating, blitz_rating) DESC, name, fideid LIMIT " + arg(limit)
	players := []Player{}
	return players, dao.db.Select(&players, q, args...)
}

func (dao *PlayerPG) Top(q TopQuery) ([]Player, error) {
	p, ok := ratingPrefix[q.Type]
	if !ok {
		return nil, fmt.Errorf("unknown rating type %q", q.Type)
	}
	// This predicate must match the partial indexes in schemaPlayerSearch.
	where := []string{p + "_rating > 0", "removed_at IS NULL", "flag NOT LIKE '%i%'"}
	args := []any{}
	switch q.List {
	case "open":
	case "women":
		where = append(where, "sex = 'F'")
	case "juniors", "girls":
		args = append(args, q.Year-20)
		where = append(where, fmt.Sprintf("birth_year >= $%d", len(args)))
		if q.List == "girls" {
			where = append(where, "sex = 'F'")
		}
	default:
		return nil, fmt.Errorf("unknown list %q", q.List)
	}
	if q.Country != "" {
		args = append(args, q.Country)
		where = append(where, fmt.Sprintf("country = $%d", len(args)))
	}
	args = append(args, q.Limit)
	sql := fmt.Sprintf("%s WHERE %s ORDER BY %s_rating DESC, %s_games DESC, name LIMIT $%d",
		playerSelect, strings.Join(where, " AND "), p, p, len(args))
	players := []Player{}
	return players, dao.db.Select(&players, sql, args...)
}
