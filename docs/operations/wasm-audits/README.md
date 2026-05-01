---
title: Per-WASM-hash decoder audits — procedure
last_verified: 2026-04-27
status: living procedure
---

# WASM audits

This directory holds one audit log per on-chain Soroban source. Each
audit log is the evidence trail for a single
`internal/sources/external.Registry` `BackfillSafe` flag flip from
`false` → `true`. Until the audit lands, the source's
`ratesengine-ops backfill` runs are refused; once the audit shows
the decoder handles every WASM version that ran for the replay
range, the flag flips in the same PR.

The constraint behind all of this is the trap CLAUDE.md flags as
"Soroban DeFi contracts upgrade in place" — Soroswap, Aquarius,
Phoenix, Comet, Reflector\*, Redstone, and Band can each
`update_contract` at the same address, and event body schemas /
topic shapes can change across an upgrade. Live ingest only ever
sees the current WASM; a since-inception backfill replays every
prior version. Decoding old events with a current-only decoder
silently produces wrong trades.

## Files in this directory

- `README.md` — this file. Procedure + checklist.
- `soroswap.md` — Soroswap audit (in progress).
- (`aquarius.md`, `phoenix.md`, `comet.md`, `reflector-{dex,cex,fx}.md`,
  `redstone.md`, `band.md` — to land per source.)

## Procedure

### 1. Identify the contracts to audit

For each source, the audit covers:

- The **factory contract** (if any) — emits `new_pair` / pool-creation
  events; topic schema changes here add or remove pools we'd want to
  ingest.
- The **router contract** (if any) — emits routed-swap aggregation
  events, and is where multi-hop swap accounting lives.
- The **pair / pool contract WASM hash** — the per-instance contracts
  that emit the actual `swap` events. Each pair instance shares the
  same WASM hash (factory deploys from a registered hash); upgrading
  the factory's stored pair WASM hash is the typical failure mode.

Contract IDs live in each source's package — search for `Mainnet*`
constants in `internal/sources/<source>/events.go`.

### 2. Collect the WASM-version timeline

Run `ratesengine-ops wasm-history` against a galexie data store that
covers the replay range:

    ratesengine-ops wasm-history \
      -config /etc/ratesengine.toml \
      -from 2 -to <r1-archive-tip> \
      -contracts <factory>,<router>,<pair-instance-A>,<pair-instance-B>... \
      > /tmp/<source>-wasm-history.json

> **`-to` upper bound on r1.** Use the verified tip of
> `galexie-archive`, NOT the network tip. As of 2026-05-01 the
> archive is frozen at **62,249,727** (the last ledger fully
> exported during the historical fill); live galexie writes to
> `galexie-live`, which the walker does not consult. Setting `-to`
> past that boundary fails partway through with `ledger object
> containing sequence X is missing` — which looks like data
> corruption but is actually just the partial trailing partition.
> Cross-check the current safe boundary against
> `/var/lib/galexie/detect-gaps.json` on r1 (`scan_to` field) and
> [docs/operations/r1-deployment-state.md §3a](../r1-deployment-state.md).

The output is a JSON timeline of `(contract_id → [(active_from,
active_to, wasm_hash)])`. Save this verbatim into the source's
audit log under "WASM timeline".

For pair contracts you can't enumerate ahead of time (Soroswap pairs
are deployed by the factory at runtime), use the factory's
`new_pair` events to get every pair contract ID created, then pass
all of them as `-contracts`. For an MVP audit, the dominant pairs by
volume are sufficient; full coverage is the v2 upgrade.

**Where to run `wasm-history`.** Two options:

- **r1 directly** — only when r1's verify-archive walk is idle.
  The wasm-history scan reads ~46 MB/sec from MinIO and competes
  with verify-archive on ZFS ARC. Run only when verifier is idle.
- **A separate workstation pointed at AWS public bucket.** Set
  galexie's `cfg.Storage.S3BucketArchive = "aws-public-blockchain"`
  + `S3Endpoint = "https://s3.us-east-1.amazonaws.com"` +
  `S3BucketArchivePrefix = "v1.1/stellar/ledgers/pubnet/"`. Costs
  bandwidth (Hetzner-out → AWS-in is free, AWS-out → home is the
  paid leg) but doesn't compete with r1's other workloads.

