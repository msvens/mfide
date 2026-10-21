# Project Context

Go backend that imports **FIDE's official downloadable rating lists** into Postgres and serves
them as a JSON API under `/api`. It replaces the third-party ChessTools API (which mostly
scrapes FIDE's HTML) as the FIDE source for `@msvens/schack-se-sdk` and the chess app
(chess.msvens.com). **No scraping of FIDE's site, only the published download files.**

Status: skeleton (config, DB connection + migrations, `/api/health`). The domain schema and
importer are not designed yet. The POC plan and todo list live in `docs/todo.md`.
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
- API keys: `go run . key create --name <who> [--rate N]` (prints the key once) · `key list` ·
  `key revoke <id>`
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
- **Large files:** FIDE's combined XML is ~857 MB unzipped. Always stream (`encoding/xml`
  Decoder) and bulk-load (`COPY`). Never read it whole.

# Behavior Rules

- Ask before assuming when requirements are ambiguous
- Write minimum code to solve the stated problem — no preemptive abstraction
- Only modify files and functions directly involved in the current task
- Say "I'm not sure" when uncertain rather than confabulating
- Never probe FIDE's (or anyone's) production servers for security weaknesses; only download the
  published files, politely (conditional requests, no hammering)
