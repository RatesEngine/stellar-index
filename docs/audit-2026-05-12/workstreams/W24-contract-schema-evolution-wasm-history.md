# W24 — Contract schema evolution + WASM history

## Scope

Every Soroban contract upgrade risk + the audit trail that
makes backfill safe.

In scope:
- `docs/architecture/contract-schema-evolution.md`
- `docs/operations/wasm-audits/*` (per-source audit log)
- `docs/operations/wasm-audits/decoder-wasm-matrix.md`
- `docs/operations/wasm-audits/protocol-epochs.md`
- `migrations/0017_create_wasm_history.up.sql`
- `cmd/ratesengine-ops/wasm_extract.go`,
  `cmd/ratesengine-ops/wasm_history.go` (test only?)
- `internal/sources/external/registry.go` `BackfillSafe` flag
  per source
- decoder-level WASM-version dispatch
- `configs/audit/wasm-walk-contracts.yaml`
- live R1 walker history
  (`docs/operations/wasm-audits/r1-walk-2026-05-01.md`,
  `walker-investigation-2026-05-01.md`)

## Inputs

- CLAUDE.md "Soroban DeFi contracts upgrade in place" surprise
- `inventory/source-decoder-inventory.md`

## Per-source WASM audit row

| Source | Current WASM hash | Prior hashes (range) | Audit doc | Audit verdict | BackfillSafe (claim) | BackfillSafe (verified) | Status |
| --- | --- | --- | --- | --- | --- | --- | --- |
| soroswap | | | `soroswap.md` | | | | |
| aquarius | | | `aquarius.md` | | | | |
| phoenix | | | `phoenix.md` | | | | |
| comet | | | `comet.md` | | | | |
| blend | | | `blend.md` (closed 05-02) | | | | |
| reflector | | | `reflector.md` | | | | |
| redstone | | | `redstone.md` | | | | |
| band | | | `band.md` | | | | |

For each: verify the audit doc covers every WASM version that
has run for the contract address since deployment (not just
"current").

## Decoder WASM matrix vs registry

Cross-reference:

- `docs/operations/wasm-audits/decoder-wasm-matrix.md` — the
  canonical "what we trust to decode" table
- `internal/sources/external/registry.go` `BackfillSafe` per
  source
- per-decoder code: how it dispatches across WASM versions
  (map field name lookup, topic[0] symbol match, never by
  contract address)

## Migration 0017 schema audit

`migrations/0017_create_wasm_history.up.sql`:

- columns
- index coverage
- write path: `wasm_extract` ops command
- read path: any?
- retention policy

## WASM extract / history operator commands

- `cmd/ratesengine-ops/wasm_extract.go` — extracts WASM bytes
  from network for inspection
- trust boundary: which RPC / archive does it pull from?
- per-WASM-hash storage layout

## Configs/audit/wasm-walk-contracts.yaml

- list of contracts the walker iterates
- frequency
- output

## Adversarial vectors

- A1.9 WASM upgrade mid-backfill (decoder reads field by name,
  field renamed in new WASM)
- A2.9 Reflector contract upgraded with new field at same key

## Cross-workstream dependencies

- W07 owns per-source decoder + caveat tests
- W08 owns external-source registry (where BackfillSafe lives)
- W09 owns migration 0017
- W13 owns operator-side `wasm_extract` + `wasm_history`
- W16 owns `docs/operations/wasm-audits/*` doc-truth pass

## Closure criteria

- Per-source WASM audit row complete for every Soroban source
- Decoder WASM matrix vs registry vs decoder code triangulated
- Migration 0017 audit complete
- Operator commands evaluated
- Adversarial WASM-upgrade test scenario documented
