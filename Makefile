# Makefile — local developer workflow for vg-racegen (the pure race producer).
#
# `make` (no target) prints this help. Production deploys use the Docker
# image (see Dockerfile / docker-compose.yml), not make.

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
IMAGE      ?= race-generator:$(VERSION)
FEED_IMAGE ?= race-feed:$(VERSION)

.PHONY: help build test vet fmt lint docker-build docker-build-feed run-local run-feed

# Default target lists commands.
help: ## Show this help.
	@awk -F':.*## ' '/^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the race-generator AND feed binaries into ./race-generator and ./feed.
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o race-generator ./cmd/race-generator
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o feed ./cmd/feed

test: ## Run all unit tests (includes the GLI golden vectors).
	go test ./...

vet: ## go vet across the whole module.
	go vet ./...

fmt: ## gofmt (+ goimports if present) on the whole tree.
	gofmt -w .
	@command -v goimports >/dev/null && goimports -w . || true

lint: ## golangci-lint if installed; otherwise a no-op (does not fail).
	@command -v golangci-lint >/dev/null && golangci-lint run ./... || \
		echo "golangci-lint not installed — skipping (install: https://golangci-lint.run)"

docker-build: ## Build the generator Docker image (IMAGE=$(IMAGE)).
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .

docker-build-feed: ## Build the feed Docker image (FEED_IMAGE=$(FEED_IMAGE)).
	docker build --target feed --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(FEED_IMAGE) .

run-local: ## Run locally against ./data (dev defaults; deterministic seed).
	mkdir -p data
	RACEGEN_SEED_HEX=$${RACEGEN_SEED_HEX:-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa} \
	DB_PATH=./data/relay.db \
	RACEGEN_AUDIT_PATH=./data/racegen-audit.jsonl \
	RACEGEN_GAMETYPES=$${RACEGEN_GAMETYPES:-dog6,dog8,horse_classic} \
	go run ./cmd/race-generator

run-feed: ## Run the feed locally against ./data/relay.db (reader; dev defaults).
	DB_PATH=$${DB_PATH:-./data/relay.db} \
	RACEGEN_FEED_PORT=$${RACEGEN_FEED_PORT:-4198} \
	RACEGEN_API_KEYS=$${RACEGEN_API_KEYS:-} \
	go run ./cmd/feed
