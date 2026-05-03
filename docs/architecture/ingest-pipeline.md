---
title: Ingest pipeline — the one canonical data path
last_verified: 2026-05-03
status: binding
---

# Ingest pipeline

**Every byte of on-chain data flows through one path:**

```
Stellar pubnet
    │   (SCP via Galexie's captive-core)
    ▼
galexie                    ← the SINGLE stellar-core on r1 (ADR-0002 + CDP pattern)
    │   (writes .xdr.zst to)
    ▼
MinIO galexie-live         ← S3-compatible object store
    │
    ▼
internal/ledgerstream/     ← SDK BufferedStorageBackend wrapper (PR 165a)
    │   yields xdr.LedgerCloseMeta per ledger
    ▼
internal/dispatcher/       ← single consumer of ledgerstream (PR 165b)
    │   per tx:
    │     • tx.GetTransactionEvents()       → Soroban contract events
    │     • tx.Envelope.Operations()        → classic ops (for SDEX)
    │     • tx.Result.Result.OperationResults()
    │   per event: byte-match topic[0] against each source's
    │              TopicPrefix*/TopicSymbol* constants
    ▼
internal/sources/{soroswap,aquarius,phoenix,reflector,sdex,…}/
    │   each is a pure decoder + (optional) per-source correlation
    │   state (Soroswap swap+sync, Phoenix 8-field assembly).
    │   NO goroutines, NO RPC clients, NO pagination loops.
    │   decode(...) → canonical.Trade | canonical.OracleUpdate
    ▼
internal/storage/timescale/
    │   InsertTrade / InsertOracleUpdate
    ▼
TimescaleDB hypertables
    │
    ▼
/v1/* API
```

**Backfill and live-tail are the same code.** Both are just
`internal/ledgerstream.Stream(ctx, fromLedger, toLedger)` with
`toLedger == 0` meaning unbounded. No separate `BackfillRange` /
`StreamLive` methods on sources.

---

## Binding rules

### 1. No stellar-rpc in production ingest

`internal/stellarrpc/` exists only for:
- `ratesengine-ops rpc-probe` — operator diagnostic against a
  public endpoint.
- Development-time fixture capture via scripts in
  `scripts/dev/capture-*-fixtures.sh`.

`ratesengine-indexer` MUST NOT import `internal/stellarrpc`. Any
source's ingest path that calls `rpc.GetEvents` or
`rpc.LatestLedgerSequence` is wrong and blocks merge. This was
established 2026-04-23 when stellar-rpc was removed from r1 (see
`docs/operations/r1-deployment-state.md`). The fact that stellar-rpc
returns the same base64 SCVal strings as ledger-meta decoding is a
coincidence, not an architectural option — only one of those paths
exists in production.

### 2. Source packages are pure decoders

Each `internal/sources/<venue>/` package exports:
- `SourceName` constant.
- `TopicPrefix*` / `TopicSymbol*` pre-encoded SCVal bytes (for the
  dispatcher's byte-equality routing).
- `decode...(event | rawPair | rawSwap) → canonical.*` functions.
- Optional per-source correlation buffer (Soroswap swap+sync,
  Phoenix 8-field). The buffer's state lives in memory for the
  lifetime of one dispatcher goroutine — no per-source goroutine,
  no RPC, no cursors.

A source package MUST NOT:
- Hold a `*stellarrpc.Client`.
- Implement `consumer.Source.BackfillRange` / `StreamLive`.
- Poll.
- Paginate.

### 3. Dispatcher owns routing

`internal/dispatcher/` (landing in PR 165b) is the single
consumer of `internal/ledgerstream`. It is the ONLY place where:
- Contract-event byte-matching against `TopicPrefix*` /
  `TopicSymbol*` happens.
- Classic-op walking for SDEX happens.
- Per-source correlation state is fed.

Adding a new source means:
- Adding its decoder package under `internal/sources/<venue>/`.
- Registering its dispatch entry in `internal/dispatcher/routes.go`.
- That's it — no wire-layer code, no new goroutines.

### 4. Fixture captures stay RPC-based

`test/fixtures/<venue>/<wasm_hash>/*.json` fixtures are recorded from
`scripts/dev/capture-*-fixtures.sh` against the public
`mainnet.sorobanrpc.com` endpoint. This is fine because the event
bytes are identical whether pulled from RPC or extracted from
`LedgerCloseMeta` — both embed the same `xdr.ContractEvent` / SCVal
payloads. Using RPC for fixture capture is a convenience for
developers without r1 MinIO access.

Integration tests that need a live Galexie source use a MinIO
testcontainer seeded with a recorded `.xdr.zst` — never a live RPC
call.

---

## Why this doc exists

Agent + @ash mistake, 2026-04-23: built Task #164 (decoders) correctly
but wired the per-source `consumer.go` to `rpc *stellarrpc.Client` →
`rpc.GetEvents`. This worked for tests (public RPC endpoint) but would
never run on r1 because stellar-rpc was removed from r1 the same day.
The mistake was *consistent extension of pre-existing RPC-based code*
without auditing whether that code was still the production path.

Preventive controls put in place:

- **CLAUDE.md** "Invariants — never violate these" now has a
  dedicated rule #6 pointing at this doc.
- **CLAUDE.md** "Things that will surprise you" highlights the 2026-
  04-23 RPC removal.
- **This doc** is binding (status: binding, not "living"); it gets
  linked from every PR description that touches the ingest path.
- **CI check (live):** `scripts/ci/lint-imports.sh` blocks
  `internal/stellarrpc` imports outside the allowlist
  (`cmd/ratesengine-ops/`, `scripts/dev/`, `internal/stellarrpc/`
  itself, `internal/sources/*/decode.go` until Event moves to a
  neutral package in PR 165b, `*_test.go`). Also enforces rule B
  (xdr scoped to internal/scval, ADR-0013) and rule C (no
  Horizon, ADR-0001). Current legacy violations grandfathered in
  `scripts/ci/lint-imports.baseline`; the lint fails on NEW
  violations or on stale baseline entries so the baseline has to
  shrink monotonically. Runs via `make lint-imports`,
  `make verify`, and the `import-checks` CI job.

If you find yourself adding a `rpc *stellarrpc.Client` field to a
source struct or writing a new `BackfillRange`/`StreamLive` method:
**stop.** Your work belongs in the dispatcher or in decode.go, not
in a per-source poll loop.

---

## References

- [ADR-0001](../adr/0001-horizon-deprecated.md) — no Horizon.
- [ADR-0002](../adr/0002-minio-s3-compat-storage.md) — S3-compatible
  storage is MinIO; not local filesystem.
- [ADR-0013](../adr/0013-go-stellar-sdk-xdr-for-scval.md) — SDK
  dependency, which gives us `ingest.ApplyLedgerMetadata`.
- [r1-deployment-state.md](../operations/r1-deployment-state.md) —
  what's actually running on r1.
- [architecture_cdp.md memory](../../../../.claude/projects/-Users-ash-code-ratesengine/memory/architecture_cdp.md) —
  the CDP pattern.
- [contract-schema-evolution.md](contract-schema-evolution.md) —
  per-contract WASM versioning (unrelated to transport; still
  applies).
