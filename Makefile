# Rates Engine — canonical build / test / ops commands.
#
# Every non-trivial task should have a make target. If a contributor
# has to memorise a shell incantation, that's a bug — add a target.
#
# Run `make help` to list every target.

.DEFAULT_GOAL := help

# Go toolchain
GO              := go
GOBIN           := $(shell $(GO) env GOPATH)/bin
GOOS            ?= $(shell $(GO) env GOOS)
GOARCH          ?= $(shell $(GO) env GOARCH)

# Project metadata
MODULE          := github.com/RatesEngine/rates-engine
VERSION         := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LD_FLAGS        := -X $(MODULE)/internal/version.Version=$(VERSION) \
                   -X $(MODULE)/internal/version.BuildDate=$(BUILD_DATE)

# Binaries we build
BINARIES := \
  ratesengine-indexer \
  ratesengine-aggregator \
  ratesengine-api \
  ratesengine-ops \
  ratesengine-migrate

# Packages that hold integration tests (gated by build tag)
INT_TEST_PKGS := ./test/integration/...

# Default test-cover threshold per package (staticcheck in CI enforces the per-package floor)
COVER_THRESHOLD := 70

# ──────────────────────────────────────────────────────────────────
# Help
# ──────────────────────────────────────────────────────────────────

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage: make \033[36m<target>\033[0m\n\nTargets:\n"} \
	     /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 } \
	     /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

.PHONY: deps
deps: ## Download + verify Go module deps and tools
	$(GO) mod download
	$(GO) mod verify
	@if [ -f tools/tools.go ]; then \
	  cd tools && $(GO) install $$(awk -F\" '/_ "/ {print $$2}' tools.go); \
	fi

.PHONY: dev
dev: ## Start the full local stack (Postgres/Timescale, Redis, MinIO, stellar-core, Galexie, stellar-rpc)
	@docker compose -f deploy/docker-compose/dev.yaml up -d
	@echo "Stack up. API at http://localhost:3000; docs at http://localhost:8080"

.PHONY: dev-teardown
dev-teardown: ## Stop the local stack and remove volumes
	@docker compose -f deploy/docker-compose/dev.yaml down -v

.PHONY: dev-seed
dev-seed: ## Load fixture data into local stack
	@./scripts/dev/seed.sh

##@ Quality gates

.PHONY: fmt
fmt: ## Format all Go code with gofumpt + goimports
	@$(GOBIN)/gofumpt -w .
	@$(GOBIN)/goimports -w -local $(MODULE) .

.PHONY: lint
lint: ## Run golangci-lint + archlint
	@$(GOBIN)/golangci-lint run ./...
	@$(GOBIN)/go-arch-lint check ./... || true

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: test
test: ## Run unit tests with race detector
	$(GO) test -race -timeout 2m ./...

.PHONY: test-cover
test-cover: ## Unit tests + coverage report
	$(GO) test -race -coverprofile=coverage.txt -covermode=atomic ./...
	$(GO) tool cover -html=coverage.txt -o coverage.html
	@echo "Coverage report: coverage.html"

.PHONY: test-integration
test-integration: ## Integration tests (requires Docker; spins its own containers via testcontainers-go)
	$(GO) test -tags=integration -timeout 10m $(INT_TEST_PKGS)

.PHONY: test-load
test-load: ## Run k6 load suite against staging (requires K6_TARGET env)
	@k6 run test/load/api_steady_state.js

.PHONY: test-all
test-all: lint vet test test-integration ## Everything short of load + chaos

.PHONY: lint-docs
lint-docs: ## Doc-code consistency linter (freshness, links, ADR integrity, TODO discipline)
	@./scripts/ci/lint-docs.sh

.PHONY: lint-imports
lint-imports: ## Import-boundary lint (production ingest doesn't import stellar-rpc, xdr scoped to scval, etc.)
	@./scripts/ci/lint-imports.sh

.PHONY: monitoring-check
monitoring-check: ## Validate Prometheus rule files with promtool
	@if ! command -v promtool >/dev/null 2>&1; then \
	  echo "promtool not found — install via 'brew install prometheus' or the Prometheus GH release"; \
	  exit 1; \
	fi
	@promtool check rules deploy/monitoring/rules/*.yml

.PHONY: verify
verify: ## Sequential local quality gate (fmt, vet, lint, docs, test) — run before every push
	@./scripts/dev/verify.sh

.PHONY: audit
audit: ## Dependency vulnerability audit (govulncheck)
	@$(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	@$(GOBIN)/govulncheck ./...

##@ Build

.PHONY: build
build: ## Build all binaries into bin/
	@mkdir -p bin
	@for b in $(BINARIES); do \
	  echo "Building $$b"; \
	  $(GO) build -trimpath -buildvcs=true -ldflags="$(LD_FLAGS)" -o bin/$$b ./cmd/$$b || exit 1; \
	done

.PHONY: build-docker
build-docker: ## Build all component Docker images locally (requires per-binary Dockerfiles in docker/)
	@if [ ! -d docker ]; then \
	  echo "build-docker: docker/ directory not present yet — per-binary Dockerfiles land with the packaging PR." >&2; \
	  echo "build-docker: local dev uses deploy/docker-compose/dev.yaml; production deploys via configs/ansible/." >&2; \
	  exit 2; \
	fi
	@for b in $(BINARIES); do \
	  docker build -t ratesengine/$$b:local -f docker/$$b.Dockerfile . || exit 1; \
	done

##@ Database migrations

.PHONY: db-migrate-up
db-migrate-up: ## Apply pending migrations
	@$(GO) run ./cmd/ratesengine-migrate up

.PHONY: db-migrate-down
db-migrate-down: ## Revert most recent migration
	@$(GO) run ./cmd/ratesengine-migrate down 1

.PHONY: db-migrate-status
db-migrate-status: ## Show migration state
	@$(GO) run ./cmd/ratesengine-migrate status

##@ Documentation

.PHONY: docs
docs: docs-all ## Alias for docs-all

.PHONY: docs-all
docs-all: docs-api docs-config docs-metrics ## Regenerate all reference docs

.PHONY: docs-api
docs-api: ## Regenerate API reference from openapi/rates-engine.v1.yaml
	@if command -v redocly >/dev/null 2>&1; then \
	  mkdir -p docs/reference/api; \
	  redocly build-docs openapi/rates-engine.v1.yaml \
	    --output docs/reference/api/index.html; \
	  echo "GENERATED FILE - DO NOT EDIT. Source: openapi/rates-engine.v1.yaml" \
	    > docs/reference/api/README.md; \
	  echo "" >> docs/reference/api/README.md; \
	  echo "The rendered reference is [index.html](index.html)." >> docs/reference/api/README.md; \
	else \
	  echo "redocly not installed. Install with 'npm i -g @redocly/cli' to render the API reference."; \
	  echo "Source of truth: openapi/rates-engine.v1.yaml"; \
	  exit 1; \
	fi

.PHONY: docs-config
docs-config: ## Regenerate config reference from struct tags
	@$(GO) run ./cmd/ratesengine-ops docs-config > docs/reference/config/README.md

.PHONY: docs-metrics
docs-metrics: ## Regenerate metrics registry reference
	@# The metrics reference at docs/reference/metrics/README.md is hand-
	@# written today — there's no Prometheus Registry walker wired up.
	@# Drift is guarded by scripts/ci/lint-docs.sh section 3 (every
	@# registered metric in internal/obs must appear in the README).
	@echo "docs-metrics is manual today; docs/reference/metrics/README.md is hand-edited."
	@echo "Drift enforced by 'make lint-docs'. See TODO in metrics/README.md."

.PHONY: docs-serve
docs-serve: ## Preview docs site locally on :8080
	@./scripts/dev/docs-serve.sh

##@ Release

.PHONY: release-dryrun
release-dryrun: ## goreleaser dry run — useful pre-tag
	@goreleaser release --snapshot --clean

##@ Housekeeping

.PHONY: clean
clean: ## Remove build artefacts + coverage
	@rm -rf bin/ dist/ coverage.txt coverage.html

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy
