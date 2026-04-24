//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"math/big"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	c "github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// gAccountFromSeed returns a strkey-valid 56-char G-address whose
// ed25519 key's first byte is `seed`. Deterministic for a given
// seed so fixture assertions stay reproducible.
func gAccountFromSeed(t *testing.T, seed byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seed
	s, err := strkey.Encode(strkey.VersionByteAccountID, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}

// TestStoreRoundTrip exercises the trade / oracle / cursor paths
// through a real TimescaleDB with our migrations applied. This is
// the first end-to-end "write → read" proof of the Go storage
// layer.
func TestStoreRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// ─── Trades ─────────────────────────────────────────────────
	usdc, err := c.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatal(err)
	}
	pair, _ := c.NewPair(c.NativeAsset(), usdc)

	tr := c.Trade{
		Source:      "sdex",
		Ledger:      52_430_001,
		TxHash:      "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe",
		OpIndex:     0,
		Timestamp:   time.Now().UTC().Truncate(time.Second),
		Pair:        pair,
		BaseAmount:  c.NewAmount(big.NewInt(1_000_000_000)), // 100 XLM in stroops
		QuoteAmount: c.NewAmount(big.NewInt(12_420_000)),    // 12.42 USDC
		Maker:       "maker-acc",
		Taker:       "taker-acc",
	}

	if err := store.InsertTrade(ctx, tr); err != nil {
		t.Fatalf("InsertTrade: %v", err)
	}
	// Idempotent re-insert should not error (ON CONFLICT DO NOTHING).
	if err := store.InsertTrade(ctx, tr); err != nil {
		t.Fatalf("InsertTrade (duplicate): %v", err)
	}

	n, err := store.CountTrades(ctx)
	if err != nil || n != 1 {
		t.Fatalf("CountTrades = %d, err=%v — want 1 row after duplicate-insert", n, err)
	}

	latest, err := store.LatestTradesForPair(ctx, pair, 5)
	if err != nil {
		t.Fatalf("LatestTradesForPair: %v", err)
	}
	if len(latest) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(latest))
	}
	got := latest[0]
	if !got.Equal(tr) {
		t.Fatalf("trade identity not preserved: %+v", got)
	}
	if got.BaseAmount.Cmp(tr.BaseAmount) != 0 {
		t.Errorf("base_amount lost: got %s want %s", got.BaseAmount, tr.BaseAmount)
	}
	if got.QuoteAmount.Cmp(tr.QuoteAmount) != 0 {
		t.Errorf("quote_amount lost: got %s want %s", got.QuoteAmount, tr.QuoteAmount)
	}

	// ─── Oracle updates ─────────────────────────────────────────
	price, _ := new(big.Int).SetString("1242000000000000", 10)
	up := c.OracleUpdate{
		Source:     "reflector-dex",
		ContractID: "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA",
		Ledger:     52_430_001,
		TxHash:     "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe",
		OpIndex:    0,
		Timestamp:  time.Now().UTC().Truncate(time.Second),
		Asset:      c.NativeAsset(),
		Quote:      usdc,
		Price:      c.NewAmount(price),
		Decimals:   14,
		Confidence: 0.95,
		// OracleUpdate.Validate requires a valid G-strkey Observer.
		// Hand-crafted "GRELAYER_FAKE" is 13 chars (expected 56),
		// so it was rejected after canonical tightened validation.
		// Generate a checksum-valid G-address from a deterministic
		// seed instead.
		Observer: gAccountFromSeed(t, 0xAA),
	}
	if err := store.InsertOracleUpdate(ctx, up); err != nil {
		t.Fatalf("InsertOracleUpdate: %v", err)
	}

	gotUp, err := store.LatestOracleUpdateForAsset(ctx, "reflector-dex", c.NativeAsset())
	if err != nil {
		t.Fatalf("LatestOracleUpdateForAsset: %v", err)
	}
	if !gotUp.Equal(up) {
		t.Fatalf("oracle identity lost: %+v", gotUp)
	}
	if gotUp.Price.Cmp(up.Price) != 0 {
		t.Errorf("price lost: got %s want %s", gotUp.Price, up.Price)
	}
	if gotUp.Decimals != 14 {
		t.Errorf("decimals lost: got %d want 14", gotUp.Decimals)
	}

	// Not-found path.
	_, err = store.LatestOracleUpdateForAsset(ctx, "reflector-dex", usdc)
	if err == nil {
		t.Fatal("expected ErrNotFound for USDC (never inserted for this source)")
	}

	// ─── Cursors ────────────────────────────────────────────────
	if err := store.UpsertCursor(ctx, "soroswap", "", 52_430_001); err != nil {
		t.Fatalf("UpsertCursor: %v", err)
	}
	cur, err := store.GetCursor(ctx, "soroswap", "")
	if err != nil {
		t.Fatalf("GetCursor: %v", err)
	}
	if cur.LastLedger != 52_430_001 {
		t.Errorf("cursor lost: got %d", cur.LastLedger)
	}

	// Update path.
	if err := store.UpsertCursor(ctx, "soroswap", "", 52_430_100); err != nil {
		t.Fatal(err)
	}
	cur, _ = store.GetCursor(ctx, "soroswap", "")
	if cur.LastLedger != 52_430_100 {
		t.Errorf("cursor update lost: got %d", cur.LastLedger)
	}

	// Second subsource for the same source shouldn't interfere.
	if err := store.UpsertCursor(ctx, "soroswap", "pair:CAB...", 99); err != nil {
		t.Fatal(err)
	}
	cur, _ = store.GetCursor(ctx, "soroswap", "pair:CAB...")
	if cur.LastLedger != 99 {
		t.Errorf("sub cursor wrong: got %d", cur.LastLedger)
	}
	cur, _ = store.GetCursor(ctx, "soroswap", "")
	if cur.LastLedger != 52_430_100 {
		t.Errorf("root cursor wrong after sub insert: got %d", cur.LastLedger)
	}

	// ─── ListCursors ────────────────────────────────────────────
	// After the upserts above we have 2 cursors: soroswap/"" and
	// soroswap/"pair:CAB...". ListCursors returns both, sorted by
	// (source, sub_source).
	all, err := store.ListCursors(ctx)
	if err != nil {
		t.Fatalf("ListCursors: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListCursors returned %d, want 2", len(all))
	}
	if all[0].Source != "soroswap" || all[0].Sub != "" {
		t.Errorf("ListCursors[0] = %+v, want soroswap/\"\"", all[0])
	}
	if all[1].Source != "soroswap" || all[1].Sub != "pair:CAB..." {
		t.Errorf("ListCursors[1] = %+v, want soroswap/pair:CAB...", all[1])
	}
	// UpdatedAt must be populated by the server-side now() call.
	for _, c := range all {
		if c.UpdatedAt.IsZero() {
			t.Errorf("cursor %s/%s has zero UpdatedAt", c.Source, c.Sub)
		}
	}

	// ─── Cursor monotonic-advance guard ─────────────────────────
	// DB-level refusal to regress last_ledger (ON CONFLICT DO UPDATE
	// ... WHERE EXCLUDED.last_ledger > ingestion_cursors.last_ledger).
	// Defense in depth: the orchestrator's Go-level advance-only rule
	// can't be the only line of defense for a misconfigured two-
	// indexer race.
	if err := store.UpsertCursor(ctx, "soroswap", "", 10_000); err != nil {
		t.Fatalf("UpsertCursor (regression attempt): %v", err)
	}
	cur, _ = store.GetCursor(ctx, "soroswap", "")
	if cur.LastLedger != 52_430_100 {
		t.Errorf("regression-attempt should have been ignored; got %d, want 52430100",
			cur.LastLedger)
	}
	// Equal-value attempt also no-ops (WHERE > , not >=).
	if err := store.UpsertCursor(ctx, "soroswap", "", 52_430_100); err != nil {
		t.Fatalf("UpsertCursor (same value): %v", err)
	}
	cur, _ = store.GetCursor(ctx, "soroswap", "")
	if cur.LastLedger != 52_430_100 {
		t.Errorf("same-value upsert shouldn't change stored cursor")
	}
	// Advancement still works.
	if err := store.UpsertCursor(ctx, "soroswap", "", 52_430_200); err != nil {
		t.Fatal(err)
	}
	cur, _ = store.GetCursor(ctx, "soroswap", "")
	if cur.LastLedger != 52_430_200 {
		t.Errorf("advance after regression-attempt lost: got %d", cur.LastLedger)
	}
}

