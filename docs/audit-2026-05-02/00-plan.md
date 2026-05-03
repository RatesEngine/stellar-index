# Audit Plan

## Objective

Execute a full cold adversarial audit of the current repository
snapshot with explicit coverage of:

- every tracked file
- every material cross-file interaction
- every runtime binary and operator path
- every invariant the system claims to rely on
- every major code-to-doc, code-to-test, and code-to-proposal contract

This audit is cold. Prior markdown may identify areas to inspect, but
no doc claim is accepted without live code or test evidence.

## Repo-Specific Workstreams

### 1. Snapshot, Governance, and Hygiene

Audit:

- exact commit SHA and dirty-worktree caveats
- root repo map truth
- ownership files, contribution flow, release docs, pinned versions
- stale residue, misleading names, ignored-but-material artifacts
- separation between product code, discovery archive, and audit residue

Primary evidence:

- `AGENTS.md`, `CLAUDE.md`, `README.md`, `CONTRIBUTING.md`
- `CODEOWNERS`, `.github/`, `CHANGELOG.md`, `VERSIONS.md`
- `.gitignore`, repo snapshot inventory

### 2. Architecture, ADRs, and Negative Space

Audit:

- binaries and package graph vs architecture docs
- ADR invariants vs implementation
- residual legacy topology or dead abstractions
- undocumented architecture changes
- new packages since the prior audit, including whether they are wired

Primary evidence:

- `docs/adr/*.md`
- `docs/architecture/*.md`
- `cmd/*`
- `internal/*`

### 3. Build, Reproducibility, and Release Controls

Audit:

- `Makefile` truth
- local dev stack and migration flow
- generated docs paths
- release and rollback controls
- CI, lint, test, docs, and verify parity

Primary evidence:

- `Makefile`
- `.github/workflows/*`
- `deploy/docker-compose/*`
- `scripts/ci/*`, `scripts/dev/*`
- `docs/operations/*`

### 4. Dependency, Provenance, and Discovery Inputs

Audit:

- direct runtime dependencies
- heavy transitive or privileged tooling
- provenance posture and pinned SHAs
- discovery mirror checkouts and how they influence trust
- whether discovery repos are runtime inputs, documentation inputs, or dead residue

Primary evidence:

- `go.mod`, `go.sum`
- `VERSIONS.md`
- `.discovery-repos/*`
- workflow files and scanner scripts

### 5. Canonical Identity, Numeric Safety, and Serialization

Audit:

- asset/pair identity and parsing
- `*big.Int` and i128/u128 handling
- SCVal parsing and typed decode assumptions
- string, JSON, DB, and cache serialization boundaries
- stable identifiers used across API, Redis, Timescale, and ops tooling

Primary evidence:

- `internal/canonical/*`
- `internal/scval/*`
- `internal/cachekeys/*`
- `internal/events/*`

### 6. Ingest Transport, Dispatcher, and Persistence Pipeline

Audit:

- `ledgerstream` behavior and backpressure assumptions
- dispatcher matching and fall-through logic
- event/op decode error handling
- sink writes, cursor semantics, and recovery
- side-channel discovery outputs

Primary evidence:

- `internal/ledgerstream/*`
- `internal/dispatcher/*`
- `internal/pipeline/*`
- `cmd/ratesengine-indexer/*`

### 7. Source Decoders and Auxiliary Ledger Readers

Audit every file in:

- `internal/sources/soroswap`
- `internal/sources/aquarius`
- `internal/sources/phoenix`
- `internal/sources/comet`
- `internal/sources/blend`
- `internal/sources/reflector`
- `internal/sources/redstone`
- `internal/sources/band`
- `internal/sources/sdex`
- `internal/sources/accounts`
- `internal/sources/trustlines`
- `internal/sources/claimable_balances`
- `internal/sources/sac_balances`
- `internal/sources/sep41_supply`
- `internal/sources/liquidity_pools`

For each source:

- claim surface
- decode path
- malformed-input handling
- storage/consumer integration
- fixture realism
- tests vs actual risk

