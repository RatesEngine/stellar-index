//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestMigrationsRoundTrip spins up a throwaway TimescaleDB,
// runs every migration up, asserts the expected hypertable +
// continuous-aggregate shape exists, then rolls them all back and
// asserts a clean slate. This ties together ADR-0006 (storage
// choice) + the SQL files + the migrate binary's underlying library
// + the compose-stack's TimescaleDB image choice.
//
// Runs under `-tags=integration` only. Nominal runtime: ~30s on a
// warm Docker cache, ~2min on a cold one (TimescaleDB image pull).
func TestMigrationsRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pg, err := tcpostgres.Run(ctx,
		"timescale/timescaledb:2.17.2-pg15",
		tcpostgres.WithDatabase("ratesengine"),
		tcpostgres.WithUsername("ratesengine"),
		tcpostgres.WithPassword("ratesengine-test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start timescale: %v", err)
	}
	t.Cleanup(func() {
		// Always attempt teardown; log but don't fail the test on
		// container-stop errors.
		if err := pg.Terminate(context.Background()); err != nil {
			t.Logf("terminate container: %v", err)
		}
	})

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	// Pre-create the timescaledb extension, mirroring
	// deploy/docker-compose/init/00-timescale-extension.sql.
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS timescaledb"); err != nil {
		t.Fatalf("create extension: %v", err)
	}

	// Resolve the absolute path to the migrations directory from
	// the test file's own location so the test works regardless of
	// cwd.
	_, thisFile, _, _ := runtime.Caller(0)
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	migrator, err := migrate.New("file://"+migrationsDir, dsn)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	t.Cleanup(func() {
		if srcErr, dbErr := migrator.Close(); srcErr != nil || dbErr != nil {
			t.Logf("migrator close: src=%v db=%v", srcErr, dbErr)
		}
	})

	// ─── Up: apply all migrations ───────────────────────────────
	if err := migrator.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	// Verify trades hypertable exists.
	assertHypertableExists(t, db, ctx, "trades")

	// Verify the expected indexes exist on trades.
	for _, idx := range []string{
		"trades_base_ts_idx",
		"trades_quote_ts_idx",
		"trades_pair_ts_idx",
		"trades_source_ledger_idx",
	} {
		assertIndexExists(t, db, ctx, "trades", idx)
	}

	// Verify ingestion_cursors table exists (non-hyper).
	assertTableExists(t, db, ctx, "ingestion_cursors")

	// Verify oracle_updates hypertable + its indexes (0003).
	assertHypertableExists(t, db, ctx, "oracle_updates")
	for _, idx := range []string{
		"oracle_updates_asset_ts_idx",
		"oracle_updates_pair_ts_idx",
		"oracle_updates_source_ledger_idx",
	} {
		assertIndexExists(t, db, ctx, "oracle_updates", idx)
	}

	// Verify every continuous aggregate is present and a CAGG
	// (not a plain view) + has a refresh policy attached.
	for _, caggName := range []string{
		"prices_1m", "prices_15m",
		"prices_1h", "prices_4h", "prices_1d",
		"prices_1w", "prices_1mo",
	} {
		assertContinuousAggregateExists(t, db, ctx, caggName)
	}

	// Spot-check: insert a trade, query the view before the refresh
	// policy fires, and make sure the pre-compute pipeline works
	// when manually refreshed.
	insertSampleTrade(t, db, ctx)
	if _, err := db.ExecContext(ctx,
		`CALL refresh_continuous_aggregate('prices_1m', NULL, NULL)`,
	); err != nil {
		t.Fatalf("manual refresh prices_1m: %v", err)
	}
	assertPrices1mHasRow(t, db, ctx)

	// ─── CHECK constraints round-trip ──────────────────────────
	// Verify the invariants baked into migration 0001 / 0003 are
	// enforced. If someone accidentally drops a CHECK in a future
	// migration, this test fails loudly. The canonical.Validate
	// functions are a first line of defense — the DB CHECKs are
	// the last. Both matter.
	assertInsertRejected(t, db, ctx, "negative base_amount", `
        INSERT INTO trades
            (source, ledger, tx_hash, op_index, ts,
             base_asset, quote_asset, base_amount, quote_amount)
        VALUES ('t', 1, 'aa', 0, now(), 'native', 'native', -1, 1)`)
	assertInsertRejected(t, db, ctx, "zero ledger", `
        INSERT INTO trades
            (source, ledger, tx_hash, op_index, ts,
             base_asset, quote_asset, base_amount, quote_amount)
        VALUES ('t', 0, 'aa', 0, now(), 'native', 'native', 1, 1)`)
	assertInsertRejected(t, db, ctx, "negative op_index", `
        INSERT INTO trades
            (source, ledger, tx_hash, op_index, ts,
             base_asset, quote_asset, base_amount, quote_amount)
        VALUES ('t', 1, 'aa', -1, now(), 'native', 'native', 1, 1)`)
	assertInsertRejected(t, db, ctx, "oracle decimals > 38", `
        INSERT INTO oracle_updates
            (source, ledger, tx_hash, op_index, ts,
             asset, quote, price, decimals)
        VALUES ('o', 1, 'aa', 0, now(), 'native', 'native', 1, 39)`)
	assertInsertRejected(t, db, ctx, "oracle confidence > 1", `
        INSERT INTO oracle_updates
            (source, ledger, tx_hash, op_index, ts,
             asset, quote, price, decimals, confidence)
        VALUES ('o', 1, 'aa', 0, now(), 'native', 'native', 1, 14, 1.5)`)
	assertInsertRejected(t, db, ctx, "oracle negative price", `
        INSERT INTO oracle_updates
            (source, ledger, tx_hash, op_index, ts,
             asset, quote, price, decimals)
        VALUES ('o', 1, 'aa', 0, now(), 'native', 'native', -1, 14)`)

	// ─── Compression + retention policies attached ─────────────
	// Migrations add compression + retention per ADR-0006. If a
	// future migration silently drops a policy, chunks would grow
	// without bound and break the disk forecast. Assert each
	// policy exists once on the hypertables that should have it.
	assertPolicyAttached(t, db, ctx, "trades", "policy_compression")
	assertPolicyAttached(t, db, ctx, "trades", "policy_retention")
	assertPolicyAttached(t, db, ctx, "oracle_updates", "policy_compression")
	assertPolicyAttached(t, db, ctx, "oracle_updates", "policy_retention")

	// ─── Down: roll everything back ─────────────────────────────
	if err := migrator.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down: %v", err)
	}

	// After a full rollback, none of our tables / CAGGs should remain.
	// Symmetric with the up-path assertions: every object the
	// migrations created must be gone. If a future migration's down
	// script forgets to drop one CAGG, retention+compression policies
	// on the survivor will keep firing against a non-existent trades
	// hypertable, flooding logs with errors — we'd rather the test
	// fail loudly.
	assertTableAbsent(t, db, ctx, "trades")
	assertTableAbsent(t, db, ctx, "ingestion_cursors")
	assertTableAbsent(t, db, ctx, "oracle_updates")
	for _, cagg := range []string{
		"prices_1m", "prices_15m", "prices_1h",
		"prices_4h", "prices_1d", "prices_1w", "prices_1mo",
	} {
		assertContinuousAggregateAbsent(t, db, ctx, cagg)
	}
}