// TestInsertTrade_MultiOpSameTxBothLand covers the most common
// real-world pattern that would have caught the Aquarius fanout
// bug: a single Soroban tx with multiple operations, each emitting
// its own trade. The trades share (source, ledger, tx_hash, ts)
// but differ on OpIndex — both MUST persist. Before the fanout
// fix, op=0,i=1,j=0 and op=1,i=0,j=0 collided on OpIndex=256 and
// ON CONFLICT DO NOTHING silently dropped the second.
func TestInsertTrade_MultiOpSameTxBothLand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	usdc, _ := c.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	pair, _ := c.NewPair(c.NativeAsset(), usdc)

	ts := time.Now().UTC().Truncate(time.Second)
	tx := "1111111111111111111111111111111111111111111111111111111111111111"
	base := c.Trade{
		Source: "sdex", Ledger: 52_430_001, TxHash: tx,
		Timestamp: ts, Pair: pair,
		BaseAmount:  c.NewAmount(big.NewInt(1_000_000_000)),
		QuoteAmount: c.NewAmount(big.NewInt(12_420_000)),
	}
	tr0 := base
	tr0.OpIndex = 0
	tr1 := base
	tr1.OpIndex = 1

	if err := store.InsertTrade(ctx, tr0); err != nil {
		t.Fatalf("op=0: %v", err)
	}
	if err := store.InsertTrade(ctx, tr1); err != nil {
		t.Fatalf("op=1: %v", err)
	}

	n, err := store.CountTrades(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("CountTrades = %d, want 2 — multi-op trades dropped?", n)
	}
}

// startTimescale is extracted so both tests can share it without
// violating "no shared fixture" — each test starts its own
// container. Returns the connection DSN.
func startTimescale(t *testing.T, ctx context.Context) string {
	t.Helper()
	pg, err := tcpostgres.Run(ctx,
		"timescale/timescaledb:2.17.2-pg15",
		tcpostgres.WithDatabase("ratesengine"),
		tcpostgres.WithUsername("ratesengine"),
		tcpostgres.WithPassword("ratesengine-test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start timescale: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn str: %v", err)
	}
	// Pre-enable extension (dev stack does this via init script).
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS timescaledb"); err != nil {
		t.Fatalf("create extension: %v", err)
	}
	return dsn
}

func applyMigrations(t *testing.T, dsn string) {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	m, err := migrate.New("file://"+migrationsDir, dsn)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
}
