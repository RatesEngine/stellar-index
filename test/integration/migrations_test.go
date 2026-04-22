//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go"
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

	// ─── Down: roll everything back ─────────────────────────────
	if err := migrator.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down: %v", err)
	}

	// After a full rollback, none of our tables / CAGGs should remain.
	assertTableAbsent(t, db, ctx, "trades")
	assertTableAbsent(t, db, ctx, "ingestion_cursors")
	assertTableAbsent(t, db, ctx, "oracle_updates")
	assertContinuousAggregateAbsent(t, db, ctx, "prices_1m")
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
