---
adr: 0039
title: Soroban contract current-state reader — read-time decode from the lake
status: Accepted
date: 2026-06-18
supersedes: []
superseded_by: null
---

# ADR-0039: Soroban contract current-state reader

## Context

Our protocol analytics are **event-derived**: we decode the events a
contract emits (`internal/sources/<protocol>`, projected via ADR-0031)
and roll them up. That gives flow (volume, position deltas, auctions)
but NOT **current on-chain state** — a contract's storage entries.

The felt gap is **lending**. Blend's per-pool TVL, utilisation, and
supply/borrow APY live in the pool contract's reserve storage
(`ReserveData` b_rate/d_rate/b_supply/d_supply; `ReserveConfig`
interest-rate-model params), not in any event. Until now
`/v1/lending/pools` could only serve a **window net-flow proxy**
(30-day signed sums of position events), which it explicitly labelled
as "not TVL" and pointed at this reader (the "#84" follow-up). The same
shape recurs for DeFindex vault composition, AMM pool reserves, etc.

Soroban contract storage IS captured: `stellar.ledger_entry_changes`
(ADR-0034 / ADR-0038 Phase C) records every `contract_data` entry with
its full `key_xdr` (LedgerKey) + `entry_xdr` (the value). On r1 today:
~267 M contract_data rows over the recent ledger window.

## Decision

Add a **Soroban contract current-state reader**: a read path that, for a
known contract + a known storage key, fetches the **latest** matching
`contract_data` entry from the lake and decodes its value with a
**per-protocol decoder**.

Binding choices:

1. **Read-time, not materialised (no new worker / table / backfill).**
   `ledger_entry_changes` already holds the latest value per
   `key_xdr` (newest `ledger_seq` wins). We build the EXACT `key_xdr`
   for the storage key we want (same technique as
   `wasm_lake_reader.instanceKeyXDR`) and `WHERE key_xdr = ? ORDER BY
   ledger_seq DESC LIMIT 1` — a point lookup, no scan. Current state is
   live-captured; this needs no contract-storage backfill. (A future
   ADR may add a materialised `*_pool_state` hypertable + worker if
   read-time fan-out becomes a latency problem; we start simple.)

2. **Per-protocol decoders live with the source.** Blend's storage
   layout is Blend's knowledge: `internal/sources/blend/storage.go`
   decodes `ReserveData` / `ReserveConfig` / `PoolConfig` by field
   NAME (`scval.MapField`), mirroring `storage.rs`. The
   interest-rate model is ported faithfully from the contract's
   `interest.rs` / `reserve.rs` into `internal/sources/blend/interest.go`
   and **validated bit-for-bit against the contract's own unit-test
   vectors** (`interest_test.go`) — the rounding (ceil/floor fixed-point)
   matches `soroban-fixed-point-math`, so our APY equals the chain's.

3. **What we compute, and our confidence:**
   - **TVL** (supplied/borrowed underlying = b_supply×b_rate /
     d_supply×d_rate, × USD price): exact — pure arithmetic on certified
     state.
   - **Utilisation** (liabilities/supply): exact — ported from
     `reserve.rs::utilization`.
   - **APY** (borrow = `calc_accrual`'s `cur_ir`; supply = borrow ×
     util × (1 − backstop_take)): exact rate model, validated against
     the contract test vectors. The backstop take rate comes from the
     pool's instance-storage `PoolConfig`.

4. **Honest provenance.** A figure derived from contract storage is
   labelled current-state TVL/APY; the event-derived window proxy stays
   available and clearly distinguished. We never blend (pun intended)
   the two into one ambiguous number.

## Consequences

- **Blend gains real per-pool current-state TVL / utilisation /
  supply+borrow APY** without a backfill — it reads the latest reserve
  entries from the lake at request time and prices them.
- **The pattern generalises.** Any protocol whose state we want
  (DeFindex vault holdings, AMM reserves, oracle config) follows the
  same shape: build the storage key → point-lookup the lake → decode
  with a per-protocol decoder. New decoders are additive.
- **Coverage = the live contract-storage capture window.** A reserve
  that hasn't been touched (no entry update) since capture began won't
  be found; in practice active pools update reserves on nearly every
  interaction, so current state is present. A pool-storage backfill
  (re-derive contract_data over history) would extend HISTORICAL state
  series — deferred until there's demand for "TVL over time".
- **Read-time cost.** A per-pool, per-reserve point lookup is cheap
  (keyed), but a pool with many reserves fans out N lookups. We cache
  the result (the existing lending-pools cache TTL) and can promote to a
  materialised projection later if needed.

## Status — Accepted 2026-06-18

Implemented in two parts: the decoders + interest model (this ADR's
core, fully unit-tested) ship first; the lake reader + `/v1/lending/pools`
wiring + r1 verification follow in the same series. The window net-flow
proxy fields remain for continuity and are explicitly distinguished from
the new current-state fields.
