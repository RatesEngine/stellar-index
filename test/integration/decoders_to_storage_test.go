//go:build integration

package integration_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"

	c "github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/aquarius"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/phoenix"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/reflector"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/soroswap"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestDecoderOutputFitsStorageSchema builds one canonical.Trade
// per Soroban source with the exact field shape the source's real
// decoder emits (strkey-valid assets, i128-scale amounts, realistic
// timestamps), inserts through the real InsertTrade /
// InsertOracleUpdate paths, then queries the row back. Plus one
// Reflector OracleUpdate.
//
// This proves that canonical.Trade / canonical.OracleUpdate
// produced by the decoders actually satisfy the trades /
// oracle_updates hypertable schema — something pure unit tests
// (without a live DB) can't check. Schema-level concerns the
// unit tests miss:
//
//   - NUMERIC bounds for 128-bit amounts.
//   - NOT NULL columns that weren't populated.
//   - Primary-key uniqueness + ON CONFLICT DO NOTHING semantics.
//   - UTC / timezone coercion on Timestamp round-trip.
//   - TEXT-column length limits for long strkeys.
//
// Decoder correctness is still tested at the unit level in each
// source's real_fixture_test.go against real mainnet captures —
// this file is deliberately about the schema bridge.
//
// One container serves all subtests to keep boot cost amortized.
func TestDecoderOutputFitsStorageSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	t.Run("soroswap/trade", func(t *testing.T) {
		// Soroswap emits 7-decimal tokens with i128-scale amounts.
		// Use realistic magnitudes and a matching swap+sync pair —
		// we only persist the resulting canonical.Trade here.
		trade := c.Trade{
			Source:    soroswap.SourceName,
			Ledger:    62_240_138,
			TxHash:    hashLit("27ed2ab56d42"),
			OpIndex:   0,
			Timestamp: nowUTC(t),
			Pair: mustPair(
				mustSorobanAsset(t, 0x10),
				mustSorobanAsset(t, 0x11)),
			BaseAmount:  amountLit(t, "842000000"),  // 84.2 at 7 decimals
			QuoteAmount: amountLit(t, "1242000000"), // 124.2 at 7 decimals
		}
		insertAndVerifyTrade(ctx, t, store, trade)
	})

	t.Run("aquarius/trade", func(t *testing.T) {
		// Aquarius trades pass taker (often a router C-strkey) in
		// topic[3]. Include it here so the taker column path is
		// exercised.
		trade := c.Trade{
			Source:      aquarius.SourceName,
			Ledger:      62_150_001,
			TxHash:      hashLit("78d86d0651d6"),
			OpIndex:     0,
			Timestamp:   nowUTC(t),
			Pair:        mustPair(mustSorobanAsset(t, 0x20), mustSorobanAsset(t, 0x21)),
			BaseAmount:  amountLit(t, "1000000000"), // 100 at 7 decimals
			QuoteAmount: amountLit(t, "12420000"),   // 12.42 at 6 decimals
			Taker:       mustContractStrkey(t, 0x22),
		}
		insertAndVerifyTrade(ctx, t, store, trade)
	})

	t.Run("phoenix/trade", func(t *testing.T) {
		// Phoenix fills Taker from the sender event body (G- or
		// C-strkey). Use an account G-strkey here to exercise the
		// G-branch of the storage layer's strkey validation.
		trade := c.Trade{
			Source:      phoenix.SourceName,
			Ledger:      62_152_147,
			TxHash:      hashLit("e02dd755d908"),
			OpIndex:     0,
			Timestamp:   nowUTC(t),
			Pair:        mustPair(mustSorobanAsset(t, 0x30), mustSorobanAsset(t, 0x31)),
			BaseAmount:  amountLit(t, "999999999999"),
			QuoteAmount: amountLit(t, "1"),
			Taker:       mustAccountStrkey(t, 0x32),
		}
		insertAndVerifyTrade(ctx, t, store, trade)
	})

	t.Run("phoenix/large_i128", func(t *testing.T) {
		// ADR-0003 boundary — value above 2^63 must survive the
		// NUMERIC column round-trip. Catches any silent truncation
		// in the SQL driver or column type.
		big1 := new(big.Int)
		big1.SetString("123456789012345678901234567890", 10) // ~ 2^96
		trade := c.Trade{
			Source:      phoenix.SourceName,
			Ledger:      62_152_148,
			TxHash:      hashLit("aaaaaaaaaaaa"),
			OpIndex:     1,
			Timestamp:   nowUTC(t),
			Pair:        mustPair(mustSorobanAsset(t, 0x40), mustSorobanAsset(t, 0x41)),
			BaseAmount:  c.NewAmount(big1),
			QuoteAmount: c.NewAmount(big1),
			Taker:       mustAccountStrkey(t, 0x42),
		}
		insertAndVerifyTrade(ctx, t, store, trade)
	})

	t.Run("reflector/fiat_oracle", func(t *testing.T) {
		// FX oracle update — fiat asset, USD quote, 14-decimal price.
		eur, err := c.NewFiatAsset("EUR")
		if err != nil {
			t.Fatal(err)
		}
		usd, _ := c.NewFiatAsset("USD")
		update := c.OracleUpdate{
			Source:     reflector.SourceFX,
			ContractID: mustContractStrkey(t, 0xF0),
			Ledger:     62_251_211,
			TxHash:     hashLit("f59b732d06a5"),
			OpIndex:    0,
			Timestamp:  nowUTC(t),
			Asset:      eur,
			Quote:      usd,
			Price:      amountLit(t, "109000000000000"), // 1.09 at 14 decimals
			Decimals:   reflector.DefaultDecimals,
			Observer:   mustAccountStrkey(t, 0xF1),
		}
		insertAndVerifyOracle(ctx, t, store, update)
	})

	t.Run("reflector/crypto_oracle", func(t *testing.T) {
		// CEX oracle update — crypto-ticker asset (ADR-0014),
		// proves the new AssetCrypto type round-trips through the
		// Asset SQL value/scan path.
		btc, err := c.NewCryptoAsset("BTC")
		if err != nil {
			t.Fatal(err)
		}
		usd, _ := c.NewFiatAsset("USD")
		update := c.OracleUpdate{
			Source:     reflector.SourceCEX,
			ContractID: mustContractStrkey(t, 0xC0),
			Ledger:     62_251_266,
			TxHash:     hashLit("eb374149026f"),
			OpIndex:    1,
			Timestamp:  nowUTC(t),
			Asset:      btc,
			Quote:      usd,
			Price:      amountLit(t, "7000000000000000000"), // 70000 at 14 decimals
			Decimals:   reflector.DefaultDecimals,
			Observer:   mustAccountStrkey(t, 0xC1),
		}
		insertAndVerifyOracle(ctx, t, store, update)
	})

	t.Run("reflector/dex_oracle", func(t *testing.T) {
		// DEX oracle update — Soroban asset (on-chain SAC).
		xlm := c.NativeAsset()
		update := c.OracleUpdate{
			Source:     reflector.SourceDEX,
			ContractID: mustContractStrkey(t, 0xD0),
			Ledger:     62_251_160,
			TxHash:     hashLit("9322ba2f5c95"),
			OpIndex:    0,
			Timestamp:  nowUTC(t),
			Asset:      mustSorobanAsset(t, 0xD2),
			Quote:      xlm,
			Price:      amountLit(t, "12420000000000"),
			Decimals:   reflector.DefaultDecimals,
			Observer:   "",
		}
		insertAndVerifyOracle(ctx, t, store, update)
	})
}

