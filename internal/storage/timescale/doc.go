// Package timescale is the data-access layer over our TimescaleDB
// schema. See migrations/README.md for the manifest of currently
// applied migrations (0001 trades-hypertable, 0002 price-aggregates,
// 0003 oracle-updates-hypertable, 0004 relax-trades-ledger-for-offchain).
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
// Unit tests use mocks at the [Store] interface (future work —
// not yet extracted). Integration tests in
// `test/integration/storage_test.go` exercise the real code against
// a testcontainers-go Timescale.
package timescale
