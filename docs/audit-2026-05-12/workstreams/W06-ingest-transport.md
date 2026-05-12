# W06 — Ingest transport, dispatcher, persistence pipeline

## Scope

From a raw LedgerCloseMeta blob to a hypertable row.

In scope:
- `internal/ledgerstream/*` — archive + live readers
- `internal/dispatcher/*` — routing + helpers
- `internal/dispatcher/statsflush/*` — decoder stats
- `internal/pipeline/*` — sink, processor, datastore, soroswap_registry
- `internal/hashdb/*` — ledger_seq → sha256 record (ADR-0017)
- `internal/archivecompleteness/*` — Tier A/B/C/D verifier
- `cmd/ratesengine-indexer/main.go` wiring (per-decoder + per-observer)
- `internal/stellarrpc/*` (residue check; should NOT be in production ingest path)
- ADR-0001 (no Horizon), ADR-0002 (S3-compat storage), ADR-0017 (archive completeness invariants)

## Inputs

- `inventory/source-decoder-inventory.md`
- `evidence/cross-file-interactions.md` XFI-1201, XFI-1217

## Per-file checklist

| File | Role | Tests | Status |
| --- | --- | --- | --- |
| `internal/ledgerstream/*.go` (every file) | live + archive LCM iteration; backpressure; checkpoint | | |
| `internal/dispatcher/dispatcher.go` + `_test.go` | top-level dispatch | | |
| `internal/dispatcher/routing_test.go` | per-route verification | | |
| `internal/dispatcher/contract_call_test.go` | ContractCallDecoder path (Band) | | |
| `internal/dispatcher/contract_event_conv_test.go` | event-conv path | | |
| `internal/dispatcher/entry_decoder_test.go` | LedgerEntryChangeDecoder path | | |
| `internal/dispatcher/extract_invoke_reject_test.go` | malformed invoke handling | | |
| `internal/dispatcher/helpers_test.go` | helpers truth | | |
| `internal/dispatcher/testutil_test.go` | test util correctness | | |
| `internal/dispatcher/statsflush/*` | metric emission, table 0020 write | | |
| `internal/pipeline/dispatcher.go` + `_test.go` | pipeline dispatcher glue | | |
| `internal/pipeline/sink.go` + `_test.go` | sink semantics; idempotency | | |
| `internal/pipeline/processor.go` + `_test.go` | processor wiring | | |
| `internal/pipeline/datastore.go` | datastore interface | | |
| `internal/pipeline/soroswap_registry.go` | per-pair registry; backed by table 0016 | | |
| `internal/hashdb/*.go` | sha256 record + drift detector | | |
| `internal/archivecompleteness/*.go` | Tier A/B/C/D verifier; metric emission | | |
| `cmd/ratesengine-indexer/main.go` | every decoder + observer wired | | |
| `cmd/ratesengine-indexer/main_test.go` | wiring tests | | |
| `internal/stellarrpc/*` | residue: only used by `rpc-probe` ops + fixture capture | | |

## Cross-file wiring proof

Capture in evidence:

1. dispatcher decoder set vs `cmd/ratesengine-indexer/main.go` registration
2. observer set vs same
3. sink-write-target table vs migration target

## ADR enforcement checks

- ADR-0001 (no Horizon): grep `horizon` in ingest paths
- ADR-0002 (S3-compat only): grep `os.Open(.*galexie` (local fs)
  or `local-archive` config use
- ADR-0017 (archive completeness): every Tier A/B/C/D path
  produces a metric + alert + runbook (cross-ref W14)

## stellar-rpc removal claim

CLAUDE.md asserts stellar-rpc was removed from r1 on 2026-04-23
but live R1 probe (EV-1204) shows stellar-core listening on
11726. Verify:

- `internal/stellarrpc` imports outside `cmd/ratesengine-ops/discovery.go` and fixture capture
- `lint-imports.sh` baseline still forbids it elsewhere
- live R1: is `stellar-core` actually used by anyone, or is the
  binary running but unused?

## Adversarial vectors

- A1.1..A1.11 (entire hostile-XDR family arrives at dispatcher)
- C1.4 MinIO disk full → galexie blocks → ingest stops
- C1.5 Galexie writer stalls but consumer doesn't notice
- C4.1 Upstream history-archive corruption silently accepted

## Cross-workstream dependencies

- W07 owns per-decoder loops; this workstream verifies they are
  *wired*
- W09 owns table semantics
- W14 owns metric + alert
- W21 R1 live probe of stellar-core process
- W24 owns WASM-version safety

## Closure criteria

- Every per-file row terminal
- Cross-file wiring proof captured
- ADR-0001/0002/0017 enforcement check completed
- stellar-rpc residue claim resolved (with finding if dispute
  exists)
