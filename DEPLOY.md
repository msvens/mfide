# Deploying mfide

What a host needs to run mfide, the first-time setup, and how to upgrade. Host specifics
(nginx site, systemd unit, TLS, backups) live with the host's own config, not here.

## What mfide needs

| Need | Details |
| --- | --- |
| Binary | `go build -o mfide .` (Go 1.25+); one static-ish binary, no other files at runtime |
| Postgres | ≥ 13 (for `pg_trgm` as a trusted extension). One database and one role that **owns** it: migrations create tables and the `pg_trgm` extension, sync runs `VACUUM` on `rating`, imports use temp tables |
| Disk, database | ~0.3 GB for `player` + ~60 MB per month of rating history (~0.4 GB at 6 months, ~3.8 GB at the 60-month cap) |
| Disk, `service.root` | Writable by the service user. Downloads land in `downloads/`: the 2 newest current lists (~100 MB) plus archive zips briefly (~35 MB per month, deleted once imported) |
| Memory | Small: an import peaks at ~25 MB |
| Network out | HTTPS to `ratings.fide.com` (conditional GETs, one file at a time, once a day) |
| Network in | HTTP on `server.host:server.port` (default `localhost:8051`). Bind to localhost and put nginx in front |

## Configuration

`config.yaml` is read from `$HOME/.mfide`, `/etc/mfide` or the working directory, in that
order. Every key can be overridden by an environment variable: `MFIDE_` + the key in upper
case with `.` as `_` (`MFIDE_DB_PASSWORD`, `MFIDE_SYNC_AT`). `config_example.yaml` documents
every key; the ones that matter on a server:

| Key | Default | Notes |
| --- | --- | --- |
| `server.host` / `server.port` | `localhost` / `8051` | Keep it on localhost |
| `server.prefix` | `/api` | All routes live under it |
| `db.host`, `db.port`, `db.user`, `db.password`, `db.name` | | Leave `db.password` empty for peer/trust auth |
| `service.root` | `.server` | Use an absolute path on a server, e.g. `/var/lib/mfide` |
| `sync.enabled` / `sync.at` | `true` / `"03:00"` | Daily sync inside the service, **server-local time**. FIDE regenerates its files around 21:00 UTC, so any time in the night works |
| `retention.currentFiles` | `2` | Current-list zips kept on disk |
| `retention.removedPlayersDays` | `90` | Players FIDE removed are deleted, with their history, after this |
| `retention.historyMonths` | `60` | Months of ratings kept; `0` keeps everything |
| `api.defaultRatePerMin` | `1000` | Default limit for new keys |
| `api.ipRatePerMin` | `60` | Requests without a valid key, per client IP |

## Behind nginx

- Proxy `/api/` to `http://127.0.0.1:8051` and set `X-Real-IP $remote_addr`. mfide trusts
  `X-Real-IP` only from a loopback peer, and uses it for the per-IP limit on requests without a
  key.
- Clients send their key in the `X-API-Key` header; pass it through.
- Responses are HTTP 200 with a `{data}` / `{error}` body, except **401** (missing or bad key)
  and **429** (rate limited, with `Retry-After`), which use the real status.

## Monitoring

`GET /api/health` is public and cheap:

```json
{"data":{"status":"ok","db":"ok","ratingsMonth":"2026-10","lastImport":"2026-10-24T03:00:27+02:00","lastSync":{"finishedAt":"2026-10-24T03:00:41+02:00","ok":true}}}
```

An unreachable database comes back as HTTP 200 with an `{error}` body (code 503), so check
the body, not just the status. Worth alerting on: `lastSync.ok` false, or `lastSync.finishedAt`
older than ~2 days (the sync isn't running).

Logs go to stderr (zap's development format for now).

## First-time setup

```sh
./mfide db check                              # connects?
./mfide db upgrade                            # creates the whole schema
./mfide fetch current && ./mfide import current            # profiles, ~30 s
./mfide fetch archive --from 2026-05                       # 6 months of ratings, 3 s between files
./mfide import archive --from 2026-05                      # ~1 min, vacuums afterwards
./mfide key create --name chess-app           # prints the key once; store it in the app's env
./mfide service                               # or start the systemd unit
```

Pick `--from` as six months back from the current month. After that the service's daily sync
keeps everything current: new monthly lists, profile changes, pruning and retention.

The service stops cleanly on SIGTERM. A sync in progress gets up to 10 s to reach a step
boundary; an import cut short rolls back and the next start catches up.

## Upgrading

The server refuses to start against a database whose schema version doesn't match the
binary, so a schema change always goes:

```sh
pg_dump ... > mfide-pre-upgrade.sql   # habit before any schema change
# stop the service, deploy the new binary
./mfide db upgrade
./mfide db version                    # binary and database versions match
# start the service
```

`db upgrade` walks every step the database is missing, each in its own transaction.
