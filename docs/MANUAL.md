# Manual de uso y operación — vg-racegen

Manual operativo del **productor puro** de carreras GA. Para el diseño interno
ver [`ARCHITECTURE.md`](./ARCHITECTURE.md).

---

## 1. Requisitos

- **Go 1.25** (`go.mod`: `go 1.25.0`; única dependencia directa
  `modernc.org/sqlite`, SQLite puro-Go — sin CGO).
- **Docker** (opcional, para correr/desplegar como contenedor). La imagen es
  estática, no-root, Alpine + `tzdata` (raceutil lee `Europe/Malta` en runtime).

---

## 2. Quick start

### Local (`go run`)

```bash
mkdir -p data

# Corrida normal (HMAC-DRBG sembrado del SO), dos disciplinas, DB local:
DB_PATH=./data/relay.db \
RACEGEN_AUDIT_PATH=./data/racegen-audit.jsonl \
RACEGEN_GAMETYPES=dog8,dog6 \
go run ./cmd/race-generator

# O vía Makefile (mismo seed dev fijo, las tres disciplinas):
make run-local
```

Salida esperada (logs UTC; ver `generateAndPersist`/`main`):

```
race-generator: version=dev commit=...
race-generator: runner ready gameType=dog8 betoffer=541 interval=240s pool=... entries
race-generator: runner ready gameType=dog6 betoffer=141 interval=240s pool=... entries
race-generator: boot bulk OK gameType=dog8 rounds=25 lastSlot=2026-06-11T...Z
race-generator ready: 25 future rounds per gameType pre-generated
race-generator: scheduler start tickMs=1000 gameTypes=[dog8 dog6]
race-generator: emitted round=GA541_105_202606110123 gameType=dog8 1st=3 2nd=7 bonus=1
race-generator: emitted round=GA141_101_202606110140 gameType=dog6 1st=2 2nd=5 bonus=1
```

Las rondas caen en `GameRounds` (resultados en `GameResults`) de
`data/relay.db`; el audit en `data/racegen-audit.jsonl`. Ambos están
git-ignored. Con seed vacío, el binario emite un **WARN** y siembra con
`crypto/rand` (no reproducible — solo dev).

### Docker

```bash
docker build -t race-generator:dev .        # o: make docker-build

docker compose up --build                   # relay.db + audit en volumen nombrado
```

El contenedor **no publica puertos** (sin superficie HTTP/WS — eso es Paso 2).
Su `HEALTHCHECK` es una sonda de **frescura del WAL** (ver §6).

---

## 3. Configuración — variables de entorno

Fuente autoritativa: `loadEnv` en `cmd/race-generator/main.go:705`.

| Var | Default | Descripción | ¿Obligatoria? |
|---|---|---|---|
| `DB_PATH` | `./data/relay.db` | Ruta de la SQLite de salida (rondas + resultados). | No |
| `RACEGEN_AUDIT_PATH` | `./data/racegen-audit.jsonl` | Audit log GLI append-only (SHA-256 encadenado). | No |
| `RACEGEN_SEED_HEX` | (debe estar VACÍO en producción) | Seed de 64 hex — **solo builds `-tags gli_lab`** (replay del laboratorio). Un build de producción **aborta si está presente**. | Solo en builds gli_lab |
| `APP_ENV` | (vacío) | `prod`/`staging`/`dev` — informativo. | No |
| `RACEGEN_GAMETYPES` | todas las soportadas (`dog8,dog6,horse_classic`) | Disciplinas coma-separadas, trim + dedup, orden preservado. | No |
| `RACEGEN_HORIZON` | `25` | Rondas futuras pre-generadas por gameType. **Mínimo 2.** | No |
| `RACEGEN_JACKPOT_INIT` | `45000.00` (`generators.JackpotInitialValue`) | Valor inicial del jackpot sintético. Debe ser ≥ 0. | No |
| `RACEGEN_TICK_MS` | `1000` | Frecuencia del scheduler en ms. **Mínimo 1.** | No |

> **Seeding en producción (GLI-19).** El binario de producción instancia un
> HMAC-DRBG (SP 800-90A) desde el CSPRNG del SO y **aborta si
> `RACEGEN_SEED_HEX` está presente** (`source_prod.go` — la semilla de
> producción debe ser impredecible; una corrida reproducible permitiría
> precomputar resultados). El replay determinista existe SOLO en builds
> `-tags gli_lab` (jamás se despliegan), donde la seed es obligatoria. Si se
> proporciona, la seed se valida (64 chars + hex) antes de abrir audit/DB.