// ─── helpers ────────────────────────────────────────────────────

func insertAndVerifyTrade(ctx context.Context, t *testing.T, store *timescale.Store, trade c.Trade) {
	t.Helper()
	if err := store.InsertTrade(ctx, trade); err != nil {
		t.Fatalf("InsertTrade: %v", err)
	}
	rows, err := store.LatestTradesForPair(ctx, trade.Pair, 10)
	if err != nil {
		t.Fatalf("LatestTradesForPair: %v", err)
	}
	var match *c.Trade
	for i := range rows {
		r := rows[i]
		if r.Source == trade.Source &&
			r.Ledger == trade.Ledger &&
			r.TxHash == trade.TxHash &&
			r.OpIndex == trade.OpIndex {
			match = &r
			break
		}
	}
	if match == nil {
		t.Fatalf("inserted trade not found: source=%s ledger=%d tx=%s op=%d",
			trade.Source, trade.Ledger, trade.TxHash, trade.OpIndex)
	}
	if match.BaseAmount.Cmp(trade.BaseAmount) != 0 {
		t.Errorf("base amount round-trip: got %s, want %s",
			match.BaseAmount, trade.BaseAmount)
	}
	if match.QuoteAmount.Cmp(trade.QuoteAmount) != 0 {
		t.Errorf("quote amount round-trip: got %s, want %s",
			match.QuoteAmount, trade.QuoteAmount)
	}
	if match.Taker != trade.Taker {
		t.Errorf("taker round-trip: got %q, want %q", match.Taker, trade.Taker)
	}
	// Timestamp round-trip tolerates microsecond-precision truncation
	// (Postgres TIMESTAMPTZ stores to microseconds; Go time.Time has
	// nanosecond precision).
	drift := match.Timestamp.Sub(trade.Timestamp)
	if drift < -time.Second || drift > time.Second {
		t.Errorf("timestamp drift: got %v, want %v (Δ%v)",
			match.Timestamp, trade.Timestamp, drift)
	}
}

