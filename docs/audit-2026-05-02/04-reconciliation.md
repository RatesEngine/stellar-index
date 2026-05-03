# Mandatory Reconciliation Passes

## R1. Code vs Docs

Compare live code against:

- root docs
- reference docs
- runbooks
- architecture docs
- discovery archive

## R2. Code vs Tests

For each critical package, compare implementation risk to what tests
actually cover.

## R3. Tests vs CI

Check whether important suites are actually exercised by CI and local
verify flows.

## R4. Handlers vs OpenAPI

Check registered routes, params, status codes, and response envelopes
against `openapi/rates-engine.v1.yaml`.

## R5. Proposal / RFPs vs Implementation

Compare `docs/stellar-rfp.md`, `docs/freighter-rfp.md`, and
`docs/ctx-proposal.md` against current runtime code.

## R6. ADRs vs Implementation

Verify the repo still honors non-negotiable ADR constraints.

## R7. Monitoring / Systemd / Runbook Alignment

Compare deployed services, timers, alert rules, and operator runbooks
against code and CLI reality.

## R8. Prior Audit vs Current Snapshot

Use `docs/audit-2026-04-29` only as a delta source:

- which findings are still real
- which were remediated
- which new surfaces appeared
- whether remediation introduced drift