// ─── helpers ──────────────────────────────────────────────────────

func assertTableExists(t *testing.T, db *sql.DB, ctx context.Context, name string) {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
		name,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %q: %v", name, err)
	}
	if !exists {
		t.Errorf("expected table %q to exist", name)
	}
}

func assertTableAbsent(t *testing.T, db *sql.DB, ctx context.Context, name string) {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
		name,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check absent %q: %v", name, err)
	}
	if exists {
		t.Errorf("expected table %q to be absent after rollback", name)
	}
}

func assertHypertableExists(t *testing.T, db *sql.DB, ctx context.Context, name string) {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM timescaledb_information.hypertables
            WHERE hypertable_name = $1
        )`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("check hypertable %q: %v", name, err)
	}
	if !exists {
		t.Errorf("expected hypertable %q to exist", name)
	}
}

func assertIndexExists(t *testing.T, db *sql.DB, ctx context.Context, table, idx string) {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM pg_indexes
            WHERE tablename = $1 AND indexname = $2
        )`, table, idx).Scan(&exists)
	if err != nil {
		t.Fatalf("check index %q on %q: %v", idx, table, err)
	}
	if !exists {
		t.Errorf("expected index %q on %q", idx, table)
	}
}

func assertContinuousAggregateExists(t *testing.T, db *sql.DB, ctx context.Context, name string) {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM timescaledb_information.continuous_aggregates
            WHERE view_name = $1
        )`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("check cagg %q: %v", name, err)
	}
	if !exists {
		t.Errorf("expected continuous aggregate %q to exist", name)
		return
	}

	// Also assert it has a refresh policy — a CAGG without a refresh
	// policy is a silent bug per migrations/README.md.
	var policyCount int
	err = db.QueryRowContext(ctx, `
        SELECT count(*) FROM timescaledb_information.jobs j
        JOIN timescaledb_information.continuous_aggregates c
          ON j.hypertable_name = c.materialization_hypertable_name
        WHERE c.view_name = $1
          AND j.proc_name = 'policy_refresh_continuous_aggregate'`,
		name,
	).Scan(&policyCount)
	if err != nil {
		t.Fatalf("check refresh policy for %q: %v", name, err)
	}
	if policyCount < 1 {
		t.Errorf("cagg %q has no refresh policy", name)
	}
}

func assertContinuousAggregateAbsent(t *testing.T, db *sql.DB, ctx context.Context, name string) {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM timescaledb_information.continuous_aggregates
            WHERE view_name = $1
        )`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("check cagg absent %q: %v", name, err)
	}
	if exists {
		t.Errorf("expected cagg %q to be absent after rollback", name)
	}
}

