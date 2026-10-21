# Project Context

Go backend that imports **FIDE's official downloadable rating lists** into Postgres and serves
them as a JSON API under `/api`. It replaces the third-party ChessTools API (which mostly
scrapes FIDE's HTML) as the FIDE source for `@msvens/schack-se-sdk` and the chess app
(chess.msvens.com). **No scraping of FIDE's site, only the published download files.**

Status: bare skeleton (config, DB connection, `/api/health`). The schema and importer are not
designed yet. **Read `docs/findings.md` first:** file formats, URL patterns, the archive,
verified top-list rules, volumes and open design questions. `docs/legal-notes.md` holds parked
FIDE-permission and GDPR items that must be settled before any public launch.

# Commands

- Gate (run before committing): `make ci` # lint + build + vet + test
- Individually: `make lint` (golangci-lint) · `make build` · `make vet` · `make test`
- Run the server: `go run . service` (or bare `go run .`)
- Check the DB connection: `go run . db check`

# Conventions (mirrors ../mphotos)

- **Routing:** stdlib `net/http.ServeMux` with method+pattern routes, all declared in
  `routes()` in `internal/server/routes.go`. Register with `mGET` (auto-prefixes
  `server.prefix`, default `/api`); write patterns without the prefix. Read path params with
  `Var(r, "name")`.
- **Handlers** return `(any, error)` and are wrapped by an adapter in `middleware.go`
  (`public` for now).
- **Responses** are always transport-200 with a `{data}` / `{error}` envelope; the real status
  lives in `error.code`. Never return `(nil, nil)`, which yields a 500. Use the constructors in
  `errors.go`; `ResolveError` maps `sql.ErrNoRows` → 404.
- **DAO** (`internal/dao/`): `sqlx` over `pgx/v5`, no ORM. Follow mphotos' DAO pattern (one
  interface per entity on `PGDB`, hand-rolled versioned migrations) when tables arrive.
- **Config:** viper + YAML through free functions in `internal/config`. Searched in
  `$HOME/.mfide`, `/etc/mfide`, `.`; `MFIDE_*` env vars override (e.g. `MFIDE_DB_HOST`).
  `config_example.yaml` documents every key and is asserted by `config_test.go`, so keep them in
  sync.
- **Logging:** zap, structured. No `fmt.Println` / `log.Printf` in the server.
- **Large files:** FIDE's combined XML is ~857 MB unzipped. Always stream (`encoding/xml`
  Decoder) and bulk-load (`COPY`). Never read it whole.

# Behavior Rules

- Ask before assuming when requirements are ambiguous
- Write minimum code to solve the stated problem — no preemptive abstraction
- Only modify files and functions directly involved in the current task
- Say "I'm not sure" when uncertain rather than confabulating
- Never probe FIDE's (or anyone's) production servers for security weaknesses; only download the
  published files, politely (conditional requests, no hammering)
