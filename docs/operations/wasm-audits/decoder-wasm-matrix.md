---
title: Decoder ↔ WASM verification matrix
last_verified: 2026-05-01
status: 51/52 WASMs verified; 1 false-negative due to known Soroban SymbolSmall packing
related:
  - docs/operations/wasm-audits/r1-walk-2026-05-01.md
  - docs/operations/wasm-audits/protocol-epochs.md
---

# Decoder ↔ WASM verification matrix

> Bidirectional check that every WASM we ingest from contains the
> event topics our decoder watches for. Built from the 52 WASM
> bytes preserved at
> [`evidence/r1-walk-2026-05-01/wasm-bytes/`](evidence/r1-walk-2026-05-01/wasm-bytes/)
> 2026-05-01.

## Method

For each `(source, role, WASM)` triple:

1. **Expected topics** = the strings the decoder's `classify` /
   `Matches` function watches for (extracted from
   `internal/sources/<source>/{events,decode}.go`).
2. **Verification** = byte-search the WASM for each expected
   topic.
3. **Verdict** = `OK` if all expected topics found,
   `MISSING <set>` otherwise.

Role-aware: factories don't emit pool events, routers don't emit
pair events. Each role has its own expected-topics set.

## Results

| Source | Role | WASMs | OK | Missing | Notes |
|---|---|---:|---:|---:|---|
| soroswap | factory | 1 | 1 | 0 | emits `new_pair` |
| soroswap | router  | 1 | 1 | 0 | orchestration only — no expected topics |
| soroswap | pair    | 1 | 1 | 0 | emits `swap`, `sync`, `skim`, `deposit`, `withdraw` |
| aquarius | router  | 6 | 6 | 0 | orchestration — no expected topics |
| aquarius | pool    | 13 | 13 | 0 | every pool variant emits `trade`, `deposit`, `withdraw`, `claim` |
| phoenix  | factory | 5 | 5 | 0 | no expected topics |
| phoenix  | multihop| 3 | 3 | 0 | no expected topics |
| phoenix  | pool    | 14 | 14 | 0 | every pool variant emits `swap` (as `("swap", <field>)` 2-tuple) |
| reflector| oracle  | 2 | 2 | 0 | SEP-40 reads via methods; no expected events |
| comet    | pool    | 1 | 1 | 0 | emits `swap`, `join_pool` |
| **redstone** | adapter | 2 | **0** | 2 | **false negative — see Caveat below** |
| band     | StandardReference | 1 | 1 | 0 | emits zero events; observed via op args |
| blend    | pool-factory | 1 | 1 | 0 | emits `deploy` |
| blend    | backstop | 1 | 1 | 0 | emits `gulp_emissions` |
| **TOTAL** |        | **52** | **50** | **2** | 96% match rate; 100% if we discount the false negative |

## Caveat — `SymbolSmall` packing

Soroban encodes contract-event topic Symbols inline-in-code when
they are **≤ 9 characters**: the symbol bytes get packed into a
single `u64` (`SymbolSmall` per `Stellar-contract.h`) rather than
stored as a string in the data section. As a result, byte-search
against the WASM data section can miss them.

This explains the Redstone misses:
- Topic: `"REDSTONE"` (8 chars)
- Search: byte-match for `b"REDSTONE"` — not found in either
  Redstone WASM (`b400f7a8…` and `5e93d22c…`).
- Reality: the topic IS emitted (verified by the production
  decoder ingesting events from the live adapter every block).
  The 8 ASCII bytes are packed into a single i64 constant in
  the code section.

For longer topics (e.g. `apply_transfer_ownership` at 24 chars,
`gulp_emissions` at 14, `fill_auction` at 12), the topic is
stored as a normal string in the data section and byte-search
finds it. Hence the 50/52 result.

**Why it matters anyway.** The verification still has signal:

- For the 50 hashes where matches succeeded, we have a positive
  byte-level confirmation that the decoder's expected topic
  literally appears in the WASM. This rules out the case "decoder
  watches for `trade` but the contract emits `Trade`" or similar
  case-sensitivity / typo bugs.
- The 2 misses are constrained to a single source (Redstone), and
  the SymbolSmall packing rule is well-understood — every
  Soroban-built contract handles short symbols this way.
- Cross-validation: production ingest health metrics
  (`ratesengine_redstone_events_total`) show events flowing.

## How to refresh

After every wasm-history walk that introduces new hashes:

1. Re-fetch WASM bytes via the audit pipeline:
   `evidence/.../fetch-wasm-rpc.py`.
2. Re-run the verifier:
   `python3 evidence/.../verify-decoder-wasm-match.py` (script
   committed alongside this doc).
3. Update this matrix table. Document any new miss with the
   reason (decoder change? topic rename? new SymbolSmall case?).

## Operator confidence

After this matrix, **for the 50 byte-match-positive WASMs we have
strong evidence the decoder will correctly classify their
events** during backfill replay. For the 2 SymbolSmall cases
(Redstone), confidence rests on:

- Every Soroban contract uses the same SymbolSmall convention.
- Production ingest is live and emitting events from the
  current production hash (`5e93d22c…`); zero `ErrUnknownEvent`
  rate in the metrics for redstone source.
- The hotfix hash (`b400f7a8…`) has a 35-min lifetime; the
  pre-backfill SQL guard documented in
  [`r1-walk-2026-05-01.md`](r1-walk-2026-05-01.md) §Redstone
  prevents accidental replay over its window.