func insertSampleTrade(t *testing.T, db *sql.DB, ctx context.Context) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
        INSERT INTO trades
            (source, ledger, tx_hash, op_index, ts,
             base_asset, quote_asset,
             base_amount, quote_amount, usd_volume,
             maker, taker)
        VALUES (
            'sdex', 52430001,
            'cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe',
            0, now(),
            'native', 'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
            1000000000, 12420000, 12.42,
            'maker-acc', 'taker-acc'
        )
    `)
	if err != nil {
		t.Fatalf("insert sample trade: %v", err)
	}
}

// assertPolicyAttached checks that a TimescaleDB background job
// with the given proc_name is registered against the hypertable.
// Covers `add_compression_policy` and `add_retention_policy` calls
// in the migrations — each creates a row in
// timescaledb_information.jobs keyed on (proc_name, hypertable).
func assertPolicyAttached(t *testing.T, db *sql.DB, ctx context.Context, hypertable, procName string) {
	t.Helper()
	var count int
	err := db.QueryRowContext(ctx, `
        SELECT count(*) FROM timescaledb_information.jobs
        WHERE hypertable_name = $1 AND proc_name = $2`,
		hypertable, procName,
	).Scan(&count)
	if err != nil {
		t.Fatalf("check policy %s on %s: %v", procName, hypertable, err)
	}
	if count < 1 {
		t.Errorf("expected %s policy on hypertable %q, got %d jobs", procName, hypertable, count)
	}
}

// assertInsertRejected runs `stmt` and expects Postgres to refuse
// it with a CHECK-constraint violation (SQLSTATE 23514). Passing
// statements are a test failure — they mean a constraint was
// silently dropped or weakened.
func assertInsertRejected(t *testing.T, db *sql.DB, ctx context.Context, name, stmt string) {
	t.Helper()
	_, err := db.ExecContext(ctx, stmt)
	if err == nil {
		t.Errorf("%s: expected CHECK constraint rejection, got nil error", name)
		return
	}
	// Postgres 23514 = check_violation. lib/pq surfaces it as a
	// string inside the error message; accept either the SQLSTATE
	// or the "check constraint" substring.
	msg := err.Error()
	if !strings.Contains(msg, "23514") && !strings.Contains(msg, "check constraint") &&
		!strings.Contains(msg, "violates check") {
		t.Errorf("%s: error %v is not a check-constraint violation", name, err)
	}
}

func assertPrices1mHasRow(t *testing.T, db *sql.DB, ctx context.Context) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM prices_1m`).Scan(&count); err != nil {
		t.Fatalf("count prices_1m: %v", err)
	}
	if count < 1 {
		t.Errorf("expected prices_1m to contain at least 1 row after refresh, got %d", count)
	}
}
