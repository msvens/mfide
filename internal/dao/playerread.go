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

// schemaOneRatingSource makes rating the only place for rating numbers and
// activity: player keeps the profile. The daily file's ratings were an exact
// copy of the current month's archive files, and its single flag was only the
// standard activity ('i') plus sex ('w'). The top-list indexes move to rating,
// per type and month, on each type's own activity flag; federation lists start
// from player's country index.
const schemaOneRatingSource = `
ALTER TABLE player
	DROP COLUMN flag,
	DROP COLUMN std_rating, DROP COLUMN std_games, DROP COLUMN std_k,
	DROP COLUMN rapid_rating, DROP COLUMN rapid_games, DROP COLUMN rapid_k,
	DROP COLUMN blitz_rating, DROP COLUMN blitz_games, DROP COLUMN blitz_k;
CREATE INDEX rating_top_std ON rating (period, std_rating DESC, std_games DESC)
	WHERE std_rating IS NOT NULL AND std_flag NOT LIKE '%i%';
CREATE INDEX rating_top_rapid ON rating (period, rapid_rating DESC, rapid_games DESC)
	WHERE rapid_rating IS NOT NULL AND rapid_flag NOT LIKE '%i%';
CREATE INDEX rating_top_blitz ON rating (period, blitz_rating DESC, blitz_games DESC)
	WHERE blitz_rating IS NOT NULL AND blitz_flag NOT LIKE '%i%';
CREATE INDEX player_country ON player (country) WHERE removed_at IS NULL;
`

// currentPeriodSQL is the current month: the latest one with all three rating
// types imported, so a half-imported month never makes a type look empty. It
// reads two small bookkeeping tables, not rating.
const currentPeriodSQL = `(SELECT sf.period FROM import_run ir JOIN source_file sf ON sf.id = ir.source_file_id
	WHERE sf.kind = 'archive' GROUP BY sf.period HAVING count(DISTINCT sf.rating_type) = 3
	ORDER BY sf.period DESC LIMIT 1)`

// CurrentRatings are a player's ratings in the current month; nil means unrated
// (or no month imported yet).
type CurrentRatings struct {
	StdRating   *int    `db:"std_rating"`
	StdGames    *int    `db:"std_games"`
	StdK        *int    `db:"std_k"`
	StdFlag     *string `db:"std_flag"`
	RapidRating *int    `db:"rapid_rating"`
	RapidGames  *int    `db:"rapid_games"`
	RapidK      *int    `db:"rapid_k"`
	RapidFlag   *string `db:"rapid_flag"`
	BlitzRating *int    `db:"blitz_rating"`
	BlitzGames  *int    `db:"blitz_games"`
	BlitzK      *int    `db:"blitz_k"`
	BlitzFlag   *string `db:"blitz_flag"`
}

// PlayerWithRatings is a profile with its current ratings.
type PlayerWithRatings struct {
	Player
	CurrentRatings
}

// playerWithRatings selects PlayerWithRatings from player p and rating r.
const playerWithRatings = `SELECT p.*,
	r.std_rating, r.std_games, r.std_k, r.std_flag,
	r.rapid_rating, r.rapid_games, r.rapid_k, r.rapid_flag,
	r.blitz_rating, r.blitz_games, r.blitz_k, r.blitz_flag`

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
	// Get returns a current (not removed) player with current ratings, or sql.ErrNoRows.
	Get(fideid int) (*PlayerWithRatings, error)
	// Search returns current players whose name has, for every term, a word
	// starting with one of the term's alternatives (any order). Names where more
	// terms match a whole word come first ("stal" ranks "Stal, Alvar" above
	// "Stalmach, Richard"), then the strongest players.
	Search(terms [][]string, country string, limit int) ([]PlayerWithRatings, error)
	// Top returns a current top list in FIDE's order: rating, then games, then
	// name; only players active in that type.
	Top(q TopQuery) ([]PlayerWithRatings, error)
	// History returns a player's monthly ratings, oldest first.
	History(fideid int) ([]RatingMonth, error)
}

