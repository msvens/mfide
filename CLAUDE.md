# Project Context

Go backend that imports **FIDE's official downloadable rating lists** into Postgres and serves
them as a JSON API under `/api`. It replaces the third-party ChessTools API (which mostly
scrapes FIDE's HTML) as the FIDE source for `@msvens/schack-se-sdk` and the chess app
(chess.msvens.com). **No scraping of FIDE's site, only the published download files.**

Status: POC. Profiles, ratings with history, search, top lists, API keys and a daily
self-sync with retention work; not deployed yet. The POC plan and todo list live in
`docs/todo.md`.
**Read `docs/findings.md` first:** file formats, URL patterns, the archive, verified top-list
rules, volumes and open design questions. `docs/legal-notes.md` holds parked
FIDE-permission and GDPR items that must be settled before any public launch.

# Commands

- Gate (run before committing): `make ci` # lint + build + vet + test
- Individually: `make lint` (golangci-lint) · `make build` · `make vet` · `make test`
- Run the server: `go run . service` (or bare `go run .`)
- Check the DB connection: `go run . db check`
- Create/migrate the schema: `go run . db upgrade` · show versions: `go run . db version`
  (the server refuses to start on a schema mismatch)
- API keys: `go run . key create --name <who> [--rate N]` (prints the key once) · `key list [--all]` (`--all` includes revoked) ·
  `key revoke <id>`
- Download FIDE files: `go run . fetch current` · `fetch archive --from 2026-09 [--type]` ·
  `fetch list` (into `service.root/downloads/`, recorded in `source_file`)
- Import: `go run . import current [--file <id>] [--force]` (latest downloaded current list;
  skips already-imported files) · `import runs`
- Import ratings: `go run . import archive --from 2026-05 [--to] [--type] [--force]`
  (downloaded monthly per-type archive files → `rating`; vacuums `rating` afterwards)
- Sync (fetch + import what FIDE changed, then retention): `go run . sync`. The service runs
  the same thing daily at `sync.at` (plus a catch-up at startup if the last successful sync is
  over a day old); `sync.enabled: false` turns the scheduler off, e.g. to use cron instead
- Read API (keyed), params, response fields and how FIDE names look: `API.md`; keep it in sync
  with endpoints, params and response fields
- Deploying (requirements, config, first-time setup, upgrades): `DEPLOY.md`; keep it in sync
  with config keys and commands
- DB tests need Postgres with role/db `mfide_test` (see `config_test.yaml`); they wipe that
  database and refuse any name not ending in `_test`

# Conventions (mirrors ../mphotos)

- **Routing:** stdlib `net/http.ServeMux` with method+pattern routes, all declared in
  `routes()` in `internal/server/routes.go`. Register with `mGET` (auto-prefixes
  `server.prefix`, default `/api`); write patterns without the prefix. Read path params with
  `Var(r, "name")`.
- **Handlers** return `(any, error)` and are wrapped by an adapter: `public` (`middleware.go`,
  only for `/health`) or `keyed` (`auth.go`, every data endpoint). `keyed` requires an
  `X-API-Key` header and applies a per-key rate limit; requests without a valid key are limited
  per client IP (`X-Real-IP` is trusted only from loopback, i.e. our nginx).
- **Responses** are always transport-200 with a `{data}` / `{error}` envelope; the real status
  lives in `error.code`. **Exception:** 401 and 429 also use the real HTTP status (429 with
  `Retry-After`) so proxies and clients can act on them. Never return `(nil, nil)`, which yields a 500. Use the constructors in
  `errors.go`; `ResolveError` maps `sql.ErrNoRows` → 404.
- **DAO** (`internal/dao/`): `sqlx` over `pgx/v5`, no ORM. Follow mphotos' DAO pattern (one
  interface per entity on `PGDB`) when tables arrive. Schema changes are append-only steps in
  `migrations` (`internal/dao/migrate.go`); there is no separate "latest schema", so a fresh DB
  walks every step. Each step runs in one transaction with its version stamp.
