# mfide API

FIDE rating data from FIDE's official downloadable rating lists: player profiles, current
ratings, monthly rating history and top lists. The data is refreshed from FIDE once a day;
ratings change once a month.

This is the guide: how things behave and why. The exact contract (every endpoint, parameter,
default, limit and response field) is the OpenAPI 3.1 document:

- in the repo: [`api/openapi.json`](api/openapi.json)
- from a running server: `GET /api/openapi.json` (no key needed). It is byte for byte the repo
  file of the deployed version; `/api/health` reports its version as `apiVersion`.

Base URL: `https://<host>/api`. Data endpoints are versioned under `/v1`.

## Basics

### Keys

Every endpoint except `/health` and `/openapi.json` needs an API key in the `X-API-Key`
header:

```sh
curl -H "X-API-Key: mfide_…" https://<host>/api/v1/players/1503014
```

Keys are issued by the operator. Keep them server-side; never ship one to a browser.

### Rate limits

Each key has a per-minute limit (1,000 by default), with short bursts allowed. Requests
without a valid key are limited per client IP.

### Responses

Every response is JSON with either `data` or `error`:

```json
{"data": …}
{"error": {"code": 404, "message": "player not found"}}
```

The HTTP status is **200** for everything, including errors; the real status is
`error.code` (400 bad parameter, 404 not found, 500 server error, 503 database unreachable).
Two exceptions also use the real HTTP status, so proxies and retry logic can act on them:
**401** (missing, unknown or revoked key) and **429** (rate limit exceeded, with a
`Retry-After` header in seconds).

## Endpoints

| Endpoint | What |
| --- | --- |
| `GET /health` | Service status, data freshness and versions (no key) |
| `GET /openapi.json` | The OpenAPI document (no key) |
| `GET /key` | Your key's name and limit |
| `GET /v1/players/search` | Find players by name or FIDE id |
| `GET /v1/players/{fideid}` | One player with current ratings |
| `GET /v1/players/{fideid}/history` | A player's monthly ratings |
| `GET /v1/top` | Top lists, world or per federation |

### Players and ratings

Search, player and top-list responses share one player object (`Player` in the spec). Things
worth knowing:

