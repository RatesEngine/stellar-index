---
adr: 0011
title: Three-domain supply algorithm — XLM hard-coded, classic from ledger entries, SEP-41 from event sums
status: Accepted
date: 2026-04-27
supersedes: []
superseded_by: null
---

# ADR-0011: Supply algorithm — total / circulating / max

> **Reality note (2026-06-12, F-1354 / D2-03).** The SEP-1
> `max_supply` precedence step and the `self_declared` API flag
> described below are **not wired into production**: `supply.Overlay`
> (`internal/supply/overlay.go`) has zero callers, so the SEP-1
> max_supply fallback never runs and `self_declared` is never stamped.
> The on-chain supply derivation (XLM / classic / SEP-41) is live; the
> issuer-declared overlay is dead code retained against future
> activation. The decision below is preserved as the original record.

## Context

The V2 spec (requirement F2.4 in
`docs/architecture/coverage-matrix.md`) requires the API to publish
`total_supply` / `circulating_supply` / `max_supply` for every
asset we index. The numbers feed market-cap, FDV, and supply-pct
fields on the asset-detail endpoint and the historical supply
chart.

Stellar's asset model has **three structurally different domains**
that need three different algorithms:

1. **Native XLM** — fixed: 50 B genesis lumens + ~1.8 B inflation
   pool, frozen by network vote in October 2019. Total supply
   doesn't move; only the SDF-reserve exclusion changes circulating.
2. **Classic credit assets** (`CODE:ISSUER`) — issuer-authoritative.
   Total supply is the sum of every unit the issuer has emitted that
   hasn't been burned, observable as the inverse of the issuer's
   balance + the trustline / claimable / LP / SAC-wrapped balances
   downstream. Reconstructed from ledger meta.
3. **SEP-41 Soroban tokens** — event-defined per the SEP-41 spec:
   `Σ mint − Σ burn − Σ clawback`. Indexed off contract events.

These can't share a single materialisation pipeline. Per ADR-0003
all amounts are `*big.Int` / `NUMERIC` end-to-end (i128 safety) and
strings on the wire.

This ADR is the immutable commitment to the supply-derivation policy.

## Decision

**Adopt three domain-specific supply algorithms with a shared
schema, plus an operator-configurable locked-set policy for the
circulating-supply derivation.**

### Algorithm 1 — Native XLM

- `total_supply` = hard-coded constant `50_001_806_812 * 10^7`
  stroops (50 B genesis + inflation pool, frozen 2019-10).
- `max_supply` = `total_supply`.
- `circulating_supply` = `total_supply − Σ(SDF reserve account
  balances)`. Reserve account list is config, not on-chain
  derivable. SDF publishes the list; we maintain a YAML version-
  controlled in the deployment repo and refresh it when SDF
  publishes changes.

No event-stream tracking; the numbers don't move except for
reserve-account balance changes (which our trustline-delta indexer
already observes).

### Algorithm 2 — Classic credit assets

- `total_supply` = `Σ trustline balances + Σ claimable balances +
  Σ LP-reserve pro-rata + Σ SAC-wrapped contract balances` for the
  asset. Reconstructed from Galexie ledger meta — we observe every
  `TrustLineEntry` / `ClaimableBalanceEntry` /
  `LiquidityPoolEntry` / SAC-contract-data delta and maintain a
  per-(asset, ledger) running total in the
  `asset_supply_history` hypertable.
- `max_supply` = `null` by default (classic issuers can always
  issue more). Two override paths:
  1. SEP-1 `[[CURRENCIES]].max_supply` from the issuer's
     `stellar.toml` — respected as a display value but flagged
     `self_declared: true` in the API response (not on-chain
     enforced).
  2. Operator override in the supply policy YAML.
- `circulating_supply` = `total_supply − Σ locked_set`. Default
  locked set: just the issuer's own balance. Operator may extend
  via YAML to include known reserve / treasury multisigs and
  vesting contracts. **LP-reserve balances are NOT excluded** —
  the underlying asset is still circulating; LP-token holders own
  it pro-rata.

### Algorithm 3 — SEP-41 Soroban tokens

- `total_supply` = `Σ mint.amount − Σ burn.amount − Σ
  clawback.amount` over the contract's lifetime, per SEP-41
  semantics. Indexed off the contract's events; running per-token
  total in `asset_supply_history`.
- `max_supply` — no canonical on-chain source. Sources, in order:
  1. SEP-1 `[[CURRENCIES]].max_supply` from the token's stellar.toml.
  2. Operator override.
  3. `null`.
- `circulating_supply` = `total_supply − Σ locked_set`. Default
  locked set: the token's admin account / contract balance (when an
  admin exists). Operator extends per-token.

### SAC-wrapped classics — both algorithms must agree

A SAC-wrapped classic asset (e.g. `CAS3…OWMA` for native XLM, or
the SAC contract address for `USDC:GA5Z…`) is simultaneously a
classic asset (Algorithm 2) and emits SEP-41 events (Algorithm 3).
We compute both. Cross-check: alert when they disagree by more
than 1 stroop.

### API + schema