### 3. Per-WASM-hash decoder review

For each unique WASM hash in the timeline, fetch the WASM and
inspect the event-emitting code paths. Two ways to fetch:

- **stellar-core**: `stellar-core get-wasm <hash> > /tmp/<hash>.wasm`
  if a captive-core's bucket dir has it cached.
- **stellar-rpc** (if running): `stellar-rpc getLedgerEntry`
  with the WASM-storage key.
- **galexie LCM** (last resort): walk LedgerEntryChange entries for
  the install ledger and extract the WASM bytes.

Once you have the WASM bytes, disassemble with `wasm2wat`:

    wasm2wat <hash>.wasm | grep -A 5 'events.publish'

…or for finer detail, look at the source contract repo at the
git tag corresponding to that WASM hash (Soroswap publishes at
github.com/soroswap/core; tags map to releases).

For each event the contract emits, verify against the decoder's
expectations (see "Decoder expectations" in each per-source audit
log):

| failure mode | what to check |
| --- | --- |
| New event topic added | does the decoder skip it cleanly (good) or trip a parse error (bad)? |
| Existing topic name changed | does our `Topic*` constant still match the new wire bytes? |
| Topic[0] prefix string changed (e.g. "SoroswapPair" → "SoroswapPairV2") | byte-equal classification stops matching — silent drop |
| Event body field renamed | by-name extraction (per ADR-0007) returns missing-field error — explicit failure, but every trade in the range is dropped |
| Event body field added | by-name extraction ignores the new field — fine |
| Event body field removed | extraction errors out — every trade dropped |
| i128 / u128 sign or scale change | decoder may produce wrong magnitudes — caught only by Hubble cross-check, not by a parse error |
| Event split into multiple events (e.g. `swap` → `swap_in` + `swap_out`) | correlation logic breaks; decoder may emit 0 or 2 trades per swap |
| Topic arity changed (2-tuple → 3-tuple) | classification matches on `topic[0..2]` so a longer topic still matches — but our position-based assumptions about further topic slots break |

If every WASM hash in the timeline passes review, document the
findings and flip `BackfillSafe: true` for the source in
`internal/sources/external/registry.go` in the same PR as the
audit log update.

If any WASM hash diverges from the current decoder, fix the decoder
(plus add a fixture test under `internal/sources/<source>/` against
that WASM hash) and ship the fix in the same PR. Don't flip
`BackfillSafe` if any WASM hash needs a decoder change that isn't
deployed yet.

### 4. Hubble cross-check (where applicable)

For sources where Hubble has a decoded view (currently SDEX only —
see `cmd/ratesengine-ops/hubble_check.go`), running `hubble-check`
over the audit's replay range is the regression gate that proves
the decoder + audit together produce correct output. For Soroban
sources Hubble has no decoded view; the WASM audit is the
load-bearing safety check.

### 5. Document + flip

Update the source's audit log with:

- The full `wasm-history` JSON output.
- The list of unique WASM hashes seen.
- Per-hash review findings (one line per hash: "matches current
  decoder" or "diverges, fix landed in PR #N").
- The active ledger ranges per hash so future audits can resume
  from `(last audited ledger + 1)`.
- The decision: `BackfillSafe: true | false`.

Then flip the registry entry in the same PR.

## Audit log lifecycle

An audit log is **append-only** with respect to history. Once a WASM
hash has been audited as "matches current decoder" for a specific
ledger range, that finding doesn't change — only the upper bound of
the audited range extends as the network closes more ledgers. New
WASM hashes deployed after the last audit get appended; if any new
hash diverges, flip `BackfillSafe: false` and ship the fix.

`last_verified` in each audit doc's frontmatter MUST be updated
whenever the audit extends — the docs CI (`scripts/ci/lint-docs.sh`)
will fail otherwise on stale freshness markers.
