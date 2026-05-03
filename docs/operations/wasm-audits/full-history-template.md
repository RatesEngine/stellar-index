---
title: Audit doc update template — v2 full per-instance WASM history
last_verified: 2026-05-03
status: template — applied to each source as v2 audit lands
---

# Per-source v2 audit update template

This template documents the structural shape each source's audit
doc takes once the v2 full-instance WASM history walk lands. The
v1 audits (2026-04-29, PRs #263–#270) captured each
factory/router/oracle's WASM history + each currently-deployed
pool/pair contract's *current* WASM. The v2 follow-up adds:

1. **Per-instance upgrade history.** Each pool/pair's full WASM
   timeline (every `update_current_contract_wasm` event observed
   for that contract from genesis through the audit's `to`
   ledger).
2. **Bytes for every unique hash.** Including hashes that have
   been evicted from current ledger state — fetched via the
   `extract-wasm-from-galexie` subcommand against r1's full
   archive.
3. **Disassembly per unique hash.** `wasm2wat` text-form output,
   with the `e.0` (contract_event) call sites called out and the
   topic+body XDR construction traced per call.

## Template sections to add (after each source's "Per-hash review findings")

### v2 — Full per-instance WASM history

> **Walk source**: `r1:/var/log/wasm-history-full.json`
> **Walk parameters**: `-from 50457424 -to 62296694 -parallel 8 -contracts <532-contract list>`
> **Walked at**: 2026-04-29

#### Per-instance timeline matrix

For each instance contract under this source, the matrix shows
every WASM hash that contract ever ran, with the active ledger
range:

| contract | hash range 1 | hash range 2 | … |
| --- | --- | --- | --- |
| `<addr>` | `<hash>:[from,to]` | `<hash>:[from,to]` | |

(Stable order by C-strkey for deterministic output.)

#### Unique-hash inventory across all instances

| hash (first 16) | first seen | last seen | instance count | bytes source |
| --- | --- | --- | --- | --- |
| `<hex>` | `<ledger>` | `<ledger>` | N | RPC fetch / galexie LCM extract |

### v2 — Disassembly per unique hash

For each unique hash in the inventory above, a per-hash subsection:

#### `<hash-first-16>`

- **Bytes source**: `RPC fetch (mainnet.sorobanrpc.com)` or
  `galexie LCM extract (r1, install ledger N)`.
- **Size**: `N bytes`.
- **Contract spec diff vs. canonical (this source's current production WASM)**:

  ```diff
  <relevant interface diff>
  ```

- **Event-emit call sites traced**:

  ```
  func: <name>      → e.0(topic_addr, topic_len, body_addr, body_len)
                    topic prepared at <wat-line>, ScVec of:
                      [0] ScSymbol "<name>"
                      [1] ...
                    body prepared at <wat-line>, ScVec/ScMap of:
                      ...
  ```

- **Decoder-compatibility verdict**: `compatible` / `divergent
  (detail)` / `not-decoder-relevant`.

### v2 — Decision

For sources where every unique hash is `compatible` and the
walk found no upgrade events that would have produced
incompatible hashes, the v1 `BackfillSafe: true` flip is
**confirmed deterministically**. For sources where any hash
diverges, the v2 audit ships either:

- A decoder fix that handles both shapes (gated by hash at
  decode time), and BackfillSafe stays `true`, OR
- A backfill range cutoff in the source's metadata that refuses
  replay of pre-divergent ranges, and BackfillSafe stays `true`
  for the audited window only, OR
- BackfillSafe flips back to `false` until the decoder fix lands
  (worst case).

## Process to apply this template

1. Wait for the full walk (`r1:/var/log/wasm-history-full.json`)
   to complete.
2. For each source, extract the per-instance timeline rows from
   the walk JSON (filter by contract IDs from
   `internal/sources/<source>/events.go` + the per-source pool
   list at `/tmp/wasm-audit/pools-<source>.txt`).
3. Compute unique-hash inventory across each source's instances.
4. For each unique hash:
   - Fetch bytes via `stellar contract fetch --wasm-hash` first.
   - If RPC returns "Contract Code not found", the WASM is
     evicted; fall back to
     `ratesengine-ops extract-wasm-from-galexie` against r1.
   - Run `wasm2wat` to get text form.
   - Use `stellar contract info interface` + `strings` for the
     interface + symbol-table view we already use in v1 audits.
   - Trace `e.0` call sites in the WAT (look for `(call $e.0 …)`)
     and read back the topic/body construction.
5. Update the source's audit doc with the v2 sections.
6. Update `last_verified` in the audit doc's frontmatter.
7. If any hash diverges, ship the decoder fix in the same PR.

## CI hook (optional, future)

A `scripts/ci/lint-wasm-audits.sh` could:

- Re-fetch each unique hash listed in each audit doc.
- Re-hash and confirm match (catches stellar-rpc / mainnet
  tampering, vanishingly unlikely).
- Re-run `stellar contract info interface` and confirm no diff
  vs the audit doc's recorded interface SHA.
- Re-disassemble and confirm no diff vs the audit doc's recorded
  WAT SHA.

This is v3 follow-up scope.

## See also

- Phase-1 audit (originating this whole work):
  [`docs/architecture/contract-schema-evolution.md`](../../architecture/contract-schema-evolution.md)
- Per-source v1 audits: this directory's `<source>.md` files.
- Subcommand:
  `cmd/ratesengine-ops/wasm_extract.go` (`extract-wasm-from-galexie`).
- Tool: `cmd/ratesengine-ops/main.go` `wasm-history` subcommand.