- **Config:** viper + YAML through free functions in `internal/config`. Searched in
  `$HOME/.mfide`, `/etc/mfide`, `.`; `MFIDE_*` env vars override (e.g. `MFIDE_DB_HOST`).
  `config_example.yaml` documents every key and is asserted by `config_test.go`, so keep them in
  sync.
- **Logging:** zap, structured. No `fmt.Println` / `log.Printf` in the server.
- **Downloads** (`internal/fide`): conditional GETs only, one file at a time with
  `fide.fetchDelay` between, mfide User-Agent. `source_file` holds one row per distinct content;
  a recheck that finds the same file only bumps `checked_at`. Stored files are never
  overwritten (a changed file gets a `.<sha8>` suffix). The current month's archive file is
  provisional until the month ends.
- **Search:** FIDE names are pure ASCII, so queries are folded in Go (`server/search.go`;
  ä/ö/ü/ø match both "o" and "oe") and matched as word prefixes (`match=word`: whole words) on
  `player.name_norm` with a pg_trgm index (the extension is created by migration 4; needs
  Postgres ≥ 13). Ranking: whole-word matches of all but the last word (still being typed),
  then rating. A digits-only `q` is a FIDE-id lookup. Never fold or rewrite the stored name;
  display help for names is documentation (API.md "Names"), not API fields.
- **Two tables, no duplication:** `player` is the profile only (name, country, sex, titles,
  birth year), from the daily file; its ratings and flag are ignored (they're an exact copy of
  the current month's archive files). `rating` is the only place for rating numbers: one row per
  player and month, std/rapid/blitz rating, games, k and flag as columns (NULL = unrated; FIDE's
  activity flag is per type). Name, country etc. are never historized.
- **Current month** = the latest month with all three types imported (`currentPeriodSQL`, read
  from `import_run` + `source_file`). Reads join `player` with `rating` at that month.
- **Archive import:** each file upserts only its type's columns and writes only differences, so
  re-importing an unchanged file writes nothing; `import archive` vacuums `rating` afterwards.
- **Top lists:** current month, type rated and that type's flag without `i`, ORDER BY rating
  DESC, games DESC, player name ASC (verified against FIDE); juniors = born ≥ year − 20. The
  rating predicates must match the partial indexes in `dao/playerread.go`. Federation lists
  sort on `rating + 0` on purpose, so the planner starts from the federation's players instead
  of walking the world list (which took 460 ms for "top 10 NOR women"); see the comment in
  `Top`.
- **Custom plans:** connections set `plan_cache_mode=force_custom_plan` (DSN in `dao.go`).
  pgx reuses prepared statements, and Postgres' generic plan after 5 runs made country top lists
  ~150× slower. Keep it unless you re-measure.
- **Sync** (`internal/syncer`): one run = current list (fetch, import if new), last and this
  month's archive files (a 404 for this month = not published yet), purge players removed longer
  than `retention.removedPlayersDays` with their ratings (plus ratings older than that of ids
  with no player row), cap `rating` at `retention.historyMonths`, prune downloads (archive zips
  once imported, all but the newest `retention.currentFiles` imported current lists; rows stay
  with `deleted_at`), vacuum if `rating` changed. A Postgres advisory lock keeps runs from
  overlapping; runs are recorded in `sync_run` and `/api/health` reports the last one. The
  fetch/import commands share their code with sync.
- **Large files:** FIDE's combined XML is ~858 MB unzipped. Rows flow zip → `xml.Decoder` →
  pgx `COPY` into a temp staging table, then SQL diffs into `player`, in one transaction. Never
  read the file or all rows into memory (the import peaks at ~25 MB). An import that would leave
  under 90% of the current players is refused (truncated-file guard).

# Behavior Rules

- Ask before assuming when requirements are ambiguous
- Write minimum code to solve the stated problem — no preemptive abstraction
- Only modify files and functions directly involved in the current task
- Say "I'm not sure" when uncertain rather than confabulating
- Never probe FIDE's (or anyone's) production servers for security weaknesses; only download the
  published files, politely (conditional requests, no hammering)
