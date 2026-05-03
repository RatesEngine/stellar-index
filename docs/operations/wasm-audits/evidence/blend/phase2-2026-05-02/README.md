# Blend audit — Phase 2 evidence (2026-05-02)

Per-contract wasm-history JSON output for the 11 Blend contracts,
extracted from the wide-net walker's full output on r1.

## Source

The walker run was: `ratesengine-ops wasm-history` against r1's
`galexie-archive` MinIO datastore, ledger range
`[50,457,424, 62,249,727]`, 8 parallel workers, 539 watched
contracts (the wide-net set), `-checkpoint-dir /tmp/walk-checkpoint`.

Walk runtime: 5h4m39s. Total ledgers scanned: 11,792,304.

## Files

- [`wasm-history-blend.json`](wasm-history-blend.json) — JSON
  array of `{contract, ranges}` for the 11 Blend contracts.
  Format matches the canonical `wasm-history` output shape.

The full 540-contract walker output remains on r1 at
`/tmp/wide-net-walk-3.json` for re-derivation if needed. The
per-worker JSONL checkpoints (200KB total) are at
`/tmp/walk-checkpoint/` on r1; `wasm-history-merge-jsonl` can
reconstitute the full JSON from those if the canonical output is
ever lost.

## Findings summary

- 11 / 11 Blend contracts present in the walk's watch list.
- 8 / 11 had at least one observed `update_current_contract_wasm`
  event in the walk window. The other 3 deployed before ledger
  50,457,424 and have not been upgraded since (per Phase 1's
  Soroban-RPC current-state query, their hash is `a41fc53d…` —
  matches the never-upgraded peers).
- 3 unique WASM hashes across all 11 contracts:
  - `a41fc53d…` — pool WASM (9 contracts)
  - `c1f4502a…` — backstop WASM (1 contract)
  - `31328050…` — factory WASM (1 contract)
- **Zero mid-life upgrades observed.** Each non-empty contract has
  exactly 1 range; the from_ledger is the first observation and
  the to_ledger is the worker's chunk boundary.

See [`../../../blend.md`](../../../blend.md) §"Phase 2 results" for the
full per-contract breakdown.
