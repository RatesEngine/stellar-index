---
adr: 0038
title: Network explorer (full Stellar + Soroban) over the certified lake
status: Accepted
date: 2026-06-14
supersedes: null
superseded_by: null
---

# ADR-0038: Network explorer (full Stellar + Soroban)

## Context

Stellar Index began as a pricing API and grew a protocol explorer
(coins / markets / DEXes / oracles / lending / issuers). The standing
product vision (CLAUDE.md) is a **comprehensive blockchain explorer**
— classic/native *and* Soroban. This ADR commits to that and records
the architecture + phased build, because a one-time discovery changed
the cost calculus dramatically.

**The discovery:** the ClickHouse Tier-1 lake (ADR-0034) already holds
the **entire chain to genesis, contiguous and hash-chain-verified** —
not just pricing-relevant slices:

| Lake table (`stellar.*`) | Range | Rows (2026-06-14) |
|---|---|---|
| `ledgers` | 2 → tip (0 gaps) | 63 M |
| `transactions` | 3 → tip | 10.1 B |
| `operations` | 3 → tip | 23.4 B |
| `operation_results` | 3 → tip | 23.4 B |
| `contract_events` | 3 → tip | 12.0 B |
| `ledger_entry_changes` | **empty** | 0 |

Ingesting + storing the full verified chain (23 B operations to 2015) is
the single most expensive component of any block explorer — and it is
**already done**. The remaining work is *serving* that data, deriving
account *state* (the one missing table), and *rendering* it. This is a
~3–4.5-month effort on top of today's product, not a re-platforming.

## Decision

Build a network explorer as a **read layer over the existing lake**, in
four phases, served by new `/v1` endpoints and rendered by new explorer
UI routes. Postgres remains the pricing served-tier (ADR-0034); the
explorer reads ClickHouse directly through a new
`internal/storage/clickhouse` reader, never Postgres (the chain history
is not in Postgres and never will be — billions of rows).

### Invariants (bind every phase)

- **i128 never truncates** (ADR-0003): amounts in op/entry decode are
  `*big.Int` → strings on the wire.
- **Explorer reads ClickHouse, not Postgres.** New read methods live in
  `internal/storage/clickhouse`; the API wires a `ChExplorerReader`
  alongside the existing Postgres `HistoryReader`.
- **XDR→JSON decode is centralised** in `internal/xdrjson` (new), built
  on `go-stellar-sdk/xdr` — one decoder per op type / entry type /
  result, reused by every endpoint. No ad-hoc decode in handlers.
- **No Horizon** (ADR-0001). We decode raw XDR from our own lake.
- **Closed-bucket / region-stable** serving conventions (ADR-0015) do
  not apply to immutable history (a closed ledger is final); explorer
  responses are cacheable indefinitely by (ledger_seq | tx_hash).

### Phase A — Read-API over the lake (the fast win)

Endpoints, all backed by existing lake tables (no new ingest):

- `GET /v1/ledgers` — recent ledgers (paged, descending).
- `GET /v1/ledgers/{seq}` — ledger header + tx/op counts + nav.
- `GET /v1/ledgers/{seq}/transactions` — txs in a ledger.
- `GET /v1/tx/{hash}` — transaction: envelope, memo, fee, result, and
  its operations (decoded) + emitted contract events.
- `GET /v1/operations` — browse/filter (by ledger, type).
- `GET /v1/contracts/{c}` — contract activity: events + invocations
  (from `contract_events` + `operations` op_args).
- `GET /v1/search?q=` — dispatch by strkey shape (G / C / 64-hex
  tx-hash / ledger-seq / asset id) to the right detail endpoint.

The bulk of Phase A is **XDR→JSON decode breadth**: ~30 classic op
types, tx envelopes/memos, and op results, into clean JSON. This phase
alone is a usable explorer (any ledger / tx / contract).

### Phase B — Account-scoped history + participant index

The #1 explorer query — "everything involving account G" — cannot be a
`WHERE source_account = G` scan: a payment *to* G, an offer crossing G,
a claimant, etc. are non-source participants. Build a
`stellar.operation_participants` table (every account touched per op,
derived from op body + results) as a ClickHouse MV / derive over the
23 B-op history, then:

- `GET /v1/accounts/{g}/transactions|operations|payments`.

### Phase C — Account state (the expensive tail)

Populate the empty `stellar.ledger_entry_changes` (the
`LedgerEntryChangeDecoder` hook exists; backfill genesis→tip — billions
of rows, weeks of compute, meaningful storage). Current state via a
`ReplacingMergeTree` keyed on entry-key, versioned by `ledger_seq`.
Decode all entry types (Account / TrustLine / Offer / Data /
ClaimableBalance / LiquidityPool / ContractData / ContractCode):

- `GET /v1/accounts/{g}` — balances, signers, thresholds, sequence,
  flags, sponsorship.
- `GET /v1/accounts/{g}/offers`, `/trustlines`, `/data`.
- `GET /v1/contracts/{c}/state` — current contract data entries.
- Offer book per pair.

Balance exactness (reserves, liabilities, sponsorship) is the fiddly
part and gets dedicated tests.

### Phase D — Explorer UI

New routes in `web/explorer`: `/ledger/[seq]`, `/tx/[hash]`,
`/account/[g]`, `/contract/[c]`, `/operations`, and a search bar that
accepts G / C / tx-hash. Dynamic entity pages are **static shells that
fetch the API client-side at runtime** (the explorer already fetches
100 % client-side; no SSR needed). The static-export model is preserved.

## Consequences

- **Positive:** a full explorer is unlocked at ~20–30 % of from-scratch
  cost because the verified history substrate already exists. Phase A
  ships a real explorer in ~1 month.
- **Cost / risk:** storage — `ledger_entry_changes` + the participant
  index add substantial disk on top of the 23 B-op lake (needs ZFS
  headroom); Phase-C backfill is weeks of compute; XDR decode must be
  exhaustive + i128-correct; account-balance exactness is subtle.
- **Sequencing:** A → B → D-for-A/B can ship and be useful before C.
  C (account state) is the expensive tail and can trail.

## Status of build

- **Phase A unit 1 (shipped + deployed):** `clickhouse.ExplorerReader` +
  `GET /v1/ledgers`, `/v1/ledgers/{seq}`, `/v1/ledgers/{seq}/transactions`.
  Live on r1, verified (prev_hash chains, total_coins as string).
- **Phase A unit 2a (shipped + deployed):** `internal/xdrjson` decoder
  (~16 classic op types + invoke_host_function, raw fallback) +
  `GET /v1/operations?ledger=`. Decode verified live against real ledger
  ops (payments / offers / path-payments / change_trust).
- **Phase A unit 2b (shipped + deployed):** `GET /v1/tx/{hash}` (summary
  + decoded ops w/ result codes + events). Added a `tx_hash` bloom
  skip-index to `stellar.transactions` + MATERIALIZE'd across all history
  (~2.5 min). Verified live on a 2022 tx (ledger 40 M) — cross-history
  hash lookup is fast.
- **Phase A unit 3 (shipped + deployed):** `GET /v1/search` (query
  dispatcher) + `GET /v1/contracts/{c}` (per-contract event activity, via
  a `contract_id` bloom skip-index on `stellar.contract_events`,
  MATERIALIZE'd).
- **Phase A — read surface COMPLETE + live:** ledgers, transactions,
  operations (decoded), tx, contracts, search. Remaining Phase-A polish:
  OpenAPI spec entries for the new paths.
- **Phases B–D:** planned per the roadmap above (participant index,
  account state + `ledger_entry_changes` backfill, explorer UI).