- All supply fields are strings on the wire (i128 safety per
  ADR-0003).
- `supply_basis` field on the response identifies which policy
  produced the numbers (`"xlm_sdf_reserve_exclusion"`,
  `"issuer_exclusion"`, `"admin_exclusion"`, `"override"`,
  `"no_metadata"`).
- `null` for any field where we don't have a defensible value;
  document the convention as "we don't fabricate."
- Hypertable shape:
  ```sql
  CREATE TABLE asset_supply_history (
    time              TIMESTAMPTZ NOT NULL,
    asset_key         TEXT NOT NULL,    -- "XLM" | "CODE:G…" | "C…"
    total_supply      NUMERIC NOT NULL,
    circulating_supply NUMERIC NOT NULL,
    max_supply        NUMERIC,           -- NULL when uncapped
    basis             TEXT NOT NULL,
    ledger_sequence   BIGINT NOT NULL
  );
  SELECT create_hypertable('asset_supply_history', 'time');
  CREATE UNIQUE INDEX ON asset_supply_history (asset_key, ledger_sequence);
  ```
  Append-only; latest row per `asset_key` is the queryable current
  state. Time-bucketed for historical queries.

## Consequences

- **Positive — covers F2.4 (Freighter V2 market-cap fields)** end-
  to-end without inventing a new ingest path. Every domain-specific
  data source we need is already captured per the discovery audit
  (Galexie ledger entries for classic, SEP-41 events for Soroban,
  configured constants for XLM).

- **Positive — the no-fabrication policy makes degradation honest.**
  When we don't have a defensible `max_supply` (uncapped issuer +
  no stellar.toml + no operator override), we publish `null` rather
  than guess. Consumers handle `null` explicitly.

- **Positive — operator-configurable locked-set lets each
  deployment match its compliance posture.** A deployment focused
  on Freighter end users may include only the issuer-balance
  exclusion; a deployment serving institutional customers may
  exclude treasury multisigs + vesting contracts per the asset's
  formal disclosure. Same code path; just YAML.

- **Negative — three algorithms means three test surfaces and
  three bug classes.** Mitigated by the SAC-wrapped cross-check
  (when the same asset is observable both ways, the sums must
  match within 1 stroop). Disagreement triggers an alert.

- **Negative — the locked-set YAML is operationally fiddly.** Every
  asset-of-interest needs a curated entry to get a meaningful
  circulating-supply. Without curation, we default to issuer-only
  exclusion and document the policy in the API response so
  consumers know not to trust the absolute number.

- **Operational impact — adds `asset_supply_history` hypertable +
  per-source supply-update emitters.** Storage is small (a few
  thousand assets × a few writes/day = MB-scale). The ingest hot
  path is unchanged; supply derivation is a downstream consumer
  of the trustline / events streams.

- **Downstream design impact — market-cap / FDV / supply-pct fields
  in the API depend on this hypertable.** Aggregation policy
  (combining supply with VWAP price) is straightforward but
  documented in `aggregation-plan.md` once this lands.

## Alternatives considered

1. **Single unified algorithm** — rejected. The three domains have
   incompatible truth sources (constant vs ledger entries vs
   events); a unified path would need to special-case each anyway,
   so make the structure explicit.

2. **Trust upstream aggregators (CoinGecko / CMC) for circulating
   supply** — rejected. We're being graded on independence per the
   spec; importing a third-party number is what aggregators are for,
   and we're explicitly NOT one. Plus the third parties' policies
   for "locked" are opaque and inconsistent across assets.

3. **Hard-code the locked-set per asset (no YAML)** — rejected.
   Treasury multisigs + vesting contracts move; a code change
   per update is too brittle for production. YAML in the
   deployment repo is the right grain.

4. **Don't publish `max_supply` at all** — rejected. The spec requires
   it for FDV; consumers have to display "unknown" somehow, and
   `null` is a clearer signal than "0" or omitting the field.

5. **Compute max_supply from on-chain auth flags** (e.g.
   `auth_immutable + auth_revocable + known burn-signer`
   patterns) — considered as an enhancement but rejected for v1
   because the heuristic is brittle and produces false positives.
   Operator override + SEP-1 declaration are sufficient signals;
   automatic derivation is a v2 feature gated on a discovery PR
   that audits the heuristic across all classic issuers on
   pubnet.

## References

- [`docs/architecture/supply-pipeline.md`](../architecture/supply-pipeline.md)
  — the supply pipeline this ADR's policy drives.
- [`docs/architecture/coverage-matrix.md`](../architecture/coverage-matrix.md)
  §F2.4 — the requirement this ADR closes.
- [ADR-0003](0003-i128-no-truncation.md) — i128 invariant binding
  every amount in this ADR.
- [ADR-0010](0010-off-chain-fiat-representation.md) — off-chain
  fiat asset representation (out of scope here; off-chain
  currencies don't have a "supply" we publish).
- SEP-1 §[[CURRENCIES]] — the `max_supply` declaration we honour
  for the self-declared overlay.
- SEP-41 — the Soroban token-contract spec defining
  `mint`/`burn`/`clawback` event semantics.
