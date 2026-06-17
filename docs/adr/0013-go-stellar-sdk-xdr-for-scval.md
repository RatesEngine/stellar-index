---
adr: 0013
title: Adopt go-stellar-sdk/xdr for SCVal decoding in source connectors
status: Accepted
date: 2026-04-23
accepted: 2026-04-23
supersedes: []
superseded_by: null
---

# ADR-0013: Adopt go-stellar-sdk/xdr for SCVal decoding in source connectors

> **Amendment (2026-06-12, F-1353 / D2-08).** This ADR weighs decoding
> options against a stellar-rpc `getEvents` ingest path. That path was
> **removed from production on 2026-04-23** (invariant 6): production
> ingest now reads Galexie MinIO LedgerCloseMeta directly, and
> `internal/stellarrpc` survives only as the `rpc-probe` diagnostic and
> fixture-capture client. The SCVal-decoding decision (adopt
> go-stellar-sdk/xdr) stands; the surrounding RPC-transport framing is
> historical. See
> [docs/architecture/ingest-pipeline.md](../architecture/ingest-pipeline.md).

## Context

The on-chain source connectors (`internal/sources/soroswap`,
`aquarius`, `phoenix`, `reflector`) receive contract events via
`internal/stellarrpc` as base64-encoded XDR SCVal blobs. Today, every
`decode.go` has stubbed `decodeAmount` / `decodeAddress` /
`decodeSwapAmounts` functions that return zero values. The
correlation + orchestration layers are otherwise complete and unit-
tested (event grouping, swap+sync pairing, topic-shape predicates).

Without real SCVal decoding, the ingestion pipeline emits no trades
from any Soroban source. This is the single biggest blocker between
"stack running on r1" and "trade data flowing into Timescale"
(recorded in [r1-deployment-state.md §Blocking](../operations/r1-deployment-state.md)).

ADR-0001 rules out Horizon, so we can't sidestep decoding by relying
on a Horizon-effects stream. We need XDR decoding in-process.

Two paths forward:

1. Take a dependency on `github.com/stellar/go-stellar-sdk/xdr` (the
   official post-monorepo SDK, archived 2025-12-16 re-homed at
   `stellar/go-stellar-sdk`).
2. Hand-roll SCVal parsing by pulling the minimal XDR types we need
   out of the SDK and vendoring them.

`internal/stellarrpc/doc.go` argues for path 2 at the RPC-client
layer (small auditable surface, ~6 JSON-RPC methods). That argument
does NOT extend to XDR: the SCVal / ContractEvent definitions are
large, interdependent, and regenerated whenever Stellar ships a new
protocol version (P23 landed 2025-09-03 with CAP-67 unified events).
Maintaining a hand-rolled fork would be Sisyphean — each protocol
bump would drop a rebase on us, and our earlier investigation already
flagged two correctness bugs in forked XDR code
(`withObsrvr/cdp-pipeline-workflow`; see
[engineering-standards.md](../engineering-standards.md)).

## Decision

Import `github.com/stellar/go-stellar-sdk/xdr` as a direct
dependency and use it from every connector's `decode.go`. Continue
using `internal/stellarrpc` for the JSON-RPC wire layer — the SDK's
own client is not adopted.

Dependency scope is restricted to `.../xdr` (and transitively,
`.../strkey`). Other SDK subpackages stay off-limits until they're
needed for a specific feature, protected by CI lint.

## Consequences

- **Positive:** Connector decode.go files get real implementations
  without re-inventing XDR. New protocol releases inherit correctness
  by bumping one go.sum pin in VERSIONS.md. The `decoderHooks`
  injection pattern already in each decode.go lets tests keep using
  fakes without touching the production SDK path.
- **Positive:** `canonical/strkey.go` can shed its local stub
  (TODO already flagged at line 21) by redirecting to `.../strkey`.
- **Negative:** Larger dependency graph. The SDK pulls in its own
  protobuf-style generated code. Mitigation: the `internal/xdr` wrapper
  (new) exports just the handful of types our connectors need, so
  the SDK's surface doesn't leak into canonical types or business
  logic.
