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

run-local: ## Run locally against ./data (dev defaults; HMAC-DRBG from OS entropy).
	mkdir -p data
	DB_PATH=./data/relay.db \
	RACEGEN_AUDIT_PATH=./data/racegen-audit.jsonl \
	RACEGEN_GAMETYPES=$${RACEGEN_GAMETYPES:-dog6,dog8,horse_classic} \
	go run ./cmd/race-generator

run-lab: ## Run the gli_lab build with a deterministic seed (lab replay only).
	mkdir -p data
	RACEGEN_SEED_HEX=$${RACEGEN_SEED_HEX:-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa} \
	DB_PATH=./data/relay.db \
	RACEGEN_AUDIT_PATH=./data/racegen-audit.jsonl \
	RACEGEN_GAMETYPES=$${RACEGEN_GAMETYPES:-dog6,dog8,horse_classic} \
	go run -tags gli_lab ./cmd/race-generator

test-lab: ## Full test suite under the gli_lab build tag.
	go test -tags gli_lab ./...

# ── GLI evidence extraction (docs/PLAN-CERTIFICACION-GLI19.md Fase 5) ────────
# Reproducible: fixed lab seeds + recorded binary hash. Each dataset uses a
# DISTINCT seed (suffix differs) so no submitted dataset is derivable from
# another — a lab cross-checking independence between the raw stream and the
# scaled/outcome files must find them unrelated. Override the base for a
# fresh evidence run and record the effective seeds in evidencia/README.
EVIDENCE_SEED_BASE ?= 5eed0f60c119000000000000000000000000000000000000000000000000
EVIDENCE_SEED_BITS  ?= $(EVIDENCE_SEED_BASE)0001
EVIDENCE_SEED_GAMES ?= $(EVIDENCE_SEED_BASE)0002
EVIDENCE_SEED_INT6  ?= $(EVIDENCE_SEED_BASE)0003
EVIDENCE_SEED_INT8  ?= $(EVIDENCE_SEED_BASE)0004
EVIDENCE_DIR  ?= evidencia

evidence-tools: ## Build the GLI collection tools (prod + lab flavors) and record hashes.
	mkdir -p $(EVIDENCE_DIR)
	go build -trimpath -o $(EVIDENCE_DIR)/rngextract ./cmd/rngextract
	go build -trimpath -tags gli_lab -o $(EVIDENCE_DIR)/rngextract-lab ./cmd/rngextract
	sha256sum $(EVIDENCE_DIR)/rngextract $(EVIDENCE_DIR)/rngextract-lab | tee $(EVIDENCE_DIR)/hashes.txt

evidence-bits: evidence-tools ## Raw Output Collection: 1 GB raw DRBG stream (lab seed, reproducible).
	$(EVIDENCE_DIR)/rngextract-lab -mode bits -seed $(EVIDENCE_SEED_BITS) \
		-bytes 1000000000 -out $(EVIDENCE_DIR)/bits-1g.bin 2>>$(EVIDENCE_DIR)/run.log

evidence-games: evidence-tools ## Final Outcome Collection: 10M outcomes per game type (lab seed).
	for gt in dog8 dog6 horse_classic; do \
		$(EVIDENCE_DIR)/rngextract-lab -mode game -seed $(EVIDENCE_SEED_GAMES) \
			-gametype $$gt -count 10000000 -out $(EVIDENCE_DIR)/games-$$gt.csv \
			2>>$(EVIDENCE_DIR)/run.log || exit 1; \
	done

evidence-int: evidence-tools ## Scaled-output evidence: 10M draws on ranges 6 and 8 (R8 rejection sampling).
	$(EVIDENCE_DIR)/rngextract-lab -mode int -seed $(EVIDENCE_SEED_INT6) -min 1 -max 6 \
		-count 10000000 -out $(EVIDENCE_DIR)/int-range6.csv 2>>$(EVIDENCE_DIR)/run.log
	$(EVIDENCE_DIR)/rngextract-lab -mode int -seed $(EVIDENCE_SEED_INT8) -min 1 -max 8 \
		-count 10000000 -out $(EVIDENCE_DIR)/int-range8.csv 2>>$(EVIDENCE_DIR)/run.log

run-feed: ## Run the feed locally against ./data/relay.db (reader; dev defaults).
	DB_PATH=$${DB_PATH:-./data/relay.db} \
	RACEGEN_FEED_PORT=$${RACEGEN_FEED_PORT:-4198} \
	RACEGEN_API_KEYS=$${RACEGEN_API_KEYS:-} \
	go run ./cmd/feed
