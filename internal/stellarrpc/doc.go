// Package stellarrpc is a minimal JSON-RPC client for stellar-rpc.
//
// # Scope after r1 stellar-rpc removal (2026-04-23)
//
// stellar-rpc was removed from r1's production ingest path on
// 2026-04-23 — every trade and oracle update now flows through
// Galexie → ledgerstream → dispatcher per
// docs/architecture/ingest-pipeline.md. This package's remaining
// callers are:
//
//   - cmd/ratesengine-ops/main.go's `rpc-probe` operator diagnostic
//     (one-shot health/liveness check against any stellar-rpc).
//   - internal/sources/soroswap/factory_seed.go — boot-time
//     factory sweep via simulateTransaction to seed the
//     pair→tokens registry for pre-history pairs (PR #14). Not
//     on the live-ingest hot path; idempotent on restart.
//   - scripts/dev/* fixture-capture scripts.
//
// The scripts/ci/lint-imports.sh rule A/no-rpc-in-ingest blocks
// any new caller outside this allow-list as a structural guardrail.
//
// # Why roll our own
//
// Two reasons it's not the SDK's stellar-rpc client:
//
//  1. The SDK brings a large dependency surface (full XDR codegen,
//     sdk-internal helpers). For our diag + factory-seed use case
//     we need a small, auditable surface covering the ~6 RPC
//     methods we actually call.
//  2. Our callers (probes, health checks, soroswap factory seed)
//     need a client they can mock in unit tests without
//     instantiating the full SDK.
//
// If a complex method shows up in a future roadmap path, we take
// the SDK dep for that one path and keep this package for the
// simple read methods.
//
// # Methods
//
// The package exposes thin wrappers over:
//
//   - getHealth                  — liveness + staleness
//   - getLatestLedger            — sequence + closeTime at tip
//   - getNetwork                 — network passphrase + protocol
//   - getVersionInfo             — build version, captive-core version
//   - getEvents                  — contract event stream with filters;
//     envelope sanity-checked (see
//     EventsResponse.sanityCheck)
//   - getLedgers                 — raw ledger XDR batch (headerXdr + metadataXdr)
//   - getTransaction             — single-tx lookup by hash (Status may
//     be NOT_FOUND outside retention window)
//   - getTransactions            — batch tx lookup (paginated)
//   - getFeeStats                — inclusion-fee percentiles (divergence input)
//
// XDR decoding is NOT this package's job — callers pass
// headerXdr / metadataXdr / event topic+value bytes to whichever
// decoder they already use (stellar-extract for ledger meta,
// canonical.Amount.FromString for strkey-style amounts, etc.).
//
// # Usage
//
//	c := stellarrpc.New("http://localhost:8000")
//	h, err := c.Health(ctx)
//	if err != nil { return err }
//	if h.Status != "healthy" {
//	    log.Warnf("rpc stale: %s", h.Status)
//	}
//
// See [cmd/ratesengine-ops]'s `rpc-probe` subcommand for a real
// example.
package stellarrpc