> **Defaults compartidos.** `DB_PATH` también tiene default `./data/relay.db`
> en `internal/config/config.go:77`; el binario usa el de `loadEnv`.

---

## 4. Disciplinas soportadas

Registro: `internal/racegen/config/extended.go` (`registry`, línea 901) e
identidades de `internal/config/config.go` (`GAME_TYPES`).

| GameType | Betoffer | Competidores | Odds | Intervalo | VideoDuration | EventType | Estado |
|---|---|---|---|---|---|---|---|
| `dog8` | 541 | 8 | 64 | 240 s | 60 s | `dog8` | Abierta (calibrada DS) |
| `dog6` | 141 | 6 | 36 | 240 s | 60 s | `dog` | Abierta (calibrada DS) |
| `horse_classic` | 241 | 7 | 49 | 240 s | 140 s | `horsec` | **Gated** (pool real, odds placeholder — GLI gate cerrado) |

> **No se generan.** `horse` (251) y `dog63` (741) existen en `GAME_TYPES`
> (`internal/config/config.go`) pero **no** están en el registro de
> `racegen/config`, así que el generador los rechaza (`config.Get` →
> "unknown game type"). Pedirlos en `RACEGEN_GAMETYPES` aborta el arranque.

> **horse_classic.** El pool de finish es DS **real**
> (`data/videoResults-horse_classic.json`, 8.256 rondas 241 capturadas), así que
> el ganador/orden tracks al vendor. Pero la calibración de **odds** es
> smoke-level (`PositionConstraints`/targets uniformes, `RankGap`/`ForecastRank`
> deshabilitados): el GLI gate **debe** seguir cerrado para 241
> (`config/extended.go:735`).

---

## 5. Despliegue en producción

1. **Seed (GLI).** NO inyectar `RACEGEN_SEED_HEX` en producción: el binario
   aborta si está presente. Eliminar cualquier seed heredado de despliegues
   anteriores (env, secrets, .env) antes de actualizar a esta versión.
2. **`APP_ENV`.** Fijar `prod` (informativo para logs/operación).
3. **Volumen `/data`.** `DB_PATH=/data/relay.db` + `RACEGEN_AUDIT_PATH=/data/racegen-audit.jsonl`
   sobre un volumen nombrado (compose: `racegen-data`) para que `relay.db` y el
   audit persistan entre reinicios. El usuario del contenedor es `1000:1000`.
4. **Healthcheck (WAL).** Heredado del Dockerfile: el contenedor está `healthy`
   si `/data/relay.db-wal` (o `/data/relay.db` tras checkpoint) fue tocado en los
   últimos **600 s**. Como el generador escribe continuamente, un WAL "viejo"
   (> 600 s) marca `unhealthy` → el orquestador reinicia.

Ejemplo de inyección de seed en deploy:

```bash
APP_ENV=prod docker compose up -d   # sin RACEGEN_SEED_HEX — ver paso 1
```

---

## 6. Operación y verificación

**¿Está generando?**

- **Logs**: líneas `emitted round=GA… gameType=… 1st=… 2nd=… bonus=…`
  (`main.go:673`). Tras el boot, `tickHorizon` emite ~1 ronda nueva por gameType
  cada `RoundIntervalSec` (240 s); la mayoría de ticks son no-ops.
- **Contar filas** en `relay.db`:

  ```bash
  sqlite3 data/relay.db \
    "SELECT GameType, COUNT(*) FROM GameRounds WHERE RoundCode LIKE 'GA%' GROUP BY GameType;"
  sqlite3 data/relay.db \
    "SELECT RoundCode, VideoStartDt, Status FROM GameRounds WHERE RoundCode LIKE 'GA%' ORDER BY VideoStartDt DESC LIMIT 5;"
  ```

  Todas las rondas GA salen con `Status='F'` (precomputado); filtra por
  `VideoStartDt`, no por `Status`.

- **Audit JSONL**: cada ronda añade un `game_generated` + un `state_mod`; el
  arranque añade un `init`.

  ```bash
  wc -l data/racegen-audit.jsonl
  tail -n 3 data/racegen-audit.jsonl
  ```

- **Frescura del WAL** (lo mismo que mira el healthcheck):

  ```bash
  stat -c '%Y' data/relay.db-wal   # epoch del último write; debe ser reciente
  ```

---

## 7. Reproducibilidad / replay GLI

