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
GOFUMPT_VERSION := v0.8.0
GOIMPORTS_VERSION := v0.42.0
GOLANGCI_LINT_VERSION := v2.11.4
GOVULNCHECK_VERSION := v1.1.4

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
	@$(GO) install mvdan.cc/gofumpt@$(GOFUMPT_VERSION)
	@$(GO) install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)
	@$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@if [ -f tools/tools.go ]; then \
	  cd tools && $(GO) install $$(awk -F\" '/_ "/ {print $$2}' tools.go); \
	fi

.PHONY: dev
dev: ## Start the local dev stack — Postgres/Timescale + Redis + MinIO (deploy/docker-compose/dev.yaml). stellar-core/Galexie/stellar-rpc run on a remote box (r1), not in compose.
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
lint: ## Run golangci-lint
	@$(GOBIN)/golangci-lint run ./...

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

.PHONY: test-integration-build
test-integration-build: ## Compile integration tests without running them (no Docker, fast). Catches build-tag breakage from interface changes.
	$(GO) test -tags=integration -run nothing -count=0 $(INT_TEST_PKGS)

# k6 load suite (Task #74). Each target sets PROM_OUT for the
# experimental-prometheus-rw exporter so the run lands in the same
# Grafana stack on-call uses (design note §How the proof report is
# generated). Production-target guard is enforced inside
# scenarios/lib/env.js, so direct `k6 run` cannot bypass it.
PROM_OUT ?= experimental-prometheus-rw

.PHONY: test-load-guard
test-load-guard:
	@if [ -z "$$K6_TARGET" ]; then \
	  echo "K6_TARGET is required (e.g. https://api.staging.ratesengine.net/v1)"; exit 2; \
	fi
	@if [ -z "$$RATESENGINE_LOAD_API_KEY" ]; then \
	  echo "RATESENGINE_LOAD_API_KEY is required (mint from vault)"; exit 2; \
	fi
	@case "$$K6_TARGET" in \
	  *api.ratesengine.net*|*api.ratesengine.io*|*rates.stellar.org*) \
	    echo "Refusing to load-test production target $$K6_TARGET"; exit 2;; \
	esac

.PHONY: test-load
test-load: test-load-guard ## Run all k6 scenarios against $K6_TARGET (slow; ~30 min)
	@for s in test/load/scenarios/[0-9]*.js; do \
	  echo "=== $$s ==="; k6 run --out $(PROM_OUT) $$s || exit $$?; \
	done

.PHONY: test-load-mixed
test-load-mixed: test-load-guard ## Run the canonical mixed-realistic SLA proof (Task #77)
	@k6 run --out $(PROM_OUT) test/load/scenarios/06-mixed-realistic.js

.PHONY: test-load-price
test-load-price: test-load-guard ## Run only the price hot-path scenario (5 min)
	@k6 run --out $(PROM_OUT) test/load/scenarios/01-price-hot-path.js

.PHONY: test-load-vwap
test-load-vwap: test-load-guard ## Run only the VWAP/TWAP scenario (5 min)
	@k6 run --out $(PROM_OUT) test/load/scenarios/02-vwap-twap.js

.PHONY: test-load-history
test-load-history: test-load-guard ## Run only the history scenario (5 min)
	@k6 run --out $(PROM_OUT) test/load/scenarios/03-history.js

.PHONY: test-load-batch
test-load-batch: test-load-guard ## Run only the batch scenario (5 min)
	@k6 run --out $(PROM_OUT) test/load/scenarios/04-batch.js

.PHONY: test-load-streaming
test-load-streaming: test-load-guard ## Run only the SSE streaming scenario (5 min)
	@k6 run --out $(PROM_OUT) test/load/scenarios/05-streaming.js

.PHONY: test-load-spike
test-load-spike: test-load-guard ## Run the 10× spike scenario (5 min). Posts AlertManager silence if ALERTMANAGER_URL set
	@if [ -z "$$ALERTMANAGER_URL" ]; then \
	  echo "WARN: ALERTMANAGER_URL unset — spike will not silence on-call alerts."; \
	  echo "      Manually silence APIHighLatencyP95 + APIHighErrorRate before continuing."; \
	  echo "      Press Ctrl-C to abort, or wait 10s to proceed."; \
	  sleep 10; \
	fi
	@k6 run --out $(PROM_OUT) test/load/scenarios/99-spike.js

.PHONY: test-load-check
test-load-check: ## Compile-check all k6 scenarios without running them (no target needed)
	@for s in test/load/scenarios/[0-9]*.js; do \
	  echo "=== $$s ==="; k6 archive --quiet -O /dev/null $$s || exit $$?; \
	done

.PHONY: test-chaos
test-chaos: ## Run the Wave 1 chaos suite against the dev stack (Task #75)
	@./test/chaos/run.sh

.PHONY: test-chaos-check
test-chaos-check: ## Lint the chaos scenarios (shellcheck) without running them
	@for s in test/chaos/run.sh test/chaos/scenarios/[0-9]*.sh test/chaos/scenarios/lib/*.sh; do \
	  echo "=== $$s ==="; shellcheck -x "$$s" || exit $$?; \
	done

.PHONY: test-all
test-all: lint vet test test-integration ## Everything short of load + chaos

.PHONY: lint-docs
lint-docs: ## Doc-code consistency linter (freshness, links, ADR integrity, TODO discipline)
	@./scripts/ci/lint-docs.sh

.PHONY: lint-imports
lint-imports: ## Import-boundary lint (production ingest doesn't import stellar-rpc, xdr scoped to scval, etc.)
	@./scripts/ci/lint-imports.sh

.PHONY: lint-openapi-urls
lint-openapi-urls: ## ADR-0018 URL-discipline check on the OpenAPI spec
	@$(GO) run ./scripts/ci/lint-openapi-urls openapi/rates-engine.v1.yaml

.PHONY: verify-launch-ready
verify-launch-ready: ## Single-pane status check on the launch-readiness backlog
	@$(GO) run ./scripts/ci/verify-launch-ready

.PHONY: verify-launch-ready-all
verify-launch-ready-all: ## verify-launch-ready with full per-row listing
	@$(GO) run ./scripts/ci/verify-launch-ready -all

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
	@$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
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
docs-api: ## Regenerate API reference from openapi/rates-engine.v1.yaml (Redocly via npx — no global install required)
	@./scripts/dev/docs-api.sh

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
release-dryrun: ## Validate whether in-repo goreleaser packaging exists for this snapshot
	@if [ ! -f .goreleaser.yaml ]; then \
	  echo "release-dryrun: .goreleaser.yaml is not present in this repo snapshot." >&2; \
	  echo "release-dryrun: use 'make build' plus the external release packaging process documented in docs/operations/release-process.md." >&2; \
	  exit 2; \
	fi
	@goreleaser release --snapshot --clean

##@ Housekeeping

.PHONY: clean
clean: ## Remove build artefacts + coverage
	@rm -rf bin/ dist/ coverage.txt coverage.html

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy
