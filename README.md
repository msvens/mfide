# mfide

FIDE rating data from FIDE's official downloadable rating lists, served as a JSON API.

Status: skeleton. See [docs/findings.md](docs/findings.md) for the background and design
questions, and [CLAUDE.md](CLAUDE.md) for commands and conventions.

```sh
cp config_example.yaml ~/.mfide/config.yaml   # edit db settings
go run . db check
go run .                                      # GET http://localhost:8060/api/health
```
