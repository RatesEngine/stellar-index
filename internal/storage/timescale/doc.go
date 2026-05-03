// Package timescale is the data-access layer over our TimescaleDB
// schema. See migrations/README.md for the authoritative manifest;
// 0001-0015 are applied today (trades + price aggregates + oracle
// updates + supply tables for XLM/classic/SEP-41 + discovered
// assets + volatility baseline + blend auctions + account/trustline/
// claimable/LP/SAC observations).
//
// # Scope
//
// This package owns the SQL. No other package imports lib/pq or
// writes raw SQL; callers use [Store]'s methods exclusively.
// This keeps the "pgx vs lib/pq" choice isolated (today: lib/pq;
// easy to swap later).
//
// # Invariants
//
//   - Amounts are read/written as strings (NUMERIC ↔ canonical.Amount).
//     Never cast to int64. ADR-0003.
//   - Timestamps are always timestamptz on the DB side, always UTC
//     [time.Time] on the Go side.
//   - Identity columns are NOT NULL; validation happens in
//     [canonical.Trade.Validate]/[canonical.OracleUpdate.Validate]
//     before any Insert call.
//
// # Usage
//
//	store, err := timescale.Open(ctx, dsn)
//	if err != nil { return err }
//	defer store.Close()
//
//	if err := store.InsertTrade(ctx, trade); err != nil {
//	    return fmt.Errorf("insert trade: %w", err)
//	}
//
// # Testing
//
// [Store] is a concrete struct, not an interface — there's no
// mock layer. Unit tests in this package's `*_test.go` files use
// a real Timescale via testcontainers-go (started by `make
// test-integration`); package-level tests cover SQL shape +
// NUMERIC round-trip. Higher-level integration tests live in
// `test/integration/`.
package timescale
