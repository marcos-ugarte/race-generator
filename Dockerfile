# syntax=docker/dockerfile:1.7
#
# Self-contained Dockerfile for vg-racegen — the PURE race producer.
# Builds ONLY ./cmd/race-generator (module vg-racegen, Go 1.25).
#
# Cloud-ready: small static binary (CGO disabled, modernc.org/sqlite is pure
# Go), minimal Alpine runtime, non-root user, WAL-freshness healthcheck.
#
#   docker build -t race-generator:dev .
#   docker build --build-arg VERSION=1.2.3 --build-arg COMMIT=$(git rev-parse --short HEAD) \
#                -t race-generator:1.2.3 .
#
# The binary has NO HTTP/WS surface (that is Paso 2). It only writes
# rounds + results into /data/relay.db and appends a GLI replay/audit log.

# ---------- builder ----------
# Match go.mod toolchain (>= 1.25). When bumping, update CI + Makefile too.
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /src

# Cache Go modules in a separate layer so go.sum changes invalidate less.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out && \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
        -o /out/race-generator ./cmd/race-generator

# ---------- runtime ----------
# Alpine over distroless because we want `wget` for healthchecks and `tzdata`
# (raceutil reads Europe/Malta at runtime — IMPRESCINDIBLE para el idRace epoch).
FROM alpine:3.19 AS runtime
RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -S -g 1000 app && adduser -S -u 1000 -G app app && \
    mkdir -p /data && chown app:app /data

COPY --from=builder /out/race-generator /app/race-generator

ENV DB_PATH=/data/relay.db
VOLUME ["/data"]
USER app

# Freshness probe: the generator writes continuously, so the WAL (or the .db
# itself, after a checkpoint) must have been touched in the last 600s. Same
# pattern as the race-generator target of virtuales-go's Dockerfile.
HEALTHCHECK --interval=60s --timeout=5s --start-period=30s --retries=3 \
    CMD test "$(($(date +%s) - $(stat -c %Y /data/relay.db-wal /data/relay.db 2>/dev/null | sort -rn | head -1)))" -lt 600 || exit 1

ENTRYPOINT ["/app/race-generator"]