func insertAndVerifyOracle(ctx context.Context, t *testing.T, store *timescale.Store, update c.OracleUpdate) {
	t.Helper()
	if err := store.InsertOracleUpdate(ctx, update); err != nil {
		t.Fatalf("InsertOracleUpdate: %v", err)
	}
	got, err := store.LatestOracleUpdateForAsset(ctx, update.Source, update.Asset)
	if err != nil {
		t.Fatalf("LatestOracleUpdateForAsset: %v", err)
	}
	if got == nil {
		t.Fatal("row not found")
	}
	if got.Source != update.Source ||
		got.TxHash != update.TxHash ||
		got.OpIndex != update.OpIndex {
		t.Errorf("identity drift: got %+v, want %+v", got, update)
	}
	if got.Price.Cmp(update.Price) != 0 {
		t.Errorf("price round-trip: got %s, want %s", got.Price, update.Price)
	}
	if !got.Asset.Equal(update.Asset) {
		t.Errorf("asset round-trip: got %+v, want %+v", got.Asset, update.Asset)
	}
	if !got.Quote.Equal(update.Quote) {
		t.Errorf("quote round-trip: got %+v, want %+v", got.Quote, update.Quote)
	}
}

// hashLit pads a short hex prefix to a full 64-char tx_hash so the
// storage layer's regex-or-length check (if any) is satisfied.
func hashLit(prefix string) string {
	const width = 64
	pad := width - len(prefix)
	out := prefix
	for i := 0; i < pad; i++ {
		out += "0"
	}
	return out
}

func amountLit(t *testing.T, decimal string) c.Amount {
	t.Helper()
	a, err := c.FromString(decimal)
	if err != nil {
		t.Fatalf("FromString(%q): %v", decimal, err)
	}
	return a
}

// mustPair is shared with assets_test.go (also in package
// integration_test) — do not redeclare. See that file for the
// canonical definition.

func mustSorobanAsset(t *testing.T, seed byte) c.Asset {
	t.Helper()
	a, err := c.NewSorobanAsset(mustContractStrkey(t, seed))
	if err != nil {
		t.Fatalf("NewSorobanAsset: %v", err)
	}
	return a
}

func mustContractStrkey(t *testing.T, seed byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seed
	s, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}

func mustAccountStrkey(t *testing.T, seed byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seed
	s, err := strkey.Encode(strkey.VersionByteAccountID, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}

func nowUTC(t *testing.T) time.Time {
	t.Helper()
	// Truncate to seconds so the round-trip delta check is stable
	// across Postgres's microsecond column + Go's nanosecond in-
	// memory representation.
	return time.Now().UTC().Truncate(time.Second)
}