- **Operational impact:** `go mod tidy` will add ~40-60 indirect
  deps (XDR codegen runtime + encoding/gogoproto etc.). Build-time
  impact is small; runtime footprint in binaries increases by ~1-2 MB.
- **Downstream design impact:** The `consumer.Source` interface
  stays stable — SCVal decoding is private to each connector. Any
  later ADR that swaps the decoder for a WASM interpreter (e.g. for
  contracts we haven't seen before) can do so without touching the
  public surface.

## Alternatives considered

1. **Hand-rolled SCVal parser** — rejected because every Stellar
   protocol bump would invalidate our fork; our earlier investigation
   already identified two connectors in the wild with XDR bugs
   stemming from vendored forks.
2. **Run Galexie output through a separate decoder process** — over-
   engineered for Phase 1; adds an IPC boundary we don't need and
   doesn't remove the XDR-decoding work, just relocates it.
3. **Stay on stellar-rpc's `getEvents` + accept stringified JSON
   values** — stellar-rpc returns SCVal as opaque base64 XDR, not
   JSON; this path doesn't exist.

## Migration plan (implementation-side)

Not formally part of the ADR but captured here so the implementation
PR doesn't re-derive it:

1. `go get github.com/stellar/go-stellar-sdk@latest` → pin in
   VERSIONS.md.
2. Create `internal/xdr/scval.go` — thin wrapper re-exporting only
   the SCVal types + helper funcs used by connectors.
3. In each `internal/sources/<venue>/decode.go`:
   - Replace `stubDecodeAmount` with `xdr.UnmarshalBase64(v, &sv)`
     → pull `sv.I128` / `sv.U128` / `sv.I64` per contract schema →
     `canonical.Amount.FromBigInt(...)`.
   - Replace `stubDecodeAddress` similarly → `canonical.Asset`.
4. Swap `canonical/strkey.go`'s local stub for SDK's `strkey` (TODO
   already flagged at line 21).
5. Add golden-file fixtures in `test/fixtures/<venue>/` — real
   base64 SCVal blobs captured from mainnet via `stellarindex-ops
   rpc-probe` for each event shape the decoder must handle.
6. Per-venue decode_test.go should cover: valid happy path, i128
   with negative values (signing semantics), the SEP-41 transfer-map
   shape, and the CAP-67 unified 4-topic shape.

## Implementation status (2026-04-23)

PR 164a landed the scaffolding — Accepted status reflects that code.

- Wrapper package lives at `internal/scval/` (not `internal/xdr/`).
  Name change made because every connector operates on SCVal, not
  on arbitrary XDR unions; the narrower name steers usage toward
  the intended surface.
- Re-exports `scval.ScVal` and `scval.ScMapEntry` as type aliases so
  connectors never import `github.com/stellar/go-stellar-sdk/xdr`
  directly. The xdr import is confined to `internal/scval/` and
  `canonical/strkey.go` (still pending conversion — separate PR).
- Golden regression in `internal/scval/scval_test.go`
  (`TestGolden_symbolBytes`) pins the base64 bytes for
  `Symbol("REFLECTOR")` and `Symbol("update")` so an SDK upgrade
  that changes wire encoding fires a test before shipping.
- Reflector is the first connector off stubs; Soroswap / Aquarius
  / Phoenix follow in PRs 164b / 164c / 164d.

## References

- Related ADRs: ADR-0001 (Horizon ruled out), ADR-0003 (i128 no
  truncation — must survive decoding path), ADR-0005 (monorepo
  structure — `internal/scval/` wrapper placement)
- Pinned versions: [VERSIONS.md](../../VERSIONS.md)
- External: [CAP-67 unified events](https://github.com/stellar/stellar-protocol/blob/master/core/cap-0067.md),
  [SEP-41 token events](https://github.com/stellar/stellar-protocol/blob/master/ecosystem/sep-0041.md)
