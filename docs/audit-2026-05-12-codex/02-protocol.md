# Execution Protocol

## 1. Zero-Trust Rule

No claim is accepted because it appears in documentation, prior audit
material, comments, generated output, test names, architecture diagrams,
or operator notes.

For every material claim:

1. identify the live source path
2. identify the runtime wiring that reaches it
3. identify tests or command output that exercise it
4. identify observability or operator visibility
5. classify docs as true, partial, stale, contradictory, or unverified

## 2. Evidence Standard

Every material audit statement must cite at least one evidence ID from
[evidence/log.md](evidence/log.md).

Accepted evidence types:

- local file references with line anchors
- generated inventory artifacts in this directory
- command output recorded in [evidence/commands.md](evidence/commands.md)
- test behavior with exact command and asserted behavior
- R1 command output with host, command, timestamp, and redaction notes
- cross-file trace entries from
  [evidence/cross-file-interactions.md](evidence/cross-file-interactions.md)

Unaccepted evidence types:

- prior findings
- unverified docs
- memory
- comments without executable backing
- green tests without knowing what the tests assert

## 3. Evidence ID Format

Use stable IDs:

- `EV-0001` for general evidence
- `CMD-0001` for command entries
- `XFI-0001` for cross-file interactions
- `J-0001` for completed journey traces
- `R1-0001` for R1 runtime observations

Evidence IDs may be referenced from findings, inventory rows, journey
tables, exclusions, and remediation items.

## 4. Per-File Audit Loop

For each row in [inventory/file-coverage.tsv](inventory/file-coverage.tsv):

1. read the file directly
2. classify file role:
   `runtime`, `test`, `fixture`, `migration`, `config`, `deploy`,
   `workflow`, `script`, `documentation`, `generated`, `frontend`,
   `asset`, `policy`, or `unknown`
3. identify inbound dependencies:
   imports, callers, routes, workflows, scripts, docs, generated inputs
4. identify outbound dependencies:
   imports, commands, network calls, database objects, cache keys,
   files, env vars, metrics, alerts, docs
5. identify trust boundaries:
   external input, user input, ledger data, third-party API, DB, Redis,
   filesystem, CI secret, SSH host, browser, Cloudflare, systemd
6. identify invariants:
   precision, idempotency, consistency, freshness, auth, ordering,
   schema, source attribution, rate limit, timeout, privacy
7. identify tests and whether they exercise meaningful behavior
8. identify docs that describe the file and classify doc truth
9. update status:
   `todo`, `in_progress`, `done`, `blocked`, or `excluded`
10. record evidence refs and notes

`done` means reviewed with evidence. It does not mean bug-free.

## 5. Cross-File Interaction Loop

Record every material interaction in
[evidence/cross-file-interactions.md](evidence/cross-file-interactions.md):

- binary -> config -> package
- package -> package interface
- decoder -> event model -> pipeline sink -> Timescale store
- migration -> store method -> API response -> OpenAPI schema
- Redis key -> aggregator writer -> API reader -> SSE publisher
- workflow -> script -> generated artifact
- Dockerfile/systemd/Ansible -> binary flags/env -> config parser
- alert rule -> metric name -> runbook -> service owner
- frontend route -> API client -> backend route -> OpenAPI type
- docs/RFP/proposal -> code path

For each interaction, record:

- source files
- target files
- data contract
- failure modes
- tests
- observability
- docs claims
- findings or notes

## 6. Severity Protocol

Use this scale in [05-findings-register.md](05-findings-register.md):

| Severity | Meaning |
| --- | --- |
| critical | Can cause severe customer harm, material market-data corruption, credential compromise, broad outage, unrecoverable data loss, or launch-blocking regulatory/security risk |
| high | Can cause wrong prices/supply, broken auth, serious outage, silent data loss, unsafe operations, or major customer commitment breach |
| medium | Meaningful correctness, reliability, security, observability, or product gap with bounded blast radius |
| low | Localized defect, stale docs with low operational risk, weak test, minor UX/API inconsistency |
| note | Non-defect observation useful for hardening, prioritization, or competitive completeness |

Severity must consider:

