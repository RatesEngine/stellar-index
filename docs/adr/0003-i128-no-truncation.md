---
adr: 0003
title: i128 / u128 values preserved end-to-end; never truncated to int64
status: Accepted
date: 2026-04-22
supersedes: []
superseded_by: null
---

# ADR-0003: i128 / u128 values preserved end-to-end; never truncated to int64

> **Reality note (2026-06-12, F-1353 / D2-10).** Two enforcement
> mechanisms claimed below are **not implemented**: there is no custom
> golangci analyzer flagging `int64` amount-shaped parameters
> (`.golangci.yml` has no such rule or plugin), and `make
> db-migrate-status` only prints migration state — it does not refuse
> `BIGINT` / `DOUBLE PRECISION` amount columns. The decision itself is
> in force and is enforced in practice by code review (CODEOWNERS on
> `internal/canonical/`) and the round-trip fixture tests in
> `internal/canonical/amount_test.go` (incl. the KALIEN-incident
> regression), which DO exist. Also: the public amount type lives in
> `pkg/client`, not the `pkg/types` path named below.

## Context

Soroban stores token quantities (balances, allowances, mint/burn
amounts, swap amounts, pool reserves, oracle prices, supply totals)
as **`i128` or `u128`** — two 64-bit words, `hi` and `lo`.

At the standard 7-decimal precision, any amount above
**~922 billion tokens** (i64 max ÷ 10⁷) overflows `int64`. This is
not theoretical: a real production incident observed in adjacent
tooling (2026-04-22) confirmed the blast radius.

> "The KALIEN balance is stored as an `i128` (two 64-bit words).
> The actual value 40,000,005,972,900,000,000 exceeds i64 max
> (~9.2×10¹⁸), so it's stored with `high=2,
> low=3106517825480896768`. Stellar Expert is only reading the low
> 64 bits, displaying 310,651,782,548.0896768 instead of the real
> 4,000,000,597,290."

Stellar Expert's own response confirmed their analytics DB uses
`int64` for most balances and that they were working on a fix at
the time, but had no committed ship date.

We also verified that `withObsrvr/cdp-pipeline-workflow`
contains this exact class of bug — its Soroswap router processor
reads `entry.Val.I128.Lo` and ignores `.Hi`, silently mis-recording
every high-value swap.

This is **not a tricky edge case.** It is **the most important
correctness invariant in the entire project.**

## Decision

Every `i128` or `u128` value on its journey from on-chain event
through our pipeline to the API response is preserved with full
128-bit precision. At each layer:

| Layer | Representation |
| ----- | -------------- |
| Soroban XDR | `xdr.Int128Parts` / `xdr.UInt128Parts` (upstream) |
| Go in-memory | `*big.Int` (via `math/big`) or `decimal.Decimal` with precision ≥ 38 |
| Postgres / TimescaleDB | `NUMERIC` (arbitrary precision) |
| JSON API output | **String** (never a JSON number — they're IEEE 754 doubles, 53-bit precision) |
| OpenAPI schema | `type: string`, `format: i128` (custom format tag) |

No code path in the repo is allowed to hold one of these values in
`int64`, `uint64`, `float32`, or `float64`. No exceptions.

Two's-complement sign handling on `Int128Parts` is delegated to
the helper in `internal/canonical/amount.go` (which follows the
verified-correct implementation in
`withObsrvr/stellar-extract/scval_converter.go`).

## Consequences

**Positive**

- Our pricing is **correct** where competitors are not. This is a
  real product differentiator for RWA and high-supply Soroban
  tokens.
- We cannot be surprised by a "the amount looks tiny but should be
  huge" incident.

**Negative**

- Every amount field in every struct takes more memory than an
  `int64` would. Negligible at our scale.
- JSON clients that naively parse our amount strings as numbers
  will truncate at their language's native precision. **This is
  on them to fix** — we document the issue clearly in API docs
  and SDK docs.

**Operational impact**

- Monitoring: amount-shape regression tests run on every release
  (corner cases in `internal/canonical/amount_test.go`). If the
  KALIEN-incident fixture ever stops round-tripping cleanly, the
  release is blocked.
- Alerting: any observed `errors.Is(err, canonical.ErrI128Overflow)`
  in production logs fires a SEV-1. It indicates an `int64`
  sneaking in somewhere.

**Downstream design impact**

- `pkg/types` public amount type is a distinct Go type, not a
  reused `*big.Int`. It cannot be conflated with plain-integer
  fields by accident.
- SDK-generated code respects the string-on-wire rule.
- Storage schema audits (`make db-migrate-status`) refuse any
  migration that adds a `BIGINT` or `DOUBLE PRECISION` column
  holding an amount.

## Alternatives considered

1. **Use `int64` and accept the overflow risk for large amounts.**
   Rejected outright. This is what Stellar Expert does today; we
   refuse to ship the same bug.
2. **Use `float64` in storage but render "carefully" in the API.**
   Rejected: precision loss happens before we ever render.
3. **Support i128 partially (storage OK, API exposes `int64` for
   "display purposes").** Rejected: split representation is the
   most common place precision loss gets reintroduced.

## Enforcement

- Lint rule in `.golangci.yml` (via a small custom analyzer) flags
  any function returning or accepting `int64` whose parameter
  name contains `amount`, `balance`, `reserve`, `supply`, `price`,
  `wei`, `stroop`, or `value`.
- Code review: CODEOWNERS requirement on `internal/canonical/`
  means @ash sees any change to the core amount type.
- Fixture tests: `TestAmountRoundTrip_KALIEN_Incident` et al. in
  `internal/canonical/amount_test.go`.

## References

- Reference correct implementation:
  `withObsrvr/stellar-extract/scval_converter.go` — verified-correct
  two's-complement `Int128Parts` handling.
- Counter-example (the bug we refuse to ship):
  `withObsrvr/cdp-pipeline-workflow` — its Soroswap router processor
  reads only the low 64 bits of an `i128`.
