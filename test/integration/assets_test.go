//go:build integration

package integration_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"

	c "github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestAssetsReader exercises DistinctAssets + HasAsset against a
// real Timescale with our migrations applied. Requires the
// `integration` build tag.
func TestAssetsReader(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Empty DB → empty list, empty HasAsset.
	got, next, err := store.DistinctAssets(ctx, "", 100)
	if err != nil {
		t.Fatalf("DistinctAssets (empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d assets", len(got))
	}
	if next != "" {
		t.Errorf("next cursor should be empty, got %q", next)
	}
	has, _ := store.HasAsset(ctx, c.NativeAsset())
	if has {
		t.Error("HasAsset should be false on empty db")
	}

	// Seed 3 assets via trades: XLM, USDC, PHOENIX.
	xlm := c.NativeAsset()
	usdc, _ := c.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	pho, _ := c.NewSorobanAsset("CBCZGGNOEUZG4CAAE7TGTQQHETZMKUT4OIPFHHPKEUX46U4KXBBZ3GLH")

	for i, pair := range []c.Pair{
		mustPair(xlm, usdc),
		mustPair(pho, usdc),
		mustPair(xlm, pho),
	} {
		tr := c.Trade{
			Source:      "test",
			Ledger:      uint32(52_000_000 + i),
			TxHash:      hexTx(i),
			OpIndex:     0,
			Timestamp:   time.Now().UTC().Truncate(time.Second).Add(time.Duration(i) * time.Second),
			Pair:        pair,
			BaseAmount:  c.NewAmount(big.NewInt(1_000_000_000)),
			QuoteAmount: c.NewAmount(big.NewInt(12_000_000)),
		}
		if err := store.InsertTrade(ctx, tr); err != nil {
			t.Fatalf("InsertTrade %d: %v", i, err)
		}
	}

	// After seeding, the distinct union is {XLM(native), USDC-G..., CBCZ...}.
	got, next, err = store.DistinctAssets(ctx, "", 100)
	if err != nil {
		t.Fatalf("DistinctAssets: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 distinct assets, got %d: %v", len(got), ids(got))
	}
	if next != "" {
		t.Errorf("next cursor should be empty when page not full, got %q", next)
	}

	for _, want := range []c.Asset{xlm, usdc, pho} {
		has, err := store.HasAsset(ctx, want)
		if err != nil {
			t.Fatalf("HasAsset %s: %v", want.String(), err)
		}
		if !has {
			t.Errorf("HasAsset(%s) = false, want true", want.String())
		}
	}
	// And a seeded-but-different asset should NOT be found.
	notInDB, _ := c.NewFiatAsset("EUR")
	if has, _ := store.HasAsset(ctx, notInDB); has {
		t.Error("HasAsset(EUR) should be false")
	}

	// F-0157 perf: an unknown classic asset must route through
	// classic_assets PK lookup and return false. The classic_assets
	// table is populated by InsertTrade's registerClassicAssetSeen
	// hook; an asset_id never seen by that hook (e.g. a random
	// 4-char code against a real-but-unrelated G-strkey) must be
	// known-unknown without touching the trades hypertable.
	bogusClassic, err := c.NewClassicAsset(
		"ZZZZ",
		"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
	)
	if err != nil {
		t.Fatalf("NewClassicAsset(ZZZZ-G...): %v", err)
	}
	has, hasErr := store.HasAsset(ctx, bogusClassic)
	if hasErr != nil {
		t.Fatalf("HasAsset(ZZZZ-G...): %v", hasErr)
	}
	if has {
		t.Errorf("HasAsset(%s) = true, want false (bogus classic asset)", bogusClassic.String())
	}
}

func TestAssetsReaderPagination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Seed 5 soroban assets with strkey-valid C-addresses derived
	// from seed bytes. Previous hand-written literals
	// (e.g. "CA001JYLG…XOWMA") were 55 chars — one short of the
	// strkey 56-char requirement, so canonical.NewSorobanAsset
	// rejected them as of 2026-04-23. strkey.Encode produces
	// checksum-valid addresses indexed deterministically by seed so
	// pagination ordering stays reproducible.
	assets := []c.Asset{
		sorobanFromSeed(t, 1),
		sorobanFromSeed(t, 2),
		sorobanFromSeed(t, 3),
		sorobanFromSeed(t, 4),
		sorobanFromSeed(t, 5),
	}
	// Seed each as BASE paired with native XLM.
	for i, a := range assets {
		tr := c.Trade{
			Source: "test", Ledger: uint32(52_000_000 + i),
			TxHash: hexTx(i), OpIndex: 0,
			Timestamp:   time.Now().UTC().Truncate(time.Second).Add(time.Duration(i) * time.Second),
			Pair:        mustPair(a, c.NativeAsset()),
			BaseAmount:  c.NewAmount(big.NewInt(1_000)),
			QuoteAmount: c.NewAmount(big.NewInt(12)),
		}
		if err := store.InsertTrade(ctx, tr); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	// Request a page size of 2 — expect 3 pages (2+2+2 where last
	// page includes 1 extra native + 1 overflow = …). Actually
	// with 5 seeded sorobans + 1 native = 6 distinct assets total.
	// Iterate with cursor until next is empty.
	var allSeen []c.Asset
	cursor := ""
	for iter := 0; iter < 10; iter++ {
		page, next, err := store.DistinctAssets(ctx, cursor, 2)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		allSeen = append(allSeen, page...)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(allSeen) != 6 {
		t.Errorf("expected 6 distinct assets across pages, got %d: %v",
			len(allSeen), ids(allSeen))
	}

	// Ordering: ascending by asset string. C... sort after "native".
	for i := 1; i < len(allSeen); i++ {
		if allSeen[i-1].String() >= allSeen[i].String() {
			t.Errorf("not sorted at %d: %q >= %q",
				i, allSeen[i-1].String(), allSeen[i].String())
		}
	}
}

// ─── helpers ──────────────────────────────────────────────────────

func mustPair(base, quote c.Asset) c.Pair {
	p, err := c.NewPair(base, quote)
	if err != nil {
		panic(err)
	}
	return p
}

func mustSorobanTest(id string) c.Asset {
	a, err := c.NewSorobanAsset(id)
	if err != nil {
		panic(err)
	}
	return a
}

// sorobanFromSeed builds a Soroban asset whose C-strkey encodes a
// 32-byte contract ID whose first byte is `seed`. Produces a valid
// checksum-encoded C-strkey (56 chars) so canonical.NewSorobanAsset
// accepts it. Deterministic: the same seed always yields the same
// address, preserving pagination-order reproducibility.
func sorobanFromSeed(t *testing.T, seed byte) c.Asset {
	t.Helper()
	var raw [32]byte
	raw[0] = seed
	s, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	a, err := c.NewSorobanAsset(s)
	if err != nil {
		t.Fatalf("NewSorobanAsset: %v", err)
	}
	return a
}

func hexTx(i int) string {
	// Deterministic 64-char hex tx-hash for fixture data.
	base := "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe"
	// Swap two chars to differentiate; trades hypertable unique
	// key tolerates duplicates via ON CONFLICT DO NOTHING, but
	// we want distinct rows for counting.
	tail := "0123456789abcdef"[i%16]
	return base[:63] + string(tail)
}

func ids(as []c.Asset) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.String()
	}
	return out
}