Reproducibilidad (solo builds `-tags gli_lab`):

1. El audit `init` registra un descriptor de la fuente (`rngSource`) y, en
   builds de laboratorio, un **fingerprint SHA-256** de la seed — nunca la
   seed misma (la seed de una corrida de evidencia se registra por separado
   en la metadata de la corrida). En producción la corrida es no
   reproducible **por diseño** (GLI-19: imprevisibilidad del seeding).
2. En un build gli_lab, la secuencia de draws es función exclusiva de la
   seed (entropía HKDF determinista; el reseed por frontera de ronda no
   consume reloj). Para evidencia con identidad de ronda también fija se usa
   `cmd/rngextract -mode game` (slot inicial fijo).
3. **Entre rondas**, el descarte `N` (1..100) viene de `crypto/rand`, así que
   re-ejecutar con el mismo seed **no** reproduce solo: hay que re-aplicar los
   `discard` registrados en cada `state_mod` del audit.
4. **Verificar la cadena** del audit (que el log no fue alterado):

   ```go
   n, err := audit.Verify("data/racegen-audit.jsonl") // internal/racegen/audit/log.go:171
   ```

   Comprueba SHA-256 recomputado, `prevHash` encadenado, genesis
   (`prevHash==""`) y `seq` monótono.

Los **golden vectors** (`rng/seed_golden_test.go`, `generators/golden_test.go`)
pinan la salida del RNG: si un golden test falla, el comportamiento del RNG
derivó → **no rebaselinar** (invalidaría la garantía de replay GLI).

---

## 8. Troubleshooting

| Síntoma | Causa probable | Acción |
|---|---|---|
| `RACEGEN_SEED_HEX is set but this is a production build` (arranque aborta) | Seed heredado de la semántica antigua en el entorno de deploy. | **Eliminar** `RACEGEN_SEED_HEX` del entorno (env/secrets/.env). El replay con seed es solo de builds `-tags gli_lab`. |
| `RACEGEN_SEED_HEX must be 64 hex chars` / `invalid hex` | Seed mal formado. | Usar exactamente 64 chars hex válidos. |
| `unknown game type "horse"` (o `dog63`) | Pediste una disciplina no registrada en racegen. | Solo `dog8`, `dog6`, `horse_classic` en `RACEGEN_GAMETYPES`. |
| `RACEGEN_HORIZON N: must be >= 2` | Horizonte < 2; el webview necesita 2 futuras. | Subir a ≥ 2 (default 25). |
| No genera rondas nuevas tras el boot | Normal: la mayoría de ticks son no-ops; solo emite al cruzar un límite de 240 s. Revisar logs `emitted`. | Esperar un intervalo; confirmar con el conteo de filas. |
| Contenedor `unhealthy` / WAL viejo | El generador dejó de escribir (WAL > 600 s) o crash. | Ver logs (`PANIC`/`permanent err`); el orquestador reinicia; el boot idempotente recubre el hueco si < HORIZON intervalos. |
| `database is locked` / `SQLITE_BUSY` en logs | Contención SQLite transitoria. | El scheduler reintenta el slot (hasta 3 veces, `isTransientErr`); si persiste, revisar otros escritores sobre la misma DB. |
| `VIRTUALES_DB_REQUIRE_EXISTING=1` y la DB no existe | Modo post-cutover: ds-capture es el creador canónico. | Pre-crear el archivo o desactivar el env. |

---

## 9. Build & test

Targets del `Makefile`:

```bash
make build         # binario estático -> ./race-generator (CGO_ENABLED=0, -trimpath, ldflags version/commit)
make test          # go test ./...  (incluye los golden vectors GLI)
make vet           # go vet ./...
make fmt           # gofmt -w . (+ goimports si está)
make lint          # golangci-lint si está instalado; si no, no-op (no falla)
make docker-build  # imagen self-contained (IMAGE=race-generator:$VERSION)
make run-local     # corre contra ./data con seed dev fijo
```

**CI** (`.github/workflows/ci.yml`), en cada push y PR:

- `go build ./...`
- `go vet ./...`
- `gofmt -l .` (falla si hay cualquier archivo sin formatear)
- `go test ./...` — **incluye los golden vectors**, que son la garantía de
  determinismo / replay GLI. Si un golden test falla, el RNG derivó: investigar,
  no rebaselinar.
- `golangci-lint` corre en un job aparte con `continue-on-error` (reporta pero
  no bloquea).