- `name` is exactly as FIDE spells it; see [Names](#names).
- A rating type the player is unrated in is **`null`**, not 0.
- `active` is FIDE's activity flag **per rating type**: a player can be inactive in standard and
  active in rapid.
- `birthYear` is null for about 2% of players.
- "Current" ratings are the latest month for which all three rating types are published. On
  the 1st of a month the previous month stays current until FIDE has published all three
  lists.

### Health

For monitoring: alert on `lastSync.ok` false, or `lastSync.finishedAt` older than about two
days (the daily sync isn't running). `ratingsMonth` is the current month, `apiVersion` the
OpenAPI document's version and `build` the git revision the server was built from. If the
database is unreachable the body is an error with code 503 (HTTP status still 200).

### Search

`GET /v1/players/search?q=…` finds current players by name, or by FIDE id. Optional:
`match`, `country`, `limit`.

**Matching.** Words match in any order: "magnus carlsen" and "carlsen magnus" both find
"Carlsen, Magnus". Accents are folded the way FIDE spells names (ASCII only): "Stål" finds
"Stal", and ä/ö/ü/ø match both spellings ("Müller" finds both "Muller" and "Mueller").
Punctuation is ignored. At least one word must have two or more letters.

**`match=prefix`** (default) is for search-as-you-type: every word must start a word of the
name. Names where the *earlier* words match a whole word come first, then the highest rating
(best of standard, rapid, blitz), then name. The last word is assumed to be still being typed,
so it never counts as whole: "carls" finds Carlsen (2860) before "Carls, Stefan".
**`match=word`**: every word must be a whole word of the name, ordered by rating.

| Query | `match=prefix` (default) | `match=word` |
| --- | --- | --- |
| `wesley so` | So, Wesley; then De Sousa, Wesley …; Soon, Wesley … (10) | So, Wesley |
| `carls` | Carlsen, Magnus; Carlsson, Pontus; … | Carls, Stefan; … |
| `alvar stal` | Stal, Alvar; Alvarez Vidarte, Juan Stalin | Stal, Alvar |

**FIDE id.** If `q` is only digits, it's a FIDE id: the result is that player as a one-element
list, or an empty list if there's no such current player (or they're not in `country`). Names
are not searched.

**Type-ahead.** Debounce keystrokes (~200 ms) and start at three letters: most queries take a
few milliseconds, but a two-letter prefix common in names (`ma`, `de`, `al`) matches ~200k
players and can take ~0.3 s. Two-letter words still work (Chinese surnames like `Yu`, `Li`).

### Player and history

`GET /v1/players/{fideid}` is one current player. Unknown players, and players FIDE has removed
from its list, are a 404 (`"player not found"`).

`GET /v1/players/{fideid}/history` is the player's monthly ratings, oldest first, one entry per
month (`period` is `YYYY-MM`); a type is null in months the player wasn't rated in it. With
`type`, only that type and only the months it was rated in. History goes back up to 60 months
(the operator's retention setting). Profile fields (name, federation, titles) are not
historized: they are always today's.

### Top lists

`GET /v1/top` is a top list for the current month, in FIDE's order. Choose the rating `type`,
the `list` (open, women, juniors, girls; juniors and girls are born in the last 20 years), a
federation with `country` (world list without) and `limit`. Each entry is a player plus `rank`.

Rules (checked against FIDE's own top lists): players rated in that type and active in that
type, ordered by rating, then games played that month, then name. Ties share no rank;
`rank` is just the position.

Lists are computed from FIDE's official monthly rating list files. FIDE's website top lists
occasionally show a different rating for a player than FIDE's own files and profile pages,
which moves them a few places or off the list; mfide follows the files.

## Names

`name` is always exactly what FIDE has. FIDE names are plain ASCII (no accents: "Stal, Alvar",
"Mueller, Jan"), entered by each federation, and the format is a convention rather than a
rule. An app that wants to show names its own way ("Magnus Carlsen" instead of "Carlsen,
Magnus") needs to know what it will meet. Counts below are for the 1.94 million players in
the October 2026 list.

### Shapes

| Shape | Share | Examples |
| --- | --- | --- |
| `Last, First` | 85% (1,654k) | Carlsen, Magnus · Ding, Liren · De Ocampo, Norman · Salam-zada, Namig |
| No comma, several words | 14% (265k) | Gukesh D · Praggnanandhaa R · Erigaisi Arjun · Saleh Fahad Al Maazmi |
| `Last,First` (no space) | 0.6% (12k) | Mardeni,Yasser · El afia,Yassine |
| One word | 0.2% (4k) | Aadharsh · Baljeet |
| Several commas | 0.1% (2.6k) | Ullrich, Frank, Dr. · Altoumi, , Hani · Deng, Khor, Kuol |
| `Last. First` (period typed for comma) | 438 | Ganotisi. Osias · Giriselvam. M |
| Trailing comma | 64 | Khliustov, · Nguyen Hoang Bach, |

No-comma names come mostly from India (185k), then Kenya, Uganda, Vietnam and Pakistan. **Their
word order isn't known:** "Gukesh D" is given name first (D is an initial), "Erigaisi Arjun"
is family name first. They can't be split into first and last name reliably.

### Quirks across all shapes

| Quirk | Count | Examples |
| --- | --- | --- |
| All upper case | 8.2k | GALEON, JULIE · GALLARDO MARIN, ANGEL |
| All lower case | 1.1k | gambuzza, giovanni · galal abdullah hussein |
| Titles inside the name | 2.6k | Galfi, Tibor Dr. · Gadzhieva, Meseda Mag |
| Digits, to tell namesakes apart | 2.5k | Gajic 1965, Zoran · Gallardo Garcia, Pablo 2015 |
| Double spaces | 92 | Hagglund,  Anders |

Family-name-first cultures are stored with a comma like everyone else ("Ding, Liren",
"Hou, Yifan"); whether to show them as "Ding Liren" or "Liren Ding" is the app's choice.

### Showing "First Last"

A safe rule:

1. Collapse runs of spaces and trim.
2. Split on commas and trim the parts. If there are **exactly two non-empty parts**, show
   `<second> <first>` ("Carlsen, Magnus" → "Magnus Carlsen", "Mardeni,Yasser" → "Yasser
   Mardeni").
3. Otherwise show the name as it is. That covers no-comma names (order unknown), one-word
   names and several-comma names.

Optional tidy-ups, each with a trade-off:

- **Case:** title-case a name only if it is entirely upper or entirely lower case ("GALEON,
  JULIE" → "Julie Galeon"). Never touch mixed-case names; "McDonald" and "de la Cruz" are
  intentional.
- **Titles:** drop parts or trailing words that are titles (`Dr.`, `Dr`, `Prof.`, `Mag`,
  `Ing.`): "Ullrich, Frank, Dr." → "Frank Ullrich". Use a short fixed list; a general rule will
  eat real names.
- **Empty parts:** ignore empty comma parts ("Altoumi, , Hani" → "Hani Altoumi").
- **`Last. First`:** if there is no comma and the first word ends with a period followed by a
  space ("Ganotisi. Osias"), you can treat the period as a comma. Don't apply it to initials
  ("Promise N. Blayon").
- **Keep digits.** They separate different people with the same name.

When in doubt, show `name` unchanged: it's what FIDE shows, and what players see on their own
FIDE profile.
