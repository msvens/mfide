# mfide API

FIDE rating data from FIDE's official downloadable rating lists: player profiles, current
ratings, monthly rating history and top lists. The data is refreshed from FIDE once a day;
ratings change once a month.

Base URL: `https://<host>/api`. All data endpoints are versioned under `/v1`.

## Basics

### Keys

Every endpoint except `/health` needs an API key in the `X-API-Key` header:

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

| Status | When | Body |
| --- | --- | --- |
| 401 | Missing, unknown or revoked key | `{"error":{"code":401,"message":"missing or invalid API key"}}` |
| 429 | Rate limit exceeded | `{"error":{"code":429,…}}` plus a `Retry-After` header (seconds) |

## Endpoints

| Endpoint | What |
| --- | --- |
| [`GET /health`](#get-health) | Service status and data freshness (no key) |
| [`GET /key`](#get-key) | Your key's name and limit |
| [`GET /v1/players/search`](#get-v1playerssearch) | Find players by name or FIDE id |
| [`GET /v1/players/{fideid}`](#get-v1playersfideid) | One player with current ratings |
| [`GET /v1/players/{fideid}/history`](#get-v1playersfideidhistory) | A player's monthly ratings |
| [`GET /v1/top`](#get-v1top) | Top lists, world or per federation |

### The player object

Search, player and top-list responses share it:

```json
{
  "fideId": 1503014,
  "name": "Carlsen, Magnus",
  "country": "NOR",
  "sex": "M",
  "title": "GM",
  "womenTitle": "",
  "otherTitles": [],
  "foaTitle": "",
  "birthYear": 1990,
  "standard": {"rating": 2823, "games": 0, "k": 10, "active": true},
  "rapid": {"rating": 2801, "games": 12, "k": 10, "active": true},
  "blitz": {"rating": 2860, "games": 0, "k": 10, "active": true}
}
```

| Field | Type | Notes |
| --- | --- | --- |
| `fideId` | number | FIDE id |
| `name` | string | Exactly as FIDE spells it; see [Names](#names) |
| `country` | string | Three-letter FIDE federation code (`SWE`, `NOR`, `FID` …) |
| `sex` | string | `M` or `F` |
| `title` | string | `GM` `IM` `FM` `CM` `WGM` `WIM` `WFM` `WCM`, or `""` |
| `womenTitle` | string | `WGM` `WIM` `WFM` `WCM` held alongside `title`, or `""` |
| `otherTitles` | string[] | Arbiter, trainer and organizer titles (`IA` `FA` `NA` `FT` `FI` `FST` `DI` `IO` `AO` `SI` `NI` `LSI` `SLI`) |
| `foaTitle` | string | FIDE Online Arena title (`AGM` `AIM` `AFM` `ACM` …), or `""` |
| `birthYear` | number \| null | Null for ~2% of players |
| `standard`, `rapid`, `blitz` | object \| null | Current rating per type; **null when unrated** in that type |

A rating object:

| Field | Type | Notes |
| --- | --- | --- |
| `rating` | number | |
| `games` | number | Rated games in that month's list |
| `k` | number | K-factor |
| `active` | boolean | FIDE's activity flag, **per rating type**: a player can be inactive in standard and active in rapid |

"Current" ratings are the latest month for which all three rating types are published. On the
1st of a month the previous month stays current until FIDE has published all three lists.

### GET /health

No key needed. For monitoring.

```json
{"data":{"status":"ok","db":"ok","ratingsMonth":"2026-10","lastImport":"2026-10-24T10:26:13+02:00","lastSync":{"finishedAt":"2026-10-24T10:29:22+02:00","ok":true}}}
```

`ratingsMonth` is the current month, `lastImport` the latest file import, `lastSync` the
latest daily sync. If the database is unreachable the body is an error with code 503 (HTTP
status still 200).

### GET /key

```json
{"data":{"name":"chess-app","prefix":"Cr5RoHcu","ratePerMin":1000}}
```

### GET /v1/players/search

Finds current players by name, or by FIDE id.

| Param | Default | Notes |
| --- | --- | --- |
| `q` | (required) | Words of the name, in any order. Digits only = a FIDE id (below) |
| `match` | `prefix` | `prefix`: every word must start a word of the name. `word`: every word must be a whole word of the name |
| `country` | | Only this federation (`SWE`) |
| `limit` | 20 | 1–100 |

Returns a list of [player objects](#the-player-object), best match first.

**Matching.** Words match in any order: "magnus carlsen" and "carlsen magnus" both find
"Carlsen, Magnus". Accents are folded the way FIDE spells names (ASCII only): "Stål" finds
"Stal", and ä/ö/ü/ø match both spellings ("Müller" finds both "Muller" and "Mueller").
Punctuation is ignored. At most 5 words and 100 characters; at least one word must have two
or more letters.

**Order.** With `match=prefix`, names where the *earlier* words match a whole word come first,
then the highest rating (best of standard, rapid, blitz), then name. The last word is assumed
to be still being typed, so it never counts as whole: "carls" finds Carlsen (2860) before
"Carls, Stefan". With `match=word`, results are ordered by rating.

| Query | `match=prefix` (default) | `match=word` |
| --- | --- | --- |
| `wesley so` | So, Wesley; then De Sousa, Wesley …; Soon, Wesley … (10) | So, Wesley |
| `carls` | Carlsen, Magnus; Carlsson, Pontus; … | Carls, Stefan; … |
| `alvar stal` | Stal, Alvar; Alvarez Vidarte, Juan Stalin | Stal, Alvar |

**FIDE id.** If `q` is only digits, it's a FIDE id: the result is that player as a one-element
list, or an empty list if there's no such current player (or they're not in `country`). Names
are not searched.

**Type-ahead.** `match=prefix` is designed for search-as-you-type. Debounce keystrokes (~200
ms) and start at three letters: most queries take a few milliseconds, but a two-letter prefix
common in names (`ma`, `de`, `al`) matches ~200k players and can take ~0.3 s. Two-letter
words still work (Chinese surnames like `Yu`, `Li`).

### GET /v1/players/{fideid}

One current [player object](#the-player-object). Unknown players, and players FIDE has removed
from its list, are a 404 (`"player not found"`).

### GET /v1/players/{fideid}/history

The player's monthly ratings, oldest first.

| Param | Default | Notes |
| --- | --- | --- |
| `type` | (all) | `standard`, `rapid` or `blitz`: only that type, and only months it was rated in |

```json
{"data":[
  {"period":"2026-09","standard":null,"rapid":{"rating":2803,"games":0,"k":10,"active":true},"blitz":null},
  {"period":"2026-10","standard":null,"rapid":{"rating":2801,"games":12,"k":10,"active":true},"blitz":null}
]}
```

`period` is `YYYY-MM`. A type is null in months the player wasn't rated in it. History goes
back up to 60 months (the operator's retention setting); profile fields (name, federation, titles) are not historized. A 404
works as for the player.

### GET /v1/top

A top list for the current month, in FIDE's order.

| Param | Default | Notes |
| --- | --- | --- |
| `type` | `standard` | `standard`, `rapid` or `blitz` |
| `list` | `open` | `open`, `women`, `juniors` (born in the last 20 years: year − 20 or later), `girls` |
| `country` | | One federation's list (`SWE`); world list without |
| `limit` | 100 | 1–500 |

Each entry is a [player object](#the-player-object) plus `rank` (1-based):

```json
{"data":[{"rank":1,"fideId":1700030,"name":"Cramling, Pia","country":"SWE","sex":"F","title":"GM",…}]}
```

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
