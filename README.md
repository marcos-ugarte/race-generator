# vg-racegen

**Standalone, pure producer of virtual race rounds (the "GA" generator).**

This repo is a clean extraction of the race generator from the `virtuales-go`
monolith. It does exactly one thing:

> Deterministically generate virtual race rounds (competitors, conditions,
> odds, finish order, video selection) and persist them to a local SQLite
> `relay.db`, plus an append-only GLI replay/audit log.

It is a **producer only**. It contains **no betting, no settlement, no money
handling, and no WebSocket/HTTP broadcasting**. Those concerns stay in
`virtuales-go` because they are coupled to PostgreSQL (`internal/pgpos`) and the
public/POS servers. This separation keeps the certifiable RNG surface (the
deterministic draw that decides each race's winner) isolated from everything
that touches a wallet.

## Documentación

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — arquitectura: los 4
  componentes (RNG / Generador / Relay / frontera Broadcaster), el núcleo RNG
  certificable GLI, flujo de datos, esquema del `relay.db`, determinismo,
  fronteras productor↔consumidor y roadmap. Con diagramas Mermaid.
- [`docs/MANUAL.md`](docs/MANUAL.md) — manual de uso y operación: quick start
  (local + Docker), tabla completa de env vars, disciplinas, despliegue, verificación,
  reproducibilidad/replay GLI, troubleshooting y build/test.

El resto de este README queda como entrada rápida.

## What's inside

- `cmd/race-generator/` — the binary's entrypoint (`main.go`).
- `internal/racegen/` — the generator core:
  - `rng/` — MT19937 + state-modifier + certified seeding (the GLI-certifiable RNG).
  - `generators/` — competitors, conditions, odds, finish, game, jackpot.
  - `data/` — embedded JSON assets (names, per-discipline videoResults) via `go:embed`.
  - `videoselector/`, `adapter/`, `audit/`, `config/`.
- `internal/sqlite/` — the `relay.db` writer. The schema is **inline** in
  `sqlite.go` (`const createTableSQL`); there are no SQL migration files for SQLite.
- `internal/config/`, `internal/models/`, `internal/raceutil/` — shared helpers.

The dependency graph is closed over exactly this subset — verified with
`go list -deps ./cmd/race-generator`. After `go mod tidy` the only direct
dependency is `modernc.org/sqlite` (pure-Go SQLite); the websocket/JWT/Postgres/
Prometheus dependencies of the monolith are gone.

## Environment variables

(See `loadEnv` in `cmd/race-generator/main.go` for the authoritative list.)

| Var                   | Default                       | Meaning |
|-----------------------|-------------------------------|---------|
| `DB_PATH`             | `./data/relay.db`             | SQLite output DB (rounds + results). |
| `RACEGEN_AUDIT_PATH`  | `./data/racegen-audit.jsonl`  | Append-only GLI replay/audit log. |
| `RACEGEN_SEED_HEX`    | (empty → crypto/rand in dev)  | 64-hex-char seed for the RNG. **Required when `APP_ENV` is `prod`/`staging`** so audit replay is deterministic (GLI-19 §3.3). |
| `RACEGEN_GAMETYPES`   | all supported                 | Comma-separated disciplines, e.g. `dog8,dog6,horse_classic`. |
| `RACEGEN_HORIZON`     | `25`                          | Number of future rounds to keep generated (min 2). |
| `RACEGEN_JACKPOT_INIT`| generator default             | Initial synthetic jackpot value. |
| `RACEGEN_TICK_MS`     | `1000`                        | Generator loop tick in ms (min 1). |
| `APP_ENV`             | (empty)                       | `prod`/`production`/`staging`/`stg` enforces a required seed. |

## Run it

### Local (`go run`)

```bash
mkdir -p data

# Deterministic run (fixed seed), two disciplines, to a local DB:
RACEGEN_SEED_HEX=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
DB_PATH=./data/relay.db \
RACEGEN_GAMETYPES=dog8,dog6 \
go run ./cmd/race-generator

# Or via Makefile (same fixed dev seed, all three disciplines):
make run-local
```

Rounds land in the `GameRounds` table (and results in `GameResults`) of
`data/relay.db`. The audit log streams to `data/racegen-audit.jsonl`. Both are
git-ignored.

### Docker

```bash
# Build the self-contained image (multi-stage, static binary, non-root):
docker build -t race-generator:dev .       # or: make docker-build

# Run standalone (relay.db + audit persist on a named volume):
docker compose up --build
```

The container publishes no ports (no HTTP/WS surface yet — that is Paso 2).
Its HEALTHCHECK is a WAL-freshness probe: the generator writes continuously, so
`/data/relay.db-wal` (or the checkpointed `/data/relay.db`) must have been
touched within the last 600s.

> **Seed in prod/staging.** `docker-compose.yml` ships safe dev defaults with an
> *empty* seed (crypto/rand, non-reproducible). With `APP_ENV=prod|staging|stg`
> the binary **fail-closes unless `RACEGEN_SEED_HEX` is set** (GLI replay). Inject
> the seed at deploy time — never hardcode a real seed in the compose file.

## Build & test

```bash
make build   # static binary -> ./race-generator
make test    # go test ./...  (includes the golden vectors)
make vet
make fmt
make lint    # golangci-lint if installed; otherwise a no-op
```

CI (`.github/workflows/ci.yml`) runs `go build`, `go vet`, a `gofmt -l` diff
check (fails on any unformatted file), and `go test ./...` on every push and PR.

The `generators/golden_test.go` and `rng/seed_golden_test.go` golden vectors pin
the RNG output. **If a golden test fails, do not rebaseline it** — it means the
RNG behaviour drifted, which would invalidate the GLI replay guarantee.

## Scope boundary (intentionally NOT here)

- Betting / odds quoting to the POS, ticket lifecycle, settlement, jackpot
  payouts → live in `virtuales-go` (coupled to `internal/pgpos`).
- WebSocket / HTTP broadcasting (`/tv`, `/pos`, `/web`) → live in `virtuales-go`.

This repo is **Step 1** of a larger extraction: the deterministic producer,
nothing more.