### 8. External Source Fleet and Source Policy

Audit:

- external framework, runner, backfill, and registry
- source class/subclass policy
- paid/free and backfill-safe metadata
- off-chain trust boundaries
- error, retry, rate-limit, and clock assumptions

Primary evidence:

- `internal/sources/external/*`
- `cmd/ratesengine-indexer/main.go`
- `internal/aggregate/*` source-consumption paths

### 9. Storage, Schema, Cache, and Migration Correctness

Audit:

- migrations and rollback semantics
- Timescale schema and query assumptions
- Redis key contracts and cache windows
- typed not-found behavior
- row identity, idempotency, and derived-surface correctness

Primary evidence:

- `migrations/*`
- `internal/storage/timescale/*`
- `internal/storage/redisclient/*`
- `internal/cachekeys/*`
- `test/integration/*`

### 10. Aggregation, Divergence, Freeze, Confidence, and Triangulation

Audit:

- VWAP/TWAP/OHLC derivation
- outlier and anomaly handling
- triangulation and provenance markers
- divergence cross-check sources
- confidence and freeze semantics
- stablecoin proxy logic

Primary evidence:

- `internal/aggregate/*`
- `internal/divergence/*`
- `cmd/ratesengine-aggregator/*`

### 11. API Runtime, Contracts, Streaming, and Auth

Audit:

- route registration and middleware ordering
- request identity, cache control, CORS, trusted proxies, rate limits
- handler validation and error envelopes
- `/v1/*`, SSE, SEP-40, health, and auth flows
- OpenAPI drift and RFP/proposal contract drift

Primary evidence:

- `internal/api/v1/*`
- `internal/api/streaming/*`
- `internal/auth/*`
- `openapi/rates-engine.v1.yaml`
- `cmd/ratesengine-api/*`

### 12. Supply, Metadata, and Asset Detail Enrichment

Audit:

- supply derivation and snapshot production
- asset metadata overlay and SEP-1 behavior
- Freighter F2 field production
- supply-reader and volume-reader correctness
- operator supply audit tools

Primary evidence:

- `internal/supply/*`
- `internal/metadata/*`
- `internal/api/v1/assets*.go`
- `cmd/ratesengine-ops/supply.go`

### 13. Operator Tooling, Archive Completeness, and DR/Safety Controls

Audit:

- every `ratesengine-ops` subcommand
- archive completeness and verify-archive paths
- cross-region comparison and monitor
- hubble and wasm audit tooling
- SLA probe and textfile outputs
- systemd, monitoring, and runbook alignment

Primary evidence:

- `cmd/ratesengine-ops/*`
- `cmd/ratesengine-sla-probe/*`
- `internal/archivecompleteness/*`
- `deploy/systemd/*`
- `deploy/monitoring/*`
- `docs/operations/runbooks/*`

### 14. Tests, CI Reality, and Regression Confidence

Audit:

- package tests by risk surface
- integration, chaos, and load harnesses
- what CI actually runs
- blind spots, false confidence, and skipped matrices

Primary evidence:

- `*_test.go`
- `test/*`
- `.github/workflows/*`
- `make verify`, `go test ./...`

### 15. Documentation Truth, Proposal Truth, and Customer Commitments

Audit:

- docs vs code
- RFPs vs shipped behavior
- proposal vs current implementation
- discovery archive vs current runtime
- stale or contradictory operator/runbook guidance

Primary evidence:

- `docs/stellar-rfp.md`
- `docs/freighter-rfp.md`
- `docs/ctx-proposal.md`
- `docs/discovery/*`
- `docs/reference/*`
- live code and tests

## Mandatory Passes

- top-down architecture pass
- bottom-up file pass
- end-to-end user journey pass
- end-to-end operator journey pass
- hostile-environment pass
- documentation-truth pass
- negative-space pass

## Required Deliverables

- complete tracker state
- complete file inventory with terminal statuses
- evidence log
- cross-file interaction ledger
- findings register
- exclusions register
- remediation plan tied to findings