- customer-visible market-data correctness
- Stellar-specific semantic correctness
- exploitability or manipulation
- blast radius across assets, sources, or regions
- detection and recovery capability
- competitive credibility

## 7. Findings Rules

Each finding must include:

- stable ID: `F-1201`, `F-1202`, ...
- severity
- title
- affected files/surfaces
- exact evidence refs
- expected behavior
- observed behavior
- impact
- reproduction or reasoning path
- remediation direction
- owner/status

Do not create a finding from suspicion alone. Create a note or open
question if evidence is incomplete.

## 8. Exclusion And Blocking Rules

Use [06-exclusions-register.md](06-exclusions-register.md) for any file,
host, command, or scope that cannot be audited.

Each exclusion needs:

- exact target
- type: `excluded` or `blocked`
- reason
- risk accepted by the exclusion
- evidence required to re-enter scope
- temporary or permanent for this audit

## 9. Test Interpretation Rules

Tests prove only their assertions.

For each important test suite:

- record the command
- record whether it ran locally, in CI, or not at all
- identify what behavior it asserts
- identify what it leaves unproven
- check skipped tests, build tags, env requirements, network reliance,
  race coverage, testcontainers, and fixture realism

## 10. R1 Runtime Protocol

R1 access is allowed by the user, but runtime commands must still be
disciplined.

Before using R1:

- record why source review cannot answer the question alone
- prefer read-only commands
- avoid secrets, private keys, and raw customer data
- redact sensitive output before logging
- record host, command, timestamp, and purpose

Allowed R1 observation classes:

- service health and versions
- systemd unit status
- recent logs with secrets redacted
- process args and env names, not secret values
- listening ports and firewall state
- disk, memory, CPU, and IO state
- database migration version and aggregate row counts
- Redis key shape and TTL samples
- Prometheus target/alert state
- recent archive completeness and SLA probe outputs

Do not use R1 output as the sole evidence for source-code correctness.

## 11. Hostile Review Protocol

For every runtime path, test or reason through:

- malformed JSON, XDR, SCVal, and event topics
- duplicate, missing, reordered, or replayed ledger events
- bad decimals, negative quantities, overlarge quantities, i128/u128
  edge values, and overflow attempts
- stale external API payloads
- rate limits, timeouts, empty bodies, schema drift, and 500s
- Redis misses, stale values, and partial pub/sub delivery
- Timescale unavailability, partial migrations, duplicate writes, and
  transaction rollback
- clock skew and bucket boundary races
- stale frontend/static output
- compromised proxy headers
- missing or malformed auth credentials
- region divergence and partial failover

## 12. Competitive Completeness Protocol

Review the product as a market-data competitor:

- asset and market catalogue breadth
- venue attribution and source transparency
- price freshness and historical depth
- chart, OHLC, VWAP, TWAP, and volume correctness
- issuer, supply, freeze/clawback, and trustline visibility
- DEX-specific pool, route, reserves, fees, lending, MEV, and auction
  visibility
- API usability, SDK quality, widgets, embeds, and docs
- status, methodology, incidents, and data-quality confidence

Classify gaps as findings when they contradict shipped claims,
customer commitments, or safety expectations. Classify them as notes
when they are roadmap/product opportunities without contradiction.

## 13. Documentation Truth Protocol

For each material doc claim:

- locate implementation
- locate tests
- locate deployment/runtime evidence if applicable
- classify: `true`, `partial`, `stale`, `contradicted`, or
  `unverifiable`
- create findings for contradictions or dangerous omissions

## 14. Second And Third Pass Protocol

Second pass:

- reconcile workstreams against top-level file counts
- reconcile package list against workstreams
- reconcile journeys against public endpoints and operator commands
- reconcile migrations against store methods
- reconcile workflows against scripts and docs

Third pass:

- search for `todo`, `blocked`, `TBD`, `TODO`, `FIXME`, `panic`,
  unchecked errors, ignored lints, skipped tests, stale audit text, and
  docs-only claims
- search for files without workstream ownership
- search for findings without evidence and evidence without consumers
- challenge all severity assignments
- review the audit artifacts themselves for false completion