type PlayerPG struct {
	db *sqlx.DB
}

func NewPlayerPG(db *sqlx.DB) *PlayerPG {
	return &PlayerPG{db: db}
}

const withCurrent = " FROM player p LEFT JOIN rating r ON r.fideid = p.fideid AND r.period = " + currentPeriodSQL

func (dao *PlayerPG) Get(fideid int) (*PlayerWithRatings, error) {
	p := PlayerWithRatings{}
	if err := dao.db.Get(&p, playerWithRatings+withCurrent+" WHERE p.fideid = $1 AND p.removed_at IS NULL", fideid); err != nil {
		return nil, err
	}
	return &p, nil
}

func (dao *PlayerPG) Search(terms [][]string, country string, limit int) ([]PlayerWithRatings, error) {
	if len(terms) == 0 {
		return nil, fmt.Errorf("search needs at least one term")
	}
	where := []string{"p.removed_at IS NULL"}
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
			prefix[i] = "p.name_norm ~ " + arg("(^| )"+a)
			whole[i] = "p.name_norm ~ " + arg("(^| )"+a+"( |$)")
		}
		where = append(where, "("+strings.Join(prefix, " OR ")+")")
		exact = append(exact, "("+strings.Join(whole, " OR ")+")::int")
	}
	if country != "" {
		where = append(where, "p.country = "+arg(country))
	}
	q := playerWithRatings + withCurrent + " WHERE " + strings.Join(where, " AND ") +
		" ORDER BY " + strings.Join(exact, " + ") + " DESC, coalesce(greatest(r.std_rating, r.rapid_rating, r.blitz_rating), 0) DESC, p.name, p.fideid LIMIT " + arg(limit)
	players := []PlayerWithRatings{}
	return players, dao.db.Select(&players, q, args...)
}

func (dao *PlayerPG) Top(q TopQuery) ([]PlayerWithRatings, error) {
	t, ok := ratingPrefix[q.Type]
	if !ok {
		return nil, fmt.Errorf("unknown rating type %q", q.Type)
	}
	// The rating predicates must match the partial indexes in schemaOneRatingSource.
	where := []string{"r.period = " + currentPeriodSQL, "r." + t + "_rating IS NOT NULL",
		"r." + t + "_flag NOT LIKE '%i%'", "p.removed_at IS NULL"}
	args := []any{}
	switch q.List {
	case "open":
	case "women":
		where = append(where, "p.sex = 'F'")
	case "juniors", "girls":
		args = append(args, q.Year-20)
		where = append(where, fmt.Sprintf("p.birth_year >= $%d", len(args)))
		if q.List == "girls" {
			where = append(where, "p.sex = 'F'")
		}
	default:
		return nil, fmt.Errorf("unknown list %q", q.List)
	}
	if q.Country != "" {
		args = append(args, q.Country)
		where = append(where, fmt.Sprintf("p.country = $%d", len(args)))
	}
	// A world list walks the type's top index until it has enough players, which
	// is always quick. For a federation that walk can be very slow: Postgres can't
	// know how the federation's players are spread over the world list (they're in
	// another table), so for "top 10 NOR women" it expected an early hit and read
	// 221k rows (460 ms). "+ 0" stops the index from supplying the order, so the
	// planner starts from the federation's players and sorts them: 3-80 ms for
	// every federation, by size, instead of 1-460 ms depending on the limit.
	order := "r." + t + "_rating"
	if q.Country != "" {
		order += " + 0"
	}
	args = append(args, q.Limit)
	sql := fmt.Sprintf("%s FROM rating r JOIN player p ON p.fideid = r.fideid WHERE %s ORDER BY %s DESC, r.%s_games DESC, p.name LIMIT $%d",
		playerWithRatings, strings.Join(where, " AND "), order, t, len(args))
	players := []PlayerWithRatings{}
	return players, dao.db.Select(&players, sql, args...)
}
