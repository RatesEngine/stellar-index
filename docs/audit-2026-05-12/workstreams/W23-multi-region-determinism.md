# W23 — Multi-region determinism (R2, R3)

## Scope

Audit the multi-region invariants — even though only R1 is
deployed today.

In scope:
- ADR-0008 (HA topology), ADR-0015 (last-closed-bucket),
  ADR-0016 (per-region storage strategy)
- `docs/operations/r1-deployment-state.md`
- `docs/operations/r2-deployment-state.md`
- `docs/operations/r3-deployment-state.md`
- `docs/operations/multi-region-cutover.md`
- `docs/architecture/ha-plan.md`
- `docs/architecture/infrastructure/multi-region-topology.md`
- `cmd/ratesengine-ops/cross_region_check.go`,
  `cross_region_monitor.go`
- `scripts/dev/verify-cross-region.sh`

Out of scope:
- live R2 / R3 state (EX-1208 — they don't exist yet)

## Inputs

- `04-reconciliation.md` R12 (R1 live vs documented; this
  workstream covers the R2/R3 *claims*)
- ADR-0015 contract: cross-region byte-identical for the same
  closed bucket

## ADR-0016 invariant audit

Per ADR-0016 the per-region storage strategy is:

- R1 (Hetzner): full-mirror integrity leader
- R2 (AWS): reads galexie data direct from
  `aws-public-blockchain` S3, no local mirror
- R3 (Vultr): hybrid — galexie-archive on Vultr Object Storage

Verify each row against `r2-deployment-state.md` and
`r3-deployment-state.md`. Verify the *tooling* would honour
those constraints today (not blocked by hardcoded R1-only
paths).

## ADR-0015 invariant audit

`/v1/price` (and adjacent) serves the last *closed* bucket. By
construction, two regions reading the same Timescale + Redis
should return byte-identical envelopes for that bucket.

Audit:

- `internal/api/v1/price.go` reader — does it use bucket
  boundaries that are clock-derived (region-skew risk)?
- `internal/storage/timescale/aggregates.go` — closed-bucket
  selection
- `internal/cachekeys/*` — region-aware? region-naïve?
- continuous aggregate refresh policy region-uniform?

## Cross-region tooling

| Tool | Behaviour against R1-only today | Failure mode | Status |
| --- | --- | --- | --- |
| `cmd/ratesengine-ops/cross_region_check.go` | | | |
| `cmd/ratesengine-ops/cross_region_monitor.go` | | | |
| `scripts/dev/verify-cross-region.sh` | | | |

For each: verify it gracefully reports "only R1 available"
rather than panicking.

## R2 / R3 deployment-state docs

For each:
- Honest about not-deployed state
- Lists the R1 → R2/R3 deltas (per ADR-0016)
- Documents what would break if naïvely cloned from R1
- Documents secret/role provisioning for the new region

## Multi-region cutover plan

`docs/operations/multi-region-cutover.md`:
- step-by-step playbook
- includes pre-cutover smoke
- includes rollback path
- includes DNS TTL considerations

## Adversarial vectors

- C1.* storage layer (timescale primary down)
- C3.* network layer (Cloudflare cache poisoning during
  cutover)
- E3.* public-flip path

## Cross-workstream dependencies

- W11 owns `/v1/price` reader
- W14 owns cross-region drift alert + runbook
- W18 owns ansible roles for new region
- W21 owns R1 live state
- W22 owns launch readiness

## Closure criteria

- ADR-0016 invariant table complete
- ADR-0015 reader audit complete
- Cross-region tooling table complete
- R2 / R3 docs verdicts captured
- Cutover plan verdict captured
