# mfide

FIDE rating data from FIDE's official downloadable rating lists, served as a JSON API.

Status: proof of concept, not deployed. See [docs/findings.md](docs/findings.md) for the background and design
questions, and [CLAUDE.md](CLAUDE.md) for commands and conventions.

```sh
cp config_example.yaml ~/.mfide/config.yaml   # edit db settings
go run . db check
go run . db upgrade
go run .                                      # GET http://localhost:8051/api/health
```

The service keeps itself up to date: every day at `sync.at` (default 03:00, local time) it
fetches FIDE's files, imports what changed and applies the `retention` settings (see
`config_example.yaml`). `go run . sync` does one such run by hand.
