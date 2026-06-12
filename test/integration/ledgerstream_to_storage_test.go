//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/support/compressxdr"
	"github.com/stellar/go-stellar-sdk/support/datastore"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
	"github.com/StellarAtlas/stellar-atlas/internal/ledgerstream"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/aquarius"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/band"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/comet"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/phoenix"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/redstone"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/reflector"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/sdex"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/soroswap"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

const testPassphrase = "Test SDF Network ; September 2015"

// TestEndToEnd_LedgerstreamToTimescale proves the full production
// ingest path works end-to-end against real infrastructure:
//
//	Galexie-shaped .xdr.zst on disk
//	  → internal/ledgerstream
//	  → internal/dispatcher (all decoders registered)
//	  → consumer.Event type-switch
//	  → internal/storage/timescale
//	  → Timescale row lands
//	  → cursor persisted
//
// The test uses the SDK's filesystem datastore (not MinIO) — the
// S3 transport is a separate concern tested in internal/ledgerstream
// (and by the r1 smoke once 165d is deployed). What's proved here
// is the wiring that cmd/ratesengine-indexer runs in production.
//
// Ledger fixtures are constructed in-test using the SDK's
// compressxdr helpers, mirroring what Galexie writes. Two
// sub-tests:
//
//  1. plumbing — bounded range of empty ledgers: the pipeline
//     handles zero-event ledgers without errors, the cursor
//     advances across all of them, and the dispatcher returns no
//     outputs.
//  2. richer fixture — a soroban-flagged envelope carrying a real
//     Reflector FX update event, whose OracleUpdate lands in
//     Timescale with the expected asset/price/timestamp. Exercises
//     the full chain: envelope hash matching → TxMetaV3.SorobanMeta
//     → tx.GetTransactionEvents() → dispatcher routing →
//     reflector decoder → timescale.InsertOracleUpdate →
//     LatestOracleUpdateForAsset round-trip.
//
// A future extension (a real LCM carrying a Soroban trade event
// through the Soroswap/Phoenix/Aquarius paths) would add
// correlation-buffer coverage; the fixture machinery for it is
// the same shape as (2).
func TestEndToEnd_LedgerstreamToTimescale(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)
	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("timescale.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	t.Run("bounded range of empty ledgers", func(t *testing.T) {
		dsDir := t.TempDir()
		// Note: SDK's BoundedRange(from, to) requires to > from,
		// so we seed + request at least 2 ledgers. Production
		// always uses unbounded (to=0); bounded ranges only show
		// up in backfill CLIs or this integration test.
		seqs := []uint32{62_000_100, 62_000_101, 62_000_102}
		seedEmptyLedgers(t, ctx, dsDir, seqs)

		disp := newFullDispatcher(t)
		lsCfg := filesystemLedgerstreamConfig(dsDir)

		events, processed, cursor := runIngest(
			ctx, t, disp, lsCfg, store,
			seqs[0], seqs[len(seqs)-1],
		)

		if processed != uint32(len(seqs)) {
			t.Errorf("processed %d ledgers, want %d", processed, len(seqs))
		}
		if events != 0 {
			t.Errorf("got %d events from empty ledgers, want 0", events)
		}
		if cursor.LastLedger != seqs[len(seqs)-1] {
			t.Errorf("cursor didn't advance to last ledger: got %d want %d",
				cursor.LastLedger, seqs[len(seqs)-1])
		}
	})

	// This is the "richer" fixture test promised in the file-level
	// doc: construct a real-shaped Soroban transaction carrying a
	// Reflector FX update event, drive it all the way through the
	// production pipeline, and verify an OracleUpdate row lands.
	//
	// Building blocks, top-down:
	//
	//  1. xdr.ContractEvent with Reflector's exact wire shape
	//     (topic[0]=Symbol("REFLECTOR"), topic[1]=Symbol("update"),
	//     topic[2]=U64 timestamp_ms, body=Map{"update_data": Vec<
	//     (Symbol("EUR"), i128)>}).
	//  2. xdr.TransactionEnvelope flagged Soroban
	//     (Ext.V=1 + SorobanData set) — IsSorobanTx() must return
	//     true so the SDK reaches TxMetaV3.SorobanMeta.Events.
	//  3. xdr.LedgerCloseMeta V1 with the envelope in a V0Components
	//     phase + matching TxProcessing (result hash = envelope hash,
	//     TxApplyProcessing = TransactionMetaV3 with the event).
	//  4. Seed into the filesystem datastore, run the existing
	//     runIngest, then query Timescale for the landed row.
	t.Run("soroban LCM with reflector FX update lands OracleUpdate", func(t *testing.T) {
		dsDir := t.TempDir()

		const fxContract = "CBKGPWGKSKZF52CFHMTRR23TBWTPMRDIYZ4O2P5VS65BMHYH4DXMCJZC"
		fxContractID := mustContractIDFromStrkey(t, fxContract)

		// Reflector publishes timestamps in ms; pick a deterministic
		// instant inside the bounded range so the OracleUpdate row we
		// assert against is easy to diff.
		const tsMs = uint64(1_745_123_456_000) // 2026-04-20T05:50:56Z
		closedAt := time.UnixMilli(int64(tsMs)).UTC()

		// One (asset, price) pair: EUR at 1.0 × 10^14 (the canonical
		// Reflector scale).
		eurPrice := big.NewInt(100_000_000_000_000)
		ev := buildReflectorFXContractEvent(t, fxContractID, tsMs,
			[]string{"EUR"}, []*big.Int{eurPrice})

		// Bounded range ≥ 2 ledgers. Second ledger is empty — the
		// meaningful assertion is that the first ledger's event lands
		// and the cursor advances past it.
		seqs := []uint32{62_100_100, 62_100_101}
		seedSorobanLedger(t, ctx, dsDir, seqs[0], closedAt, ev)
		seedEmptyLedgers(t, ctx, dsDir, seqs[1:])

		// Dispatcher with just the FX decoder registered — scopes the
		// test to exactly what we're proving. WithDecoderObserver
		// stamps a known G-strkey on the row so we can assert on it.
		// AQUA mainnet issuer — chosen because it's a real, stable
		// G-strkey that survives the strkey-CRC validation that the
		// canonical package now enforces (the prior hand-crafted
		// observer strkey "GA7QYN…" had an invalid checksum).
		const observerStrkey = "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA"
		fxDecoder := reflector.NewDecoder(reflector.VariantFX, fxContract,
			reflector.WithDecoderObserver(observerStrkey))
		disp := dispatcher.New(fxDecoder)
		lsCfg := filesystemLedgerstreamConfig(dsDir)

		events, processed, cursor := runIngest(
			ctx, t, disp, lsCfg, store,
			seqs[0], seqs[len(seqs)-1],
		)

		if processed != uint32(len(seqs)) {
			t.Errorf("processed %d ledgers, want %d", processed, len(seqs))
		}
		if events != 1 {
			t.Errorf("got %d events, want 1", events)
		}
		if cursor.LastLedger != seqs[len(seqs)-1] {
			t.Errorf("cursor didn't advance: got %d want %d",
				cursor.LastLedger, seqs[len(seqs)-1])
		}

		eur, err := canonical.NewFiatAsset("EUR")
		if err != nil {
			t.Fatalf("NewFiatAsset(EUR): %v", err)
		}
		got, err := store.LatestOracleUpdateForAsset(ctx, reflector.SourceFX, eur)
		if err != nil {
			t.Fatalf("LatestOracleUpdateForAsset: %v", err)
		}
		if got.Source != reflector.SourceFX {
			t.Errorf("Source = %q want %q", got.Source, reflector.SourceFX)
		}
		if got.ContractID != fxContract {
			t.Errorf("ContractID = %q want %q", got.ContractID, fxContract)
		}
		if got.Ledger != seqs[0] {
			t.Errorf("Ledger = %d want %d", got.Ledger, seqs[0])
		}
		if got.Price.BigInt().Cmp(eurPrice) != 0 {
			t.Errorf("Price = %s want %s", got.Price, eurPrice)
		}
		if got.Decimals != reflector.DefaultDecimals {
			t.Errorf("Decimals = %d want %d", got.Decimals, reflector.DefaultDecimals)
		}
		if got.Timestamp.UnixMilli() != int64(tsMs) {
			t.Errorf("Timestamp = %v (ms=%d) want ms=%d",
				got.Timestamp, got.Timestamp.UnixMilli(), tsMs)
		}
		if got.Observer != observerStrkey {
			t.Errorf("Observer = %q want %q", got.Observer, observerStrkey)
		}
	})

	// Redstone subtest — proves the OpArgs pathway added in PR 166:
	// the dispatcher extracts InvokeContract args from the envelope
	// and the Redstone decoder zips them against event body entries.
	// Without OpArgs the decoder has no feed_ids and can't attribute
	// prices to assets, so a passing test confirms both the wire-up
	// and the decoder logic together.
	t.Run("soroban LCM with redstone write_prices lands OracleUpdates", func(t *testing.T) {
		dsDir := t.TempDir()

		const adapterC = "CA526Y2NQWGWVVQ7RFFPGAZMU66PSYJ3UC2MTVAV4ZU7OM5BOPHDXUSG"
		adapterID := mustContractIDFromStrkey(t, adapterC)

		// Two known feeds: BTC + ETH, both in canonical.IsKnownCrypto.
		// Prices at the Redstone 8-decimal scale.
		const pkgTs = uint64(1_745_000_000_000) // ms
		const wrTs = uint64(1_745_000_060_000)
		btcPriceE8 := big.NewInt(50_000_000_000_000) // $500k
		ethPriceE8 := big.NewInt(3_500_000_000_000)  // $35k

		// Relayer strkey from a deterministic 32-byte seed — skips the
		// checksum-drift trap we hit writing redstone's unit tests.
		relayerSeed := [32]byte{
			0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
			0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
			0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
			0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
		}
		relayerG, err := strkey.Encode(strkey.VersionByteAccountID, relayerSeed[:])
		if err != nil {
			t.Fatalf("encode relayer strkey: %v", err)
		}

		ev := buildRedstoneWritePricesEvent(t, adapterID, relayerG,
			[]*big.Int{btcPriceE8, ethPriceE8}, pkgTs, wrTs)

		seqs := []uint32{62_200_100, 62_200_101}
		seedRedstoneLedger(t, ctx, dsDir, seqs[0],
			time.UnixMilli(int64(wrTs)).UTC(),
			adapterID, relayerG, []string{"BTC", "ETH"}, ev)
		seedEmptyLedgers(t, ctx, dsDir, seqs[1:])

		disp := dispatcher.New(redstone.NewDecoder(adapterC))
		lsCfg := filesystemLedgerstreamConfig(dsDir)

		events, processed, cursor := runIngest(
			ctx, t, disp, lsCfg, store,
			seqs[0], seqs[len(seqs)-1],
		)

		if processed != uint32(len(seqs)) {
			t.Errorf("processed %d ledgers, want %d", processed, len(seqs))
		}
		if events != 2 {
			t.Errorf("got %d events, want 2 (BTC+ETH)", events)
		}
		if cursor.LastLedger != seqs[len(seqs)-1] {
			t.Errorf("cursor didn't advance: got %d want %d",
				cursor.LastLedger, seqs[len(seqs)-1])
		}

		btc, err := canonical.NewCryptoAsset("BTC")
		if err != nil {
			t.Fatalf("NewCryptoAsset(BTC): %v", err)
		}
		gotBTC, err := store.LatestOracleUpdateForAsset(ctx, redstone.SourceName, btc)
		if err != nil {
			t.Fatalf("LatestOracleUpdateForAsset(BTC): %v", err)
		}
		if gotBTC.Price.BigInt().Cmp(btcPriceE8) != 0 {
			t.Errorf("BTC price = %s want %s", gotBTC.Price, btcPriceE8)
		}
		if gotBTC.Decimals != 8 {
			t.Errorf("BTC decimals = %d want 8", gotBTC.Decimals)
		}
		if gotBTC.Timestamp.UnixMilli() != int64(pkgTs) {
			t.Errorf("BTC ts = %d ms want %d", gotBTC.Timestamp.UnixMilli(), pkgTs)
		}
		if gotBTC.Observer != relayerG {
			t.Errorf("BTC observer = %q want %q", gotBTC.Observer, relayerG)
		}

		eth, err := canonical.NewCryptoAsset("ETH")
		if err != nil {
			t.Fatalf("NewCryptoAsset(ETH): %v", err)
		}
		gotETH, err := store.LatestOracleUpdateForAsset(ctx, redstone.SourceName, eth)
		if err != nil {
			t.Fatalf("LatestOracleUpdateForAsset(ETH): %v", err)
		}
		if gotETH.Price.BigInt().Cmp(ethPriceE8) != 0 {
			t.Errorf("ETH price = %s want %s", gotETH.Price, ethPriceE8)
		}
	})

	// Comet subtest — weighted-AMM SwapEvent landing a canonical.Trade.
	// Simpler than Redstone: no OpArgs dependency (tokens live in the
	// event body by field name), no correlation buffer (single-event
	// decode per swap, unlike Soroswap's swap+sync pairing).
	t.Run("soroban LCM with comet POOL.swap lands Trade", func(t *testing.T) {
		dsDir := t.TempDir()

		// Pool contract ID — shape-valid C-strkey built from a seed.
		// Comet decoder matches on topic bytes, not pool ID, so this
		// address only needs to be well-formed.
		poolSeed := [32]byte{0x09, 0xC0, 0xDE, 0x70, 0xF0, 0x01}
		for i := 6; i < 32; i++ {
			poolSeed[i] = byte(i)
		}
		if _, err := strkey.Encode(strkey.VersionByteContract, poolSeed[:]); err != nil {
			t.Fatalf("encode pool strkey: %v", err)
		}
		var poolID xdr.ContractId
		copy(poolID[:], poolSeed[:])

		// Two Soroban tokens (base+quote) with deterministic seeds so
		// NewSorobanAsset succeeds and the canonical.Pair round-trips.
		tokenInSeed := [32]byte{0x10}
		for i := 1; i < 32; i++ {
			tokenInSeed[i] = byte(0x10) ^ byte(i)
		}
		tokenOutSeed := [32]byte{0x20}
		for i := 1; i < 32; i++ {
			tokenOutSeed[i] = byte(0x20) ^ byte(i)
		}
		tokenInStrkey, err := strkey.Encode(strkey.VersionByteContract, tokenInSeed[:])
		if err != nil {
			t.Fatalf("encode tokenIn: %v", err)
		}
		tokenOutStrkey, err := strkey.Encode(strkey.VersionByteContract, tokenOutSeed[:])
		if err != nil {
			t.Fatalf("encode tokenOut: %v", err)
		}

		// Caller (trader): G-strkey.
		callerSeed := [32]byte{0x30}
		for i := 1; i < 32; i++ {
			callerSeed[i] = byte(0x30) ^ byte(i)
		}
		callerStrkey, err := strkey.Encode(strkey.VersionByteAccountID, callerSeed[:])
		if err != nil {
			t.Fatalf("encode caller: %v", err)
		}

		amountIn := big.NewInt(1_000_000_000)   // 1.0 at 9 dec
		amountOut := big.NewInt(42_500_000_000) // 42.5 at 9 dec

		ev := buildCometSwapEvent(t, poolID,
			callerStrkey, tokenInStrkey, tokenOutStrkey,
			amountIn, amountOut)

		seqs := []uint32{62_300_100, 62_300_101}
		closedAt := time.Unix(1_745_000_200, 0).UTC()
		seedSorobanLedger(t, ctx, dsDir, seqs[0], closedAt, ev)
		seedEmptyLedgers(t, ctx, dsDir, seqs[1:])

		disp := dispatcher.New(comet.NewDecoder())
		lsCfg := filesystemLedgerstreamConfig(dsDir)

		events, processed, cursor := runIngest(
			ctx, t, disp, lsCfg, store,
			seqs[0], seqs[len(seqs)-1],
		)
		if processed != uint32(len(seqs)) {
			t.Errorf("processed %d ledgers, want %d", processed, len(seqs))
		}
		if events != 1 {
			t.Errorf("got %d events, want 1", events)
		}
		if cursor.LastLedger != seqs[len(seqs)-1] {
			t.Errorf("cursor didn't advance: got %d want %d",
				cursor.LastLedger, seqs[len(seqs)-1])
		}

		base, err := canonical.NewSorobanAsset(tokenInStrkey)
		if err != nil {
			t.Fatalf("NewSorobanAsset(base): %v", err)
		}
		quote, err := canonical.NewSorobanAsset(tokenOutStrkey)
		if err != nil {
			t.Fatalf("NewSorobanAsset(quote): %v", err)
		}
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			t.Fatalf("NewPair: %v", err)
		}
		trades, err := store.LatestTradesForPair(ctx, pair, 10)
		if err != nil {
			t.Fatalf("LatestTradesForPair: %v", err)
		}
		if len(trades) != 1 {
			t.Fatalf("expected 1 trade, got %d", len(trades))
		}
		got := trades[0]
		if got.Source != comet.SourceName {
			t.Errorf("Source = %q want %q", got.Source, comet.SourceName)
		}
		if got.BaseAmount.BigInt().Cmp(amountIn) != 0 {
			t.Errorf("BaseAmount = %s want %s", got.BaseAmount, amountIn)
		}
		if got.QuoteAmount.BigInt().Cmp(amountOut) != 0 {
			t.Errorf("QuoteAmount = %s want %s", got.QuoteAmount, amountOut)
		}
		if got.Taker != callerStrkey {
			t.Errorf("Taker = %q want %q", got.Taker, callerStrkey)
		}
		if got.Ledger != seqs[0] {
			t.Errorf("Ledger = %d want %d", got.Ledger, seqs[0])
		}
	})

	// Band subtest — proves the ContractCallDecoder pathway. This is
	// the "no events" case: Band's StandardReference doesn't emit on
	// relay(), so the dispatcher routes purely on the InvokeContract
	// op itself. If the dispatcher's contract-call loop skips
	// argless/event-only ops, this test catches that.
	t.Run("soroban LCM with band relay (no events) lands OracleUpdates", func(t *testing.T) {
		dsDir := t.TempDir()

		const bandContract = "CCQXWMZVM3KRTXTUPTN53YHL272QGKF32L7XEDNZ2S6OSUFK3NFBGG5M"
		bandContractID := mustContractIDFromStrkey(t, bandContract)

		relayerSeed := [32]byte{
			0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44,
			0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44,
			0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44,
			0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44,
		}
		relayerStrkey, err := strkey.Encode(strkey.VersionByteAccountID, relayerSeed[:])
		if err != nil {
			t.Fatalf("encode relayer strkey: %v", err)
		}

		const resolveSec = uint64(1_745_000_300)
		const btcRateE9 = uint64(500_000_000_000_000) // $500k × 10^9
		const xlmRateE9 = uint64(120_000_000)         // $0.12 × 10^9

		seqs := []uint32{62_400_100, 62_400_101}
		closedAt := time.Unix(int64(resolveSec), 0).UTC()
		seedBandRelayLedger(t, ctx, dsDir, seqs[0], closedAt, bandContractID,
			relayerStrkey, []bandRate{{"BTC", btcRateE9}, {"XLM", xlmRateE9}}, resolveSec, 99)
		seedEmptyLedgers(t, ctx, dsDir, seqs[1:])

		disp := dispatcher.New() // no event decoders
		disp.AddContractCallDecoder(band.NewDecoder(bandContract))
		lsCfg := filesystemLedgerstreamConfig(dsDir)

		events, processed, cursor := runIngest(
			ctx, t, disp, lsCfg, store,
			seqs[0], seqs[len(seqs)-1],
		)
		if processed != uint32(len(seqs)) {
			t.Errorf("processed %d ledgers, want %d", processed, len(seqs))
		}
		if events != 2 {
			t.Errorf("got %d events, want 2 (BTC+XLM)", events)
		}
		if cursor.LastLedger != seqs[len(seqs)-1] {
			t.Errorf("cursor didn't advance: got %d want %d",
				cursor.LastLedger, seqs[len(seqs)-1])
		}

		btc, err := canonical.NewCryptoAsset("BTC")
		if err != nil {
			t.Fatalf("NewCryptoAsset(BTC): %v", err)
		}
		gotBTC, err := store.LatestOracleUpdateForAsset(ctx, band.SourceName, btc)
		if err != nil {
			t.Fatalf("LatestOracleUpdateForAsset(BTC): %v", err)
		}
		if gotBTC.Price.BigInt().Uint64() != btcRateE9 {
			t.Errorf("BTC rate = %s want %d", gotBTC.Price, btcRateE9)
		}
		if gotBTC.Decimals != 9 {
			t.Errorf("BTC decimals = %d want 9", gotBTC.Decimals)
		}
		if gotBTC.Timestamp.Unix() != int64(resolveSec) {
			t.Errorf("BTC ts = %v want unix %d", gotBTC.Timestamp, resolveSec)
		}
		if gotBTC.Observer != relayerStrkey {
			t.Errorf("BTC observer = %q want %q (from relay arg[0])", gotBTC.Observer, relayerStrkey)
		}

		xlm, err := canonical.NewCryptoAsset("XLM")
		if err != nil {
			t.Fatalf("NewCryptoAsset(XLM): %v", err)
		}
		gotXLM, err := store.LatestOracleUpdateForAsset(ctx, band.SourceName, xlm)
		if err != nil {
			t.Fatalf("LatestOracleUpdateForAsset(XLM): %v", err)
		}
		if gotXLM.Price.BigInt().Uint64() != xlmRateE9 {
			t.Errorf("XLM rate = %s want %d", gotXLM.Price, xlmRateE9)
		}
	})
}

// ─── helpers ─────────────────────────────────────────────────────

// runIngest mirrors cmd/ratesengine-indexer's processAndPersist
// logic in-test: stream ledgers from the datastore, dispatch them,
// persist each consumer.Event via the appropriate store insert,
// and upsert the pipeline cursor after each ledger. Returns the
// total event count emitted + number of ledgers processed + final
// cursor, so the test can assert on all three.
func runIngest(
	ctx context.Context, t *testing.T,
	disp *dispatcher.Dispatcher, lsCfg ledgerstream.Config,
	store *timescale.Store,
	from, to uint32,
) (events int, processed uint32, cursor timescale.Cursor) {
	t.Helper()

	err := ledgerstream.Stream(ctx, lsCfg, from, to, func(lcm xdr.LedgerCloseMeta) error {
		outputs, err := disp.ProcessLedger(lcm, testPassphrase)
		if err != nil {
			t.Logf("dispatcher rejected ledger %d: %v", lcm.LedgerSequence(), err)
			return nil
		}
		for _, ev := range outputs {
			if err := persistInTest(ctx, store, ev); err != nil {
				t.Errorf("persist %s: %v", ev.EventKind(), err)
				continue
			}
			events++
		}
		processed++
		if err := store.UpsertCursor(ctx, "ledgerstream", "", lcm.LedgerSequence()); err != nil {
			t.Errorf("upsert cursor at ledger %d: %v", lcm.LedgerSequence(), err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ledgerstream.Stream: %v", err)
	}

	cursor, err = store.GetCursor(ctx, "ledgerstream", "")
	if err != nil {
		t.Fatalf("read back cursor: %v", err)
	}
	return events, processed, cursor
}

// persistInTest mirrors handleOneEvent in cmd/ratesengine-indexer
// but without the panic recovery + metrics plumbing — the test
// wants raw errors to surface.
func persistInTest(ctx context.Context, store *timescale.Store, ev consumer.Event) error {
	switch e := ev.(type) {
	case soroswap.TradeEvent:
		return store.InsertTrade(ctx, e.Trade)
	case aquarius.TradeEvent:
		return store.InsertTrade(ctx, e.Trade)
	case phoenix.TradeEvent:
		return store.InsertTrade(ctx, e.Trade)
	case sdex.TradeEvent:
		return store.InsertTrade(ctx, e.Trade)
	case reflector.UpdateEvent:
		return store.InsertOracleUpdate(ctx, e.Update)
	case redstone.UpdateEvent:
		return store.InsertOracleUpdate(ctx, e.Update)
	case band.UpdateEvent:
		return store.InsertOracleUpdate(ctx, e.Update)
	case comet.TradeEvent:
		return store.InsertTrade(ctx, e.Trade)
	}
	return fmt.Errorf("persistInTest: unhandled event %T", ev)
}

// newFullDispatcher registers every production decoder — the
// same set cmd/ratesengine-indexer wires from config when all
// sources are enabled. Reflector contracts use placeholders because
// the empty-ledger tests don't emit events that would be matched
// against them.
func newFullDispatcher(t *testing.T) *dispatcher.Dispatcher {
	t.Helper()
	d := dispatcher.New(
		reflector.NewDecoder(reflector.VariantDEX, "CALI2BYU2JE6WVRUFYTS6MSBNEHGJ35P4AVCZYF3B6QOE3QKOB2PLE6M"),
		reflector.NewDecoder(reflector.VariantCEX, "CAFJZQWSED6YAWZU3GWRTOCNPPCGBN32L7QV43XX5LZLFTK6JLN34DLN"),
		reflector.NewDecoder(reflector.VariantFX, "CBKGPWGKSKZF52CFHMTRR23TBWTPMRDIYZ4O2P5VS65BMHYH4DXMCJZC"),
		soroswap.NewDecoder(),
		aquarius.NewDecoder(),
		phoenix.NewDecoder(),
	)
	d.AddOpDecoder(sdex.NewDecoder())
	return d
}

// filesystemLedgerstreamConfig builds a ledgerstream.Config
// pointing at a local directory — the same config shape
// cmd/ratesengine-indexer would produce for an S3 datastore, just
// with Type=Filesystem so we don't need MinIO in the unit
// integration suite.
func filesystemLedgerstreamConfig(dir string) ledgerstream.Config {
	return ledgerstream.Config{
		DataStore: datastore.DataStoreConfig{
			Type:              "Filesystem",
			Params:            map[string]string{"destination_path": dir},
			Schema:            datastore.DataStoreSchema{LedgersPerFile: 1, FilesPerPartition: 1},
			NetworkPassphrase: testPassphrase,
			Compression:       "zstd",
		},
	}
}

// seedEmptyLedgers writes one valid-but-empty xdr.LedgerCloseMeta
// per sequence in `seqs` to the filesystem datastore rooted at
// `dir`. Publishes the datastore manifest so the ledgerstream's
// LoadSchema call finds it.
//
// "Empty" here means: a LedgerHeader with the right sequence, a
// valid (empty) GeneralizedTransactionSet, no transactions. The
// SDK's ingest reader handles this cleanly — the tx loop hits
// io.EOF immediately and the dispatcher returns zero outputs.
func seedEmptyLedgers(t *testing.T, ctx context.Context, dir string, seqs []uint32) {
	t.Helper()
	store, err := datastore.NewFilesystemDataStoreWithPath(dir)
	if err != nil {
		t.Fatalf("open filesystem datastore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := datastore.DataStoreConfig{
		Type:              "Filesystem",
		Params:            map[string]string{"destination_path": dir},
		Schema:            datastore.DataStoreSchema{LedgersPerFile: 1, FilesPerPartition: 1},
		NetworkPassphrase: testPassphrase,
		Compression:       "zstd",
	}
	if _, _, err := datastore.PublishConfig(ctx, store, cfg); err != nil {
		t.Fatalf("publish manifest: %v", err)
	}

	for _, seq := range seqs {
		lcm := xdr.LedgerCloseMeta{
			V: 1,
			V1: &xdr.LedgerCloseMetaV1{
				LedgerHeader: xdr.LedgerHeaderHistoryEntry{
					Header: xdr.LedgerHeader{
						LedgerSeq: xdr.Uint32(seq),
					},
				},
				TxSet: xdr.GeneralizedTransactionSet{
					V:       1,
					V1TxSet: &xdr.TransactionSetV1{},
				},
			},
		}
		batch := xdr.LedgerCloseMetaBatch{
			StartSequence:    xdr.Uint32(seq),
			EndSequence:      xdr.Uint32(seq),
			LedgerCloseMetas: []xdr.LedgerCloseMeta{lcm},
		}
		encoder := compressxdr.NewXDREncoder(compressxdr.DefaultCompressor, batch)
		var buf bytes.Buffer
		if _, err := encoder.WriteTo(&buf); err != nil {
			t.Fatalf("encode batch seq=%d: %v", seq, err)
		}
		key := cfg.Schema.GetObjectKeyFromSequenceNumber(seq)
		if err := store.PutFile(ctx, key, byteSliceWriterTo(buf.Bytes()), nil); err != nil {
			t.Fatalf("put seq=%d: %v", seq, err)
		}
	}
}

// byteSliceWriterTo adapts a []byte to io.WriterTo — the
// interface datastore.DataStore.PutFile expects.
type byteSliceWriterTo []byte

func (b byteSliceWriterTo) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(b)
	return int64(n), err
}

// ─── richer Soroban-event fixture helpers ───────────────────────
// Everything below is scaffolding for the "soroban LCM with
// reflector FX update" subtest. The key design constraint: the
// LCM we construct must satisfy both (a) the SDK reader's
// envelope-hash ↔ TxProcessing matching (storeTransactions at
// ingest/ledger_transaction_reader.go) and (b) the dispatcher's
// topic-byte-equality match against the Reflector decoder.
//
// We lean on the SDK's own encoders for (a) — HashTransactionInEnvelope
// + compressxdr.NewXDREncoder — and on internal/scval for (b).
// Nothing here hand-rolls XDR bytes.

// mustContractIDFromStrkey decodes a C-strkey into a 32-byte
// xdr.ContractId. The reflector decoder compares events by the
// contract's strkey form, so we need the inverse of that here —
// the decoder gets strkey back from contractIDToStrkey() inside
// the dispatcher.
func mustContractIDFromStrkey(t *testing.T, s string) xdr.ContractId {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteContract, s)
	if err != nil {
		t.Fatalf("strkey decode %q: %v", s, err)
	}
	if len(raw) != 32 {
		t.Fatalf("contract strkey %q decoded to %d bytes, want 32", s, len(raw))
	}
	var cid xdr.ContractId
	copy(cid[:], raw)
	return cid
}

// buildReflectorFXContractEvent constructs a single xdr.ContractEvent
// matching the Reflector FX oracle's exact on-wire shape. The FX
// variant's update_data entries all use Asset::Other(Symbol) (fiat
// tickers); callers pass a parallel pair of `symbols` and `prices`.
//
// The event body mirrors the SDK-encoded fixture shape in
// internal/sources/reflector/decode_test.go:encodeUpdateBody —
// reproduced here so the integration test doesn't reach into the
// reflector package's test helpers.
func buildReflectorFXContractEvent(
	t *testing.T,
	contractID xdr.ContractId,
	tsMs uint64,
	symbols []string,
	prices []*big.Int,
) xdr.ContractEvent {
	t.Helper()
	if len(symbols) != len(prices) {
		t.Fatalf("symbols/prices length mismatch: %d vs %d", len(symbols), len(prices))
	}

	// topic[0]=Symbol("REFLECTOR"), topic[1]=Symbol("update") — but
	// unlike the events.Event pathway (base64 strings), ContractEvent
	// carries *decoded* ScVals. We pass through the same scval encoder
	// so the round-trip (encode → dispatcher re-encodes to b64) lands
	// at identical topic bytes, keeping the byte-equality match live.
	refSym := xdr.ScSymbol(reflector.EventTopic0)
	updSym := xdr.ScSymbol(reflector.EventTopic1)
	tsVal := xdr.Uint64(tsMs)
	topicRef := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &refSym}
	topicUpd := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &updSym}
	topicTs := xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &tsVal}

	// Build the update_data Vec<(Symbol, i128)>.
	tuples := make([]xdr.ScVal, len(symbols))
	for i := range symbols {
		sym := xdr.ScSymbol(symbols[i])
		symSv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
		hi, lo := splitBigInt128For128Parts(prices[i])
		parts := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
		priceSv := xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &parts}
		pair := xdr.ScVec{symSv, priceSv}
		pp := &pair
		tuples[i] = xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pp}
	}
	updVec := xdr.ScVec(tuples)
	pUpdVec := &updVec
	innerVec := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pUpdVec}

	keySym := xdr.ScSymbol("update_data")
	keySv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &keySym}
	scMap := xdr.ScMap{xdr.ScMapEntry{Key: keySv, Val: innerVec}}
	pMap := &scMap
	body := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pMap}

	cid := contractID
	return xdr.ContractEvent{
		Type:       xdr.ContractEventTypeContract,
		ContractId: &cid,
		Body: xdr.ContractEventBody{
			V: 0,
			V0: &xdr.ContractEventV0{
				Topics: xdr.ScVec{topicRef, topicUpd, topicTs},
				Data:   body,
			},
		},
	}
}

// splitBigInt128For128Parts is the integration-test copy of the
// helper in internal/sources/reflector/decode_test.go. It splits a
// *big.Int into the (hi int64, lo uint64) pair that xdr.Int128Parts
// expects, handling negative values via two's-complement unwind.
func splitBigInt128For128Parts(n *big.Int) (hi int64, lo uint64) {
	twoTo64 := new(big.Int).Lsh(big.NewInt(1), 64)
	mask64 := new(big.Int).Sub(twoTo64, big.NewInt(1))
	if n.Sign() >= 0 {
		loBig := new(big.Int).And(n, mask64)
		hiBig := new(big.Int).Rsh(n, 64)
		return hiBig.Int64(), loBig.Uint64()
	}
	twoTo128 := new(big.Int).Lsh(big.NewInt(1), 128)
	u := new(big.Int).Add(twoTo128, n)
	loBig := new(big.Int).And(u, mask64)
	hiBig := new(big.Int).Rsh(u, 64)
	return int64(hiBig.Uint64()), loBig.Uint64()
}

// seedSorobanLedger writes a single ledger containing one Soroban
// transaction whose TxMetaV3 carries the given ContractEvent. The
// envelope is the minimal Soroban-flagged shape from the SDK's test
// suite (ingest/ledger_transaction_test.go:48-58): V1 with
// Ext.V=1 and SorobanData set; IsSorobanTx() returns true so
// tx.GetTransactionEvents() reaches SorobanMeta.Events.
//
// The tx hash stored in TxProcessing[i].Result.TransactionHash must
// match HashTransactionInEnvelope(envelope, passphrase) — otherwise
// the reader's hash-lookup fails ("unknown tx hash in LedgerCloseMeta").
func seedSorobanLedger(
	t *testing.T,
	ctx context.Context,
	dir string,
	seq uint32,
	closedAt time.Time,
	ev xdr.ContractEvent,
) {
	t.Helper()
	store, err := datastore.NewFilesystemDataStoreWithPath(dir)
	if err != nil {
		t.Fatalf("open filesystem datastore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := datastore.DataStoreConfig{
		Type:              "Filesystem",
		Params:            map[string]string{"destination_path": dir},
		Schema:            datastore.DataStoreSchema{LedgersPerFile: 1, FilesPerPartition: 1},
		NetworkPassphrase: testPassphrase,
		Compression:       "zstd",
	}
	// Idempotent — PublishConfig is a no-op once the manifest is
	// present, so either subtest order works.
	if _, _, err := datastore.PublishConfig(ctx, store, cfg); err != nil {
		t.Fatalf("publish manifest: %v", err)
	}

	// ─── envelope ────────────────────────────────────────────
	// Soroban tx envelope shape (same as someSorobanTxEnvelope
	// in the SDK's tests). Empty Operations slice is fine —
	// tx.GetTransactionEvents() pulls from SorobanMeta in V3 meta,
	// not from the op list. The envelope source account is a
	// deterministic G-strkey so accountIDToStrkey() inside the
	// dispatcher doesn't error.
	srcSeed := [32]byte{0x10, 0xDE, 0xAD, 0xBE, 0xEF}
	for i := 5; i < 32; i++ {
		srcSeed[i] = byte(i)
	}
	srcMuxed, err := xdr.NewMuxedAccount(xdr.CryptoKeyTypeKeyTypeEd25519, xdr.Uint256(srcSeed))
	if err != nil {
		t.Fatalf("NewMuxedAccount: %v", err)
	}
	envelope := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: srcMuxed,
				Fee:           100,
				SeqNum:        1,
				Cond: xdr.Preconditions{
					Type: xdr.PreconditionTypePrecondNone,
				},
				Memo:       xdr.Memo{Type: xdr.MemoTypeMemoNone},
				Operations: []xdr.Operation{},
				Ext: xdr.TransactionExt{
					V:           1,
					SorobanData: &xdr.SorobanTransactionData{},
				},
			},
		},
	}
	hash, err := network.HashTransactionInEnvelope(envelope, testPassphrase)
	if err != nil {
		t.Fatalf("hash envelope: %v", err)
	}

	// ─── tx result ───────────────────────────────────────────
	// TxSuccess code with an empty Results slice → Successful()
	// is true AND OperationResults() returns ([], true). The
	// dispatcher's classic-op loop iterates over zero ops in that
	// case, skipping cleanly.
	emptyOpResults := []xdr.OperationResult{}
	result := xdr.TransactionResultPair{
		TransactionHash: xdr.Hash(hash),
		Result: xdr.TransactionResult{
			FeeCharged: 100,
			Result: xdr.TransactionResultResult{
				Code:    xdr.TransactionResultCodeTxSuccess,
				Results: &emptyOpResults,
			},
		},
	}

	// ─── tx meta with our event ──────────────────────────────
	// SorobanTransactionMeta.ReturnValue is an ScVal (not a pointer)
	// — its zero value marshals as ScVal{Type:0 = ScvBool} with
	// B=nil, which nil-derefs inside EncodeTo. Force it to ScvVoid
	// so the encoder has a valid arm with no payload.
	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				Events:      []xdr.ContractEvent{ev},
				ReturnValue: xdr.ScVal{Type: xdr.ScValTypeScvVoid},
			},
		},
	}

	// ─── assemble the LCM ────────────────────────────────────
	txProc := xdr.TransactionResultMeta{
		Result:            result,
		FeeProcessing:     xdr.LedgerEntryChanges{},
		TxApplyProcessing: meta,
	}
	phase := xdr.TransactionPhase{
		V: 0,
		V0Components: &[]xdr.TxSetComponent{
			{
				Type: xdr.TxSetComponentTypeTxsetCompTxsMaybeDiscountedFee,
				TxsMaybeDiscountedFee: &xdr.TxSetComponentTxsMaybeDiscountedFee{
					Txs: []xdr.TransactionEnvelope{envelope},
				},
			},
		},
	}
	lcm := xdr.LedgerCloseMeta{
		V: 1,
		V1: &xdr.LedgerCloseMetaV1{
			LedgerHeader: xdr.LedgerHeaderHistoryEntry{
				Header: xdr.LedgerHeader{
					LedgerSeq: xdr.Uint32(seq),
					ScpValue: xdr.StellarValue{
						CloseTime: xdr.TimePoint(closedAt.Unix()),
					},
				},
			},
			TxSet: xdr.GeneralizedTransactionSet{
				V: 1,
				V1TxSet: &xdr.TransactionSetV1{
					Phases: []xdr.TransactionPhase{phase},
				},
			},
			TxProcessing: []xdr.TransactionResultMeta{txProc},
		},
	}

	// ─── encode + publish ────────────────────────────────────
	batch := xdr.LedgerCloseMetaBatch{
		StartSequence:    xdr.Uint32(seq),
		EndSequence:      xdr.Uint32(seq),
		LedgerCloseMetas: []xdr.LedgerCloseMeta{lcm},
	}
	encoder := compressxdr.NewXDREncoder(compressxdr.DefaultCompressor, batch)
	var buf bytes.Buffer
	if _, err := encoder.WriteTo(&buf); err != nil {
		t.Fatalf("encode batch seq=%d: %v", seq, err)
	}
	key := cfg.Schema.GetObjectKeyFromSequenceNumber(seq)
	if err := store.PutFile(ctx, key, byteSliceWriterTo(buf.Bytes()), nil); err != nil {
		t.Fatalf("put seq=%d: %v", seq, err)
	}
}

// ─── band-specific fixture helpers ──────────────────────────────
// Band's Stellar contract emits NO events — it's purely observed
// via the InvokeContract op args (the dispatcher's ContractCallDecoder
// path). This helper seeds a ledger whose Soroban tx invokes
// `StandardReference.relay(from, symbol_rates, resolve_time,
// request_id)` with the given arg payload and no emitted events.

// bandRate is one (symbol, u64_rate_at_E9) pair — mirrors the
// Vec<(Symbol, u64)> shape of Band's symbol_rates arg.
type bandRate struct {
	Symbol string
	Rate   uint64
}

// seedBandRelayLedger writes one ledger containing a single
// Soroban InvokeHostFunction op that targets Band's
// StandardReference.relay(from, symbol_rates, resolve_time,
// request_id). The tx succeeds with zero emitted events — the
// dispatcher reaches the decoder via the ContractCall path only.
func seedBandRelayLedger(
	t *testing.T,
	ctx context.Context,
	dir string,
	seq uint32,
	closedAt time.Time,
	bandContractID xdr.ContractId,
	relayerStrkey string,
	rates []bandRate,
	resolveTime uint64,
	requestID uint64,
) {
	t.Helper()
	store, err := datastore.NewFilesystemDataStoreWithPath(dir)
	if err != nil {
		t.Fatalf("open filesystem datastore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := datastore.DataStoreConfig{
		Type:              "Filesystem",
		Params:            map[string]string{"destination_path": dir},
		Schema:            datastore.DataStoreSchema{LedgersPerFile: 1, FilesPerPartition: 1},
		NetworkPassphrase: testPassphrase,
		Compression:       "zstd",
	}
	if _, _, err := datastore.PublishConfig(ctx, store, cfg); err != nil {
		t.Fatalf("publish manifest: %v", err)
	}

	// ─── InvokeContract op: StandardReference.relay(…) ───
	contractAddr := xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &bandContractID,
	}
	fromSv := accountStrkeyToAddressScVal(t, relayerStrkey)

	// symbol_rates: Vec<(Symbol, u64)>
	rateItems := make([]xdr.ScVal, len(rates))
	for i, r := range rates {
		sym := xdr.ScSymbol(r.Symbol)
		symSv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
		u := xdr.Uint64(r.Rate)
		rateSv := xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &u}
		tuple := xdr.ScVec{symSv, rateSv}
		pt := &tuple
		rateItems[i] = xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pt}
	}
	outer := xdr.ScVec(rateItems)
	po := &outer
	symbolRatesSv := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &po}

	// resolve_time: u64, request_id: u64
	rt := xdr.Uint64(resolveTime)
	resolveSv := xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &rt}
	rid := xdr.Uint64(requestID)
	requestSv := xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &rid}

	invokeOp := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
				HostFunction: xdr.HostFunction{
					Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
					InvokeContract: &xdr.InvokeContractArgs{
						ContractAddress: contractAddr,
						FunctionName:    xdr.ScSymbol(band.FnRelay),
						Args:            []xdr.ScVal{fromSv, symbolRatesSv, resolveSv, requestSv},
					},
				},
			},
		},
	}

	// ─── envelope ────────────────────────────────────────────
	srcSeed := [32]byte{0x30, 0xBA, 0x5D, 0x00}
	for i := 4; i < 32; i++ {
		srcSeed[i] = byte(i) + 0x50
	}
	srcMuxed, err := xdr.NewMuxedAccount(xdr.CryptoKeyTypeKeyTypeEd25519, xdr.Uint256(srcSeed))
	if err != nil {
		t.Fatalf("NewMuxedAccount: %v", err)
	}
	envelope := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: srcMuxed,
				Fee:           300,
				SeqNum:        1,
				Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
				Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
				Operations:    []xdr.Operation{invokeOp},
				Ext: xdr.TransactionExt{
					V:           1,
					SorobanData: &xdr.SorobanTransactionData{},
				},
			},
		},
	}
	hash, err := network.HashTransactionInEnvelope(envelope, testPassphrase)
	if err != nil {
		t.Fatalf("hash envelope: %v", err)
	}

	// ─── result: TxSuccess + one InvokeHostFunctionResult ────
	invokeRes := xdr.InvokeHostFunctionResult{
		Code:    xdr.InvokeHostFunctionResultCodeInvokeHostFunctionSuccess,
		Success: new(xdr.Hash),
	}
	opResults := []xdr.OperationResult{{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type:                     xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionResult: &invokeRes,
		},
	}}
	result := xdr.TransactionResultPair{
		TransactionHash: xdr.Hash(hash),
		Result: xdr.TransactionResult{
			FeeCharged: 300,
			Result: xdr.TransactionResultResult{
				Code:    xdr.TransactionResultCodeTxSuccess,
				Results: &opResults,
			},
		},
	}

	// ─── meta with EMPTY events slice — the whole point of Band ──
	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				Events:      []xdr.ContractEvent{}, // ← explicitly empty
				ReturnValue: xdr.ScVal{Type: xdr.ScValTypeScvVoid},
			},
		},
	}

	txProc := xdr.TransactionResultMeta{
		Result:            result,
		FeeProcessing:     xdr.LedgerEntryChanges{},
		TxApplyProcessing: meta,
	}
	phase := xdr.TransactionPhase{
		V: 0,
		V0Components: &[]xdr.TxSetComponent{
			{
				Type: xdr.TxSetComponentTypeTxsetCompTxsMaybeDiscountedFee,
				TxsMaybeDiscountedFee: &xdr.TxSetComponentTxsMaybeDiscountedFee{
					Txs: []xdr.TransactionEnvelope{envelope},
				},
			},
		},
	}
	lcm := xdr.LedgerCloseMeta{
		V: 1,
		V1: &xdr.LedgerCloseMetaV1{
			LedgerHeader: xdr.LedgerHeaderHistoryEntry{
				Header: xdr.LedgerHeader{
					LedgerSeq: xdr.Uint32(seq),
					ScpValue:  xdr.StellarValue{CloseTime: xdr.TimePoint(closedAt.Unix())},
				},
			},
			TxSet: xdr.GeneralizedTransactionSet{
				V: 1,
				V1TxSet: &xdr.TransactionSetV1{
					Phases: []xdr.TransactionPhase{phase},
				},
			},
			TxProcessing: []xdr.TransactionResultMeta{txProc},
		},
	}

	batch := xdr.LedgerCloseMetaBatch{
		StartSequence:    xdr.Uint32(seq),
		EndSequence:      xdr.Uint32(seq),
		LedgerCloseMetas: []xdr.LedgerCloseMeta{lcm},
	}
	encoder := compressxdr.NewXDREncoder(compressxdr.DefaultCompressor, batch)
	var buf bytes.Buffer
	if _, err := encoder.WriteTo(&buf); err != nil {
		t.Fatalf("encode batch seq=%d: %v", seq, err)
	}
	key := cfg.Schema.GetObjectKeyFromSequenceNumber(seq)
	if err := store.PutFile(ctx, key, byteSliceWriterTo(buf.Bytes()), nil); err != nil {
		t.Fatalf("put seq=%d: %v", seq, err)
	}
}

// ─── comet-specific fixture helpers ─────────────────────────────

// buildCometSwapEvent assembles a Comet POOL.swap ContractEvent:
//
//	topic  = (Symbol("POOL"), Symbol("swap"))
//	body   = Map { "caller": Address, "token_in": Address,
//	               "token_out": Address, "token_amount_in": i128,
//	               "token_amount_out": i128 }
//
// Mirrors the contract's `env.events().publish((POOL, swap), event)`
// call at comet-contracts/contracts/src/c_pool/call_logic/pool.rs:191.
func buildCometSwapEvent(
	t *testing.T,
	poolContractID xdr.ContractId,
	caller, tokenIn, tokenOut string,
	amountIn, amountOut *big.Int,
) xdr.ContractEvent {
	t.Helper()

	poolSym := xdr.ScSymbol(comet.EventTopic0)
	swapSym := xdr.ScSymbol(comet.EventSwap)
	topicPool := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &poolSym}
	topicSwap := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &swapSym}

	callerSv := accountStrkeyToAddressScVal(t, caller)
	tokenInSv := contractStrkeyToAddressScVal(t, tokenIn)
	tokenOutSv := contractStrkeyToAddressScVal(t, tokenOut)
	amountInSv := bigIntToI128ScVal(t, amountIn)
	amountOutSv := bigIntToI128ScVal(t, amountOut)

	keys := []string{"caller", "token_amount_in", "token_amount_out", "token_in", "token_out"}
	vals := []xdr.ScVal{callerSv, amountInSv, amountOutSv, tokenInSv, tokenOutSv}
	m := make(xdr.ScMap, len(keys))
	for i, k := range keys {
		sym := xdr.ScSymbol(k)
		m[i] = xdr.ScMapEntry{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
			Val: vals[i],
		}
	}
	pm := &m
	body := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pm}

	cid := poolContractID
	return xdr.ContractEvent{
		Type:       xdr.ContractEventTypeContract,
		ContractId: &cid,
		Body: xdr.ContractEventBody{
			V: 0,
			V0: &xdr.ContractEventV0{
				Topics: xdr.ScVec{topicPool, topicSwap},
				Data:   body,
			},
		},
	}
}

// contractStrkeyToAddressScVal encodes a C-strkey as ScVal::Address.
func contractStrkeyToAddressScVal(t *testing.T, cStrkey string) xdr.ScVal {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteContract, cStrkey)
	if err != nil {
		t.Fatalf("strkey decode %q: %v", cStrkey, err)
	}
	var cid xdr.ContractId
	copy(cid[:], raw)
	addr := xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &cid,
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
}

// bigIntToI128ScVal splits a signed *big.Int into Hi/Lo with
// two's-complement semantics for negatives.
func bigIntToI128ScVal(t *testing.T, n *big.Int) xdr.ScVal {
	t.Helper()
	twoTo64 := new(big.Int).Lsh(big.NewInt(1), 64)
	mask64 := new(big.Int).Sub(twoTo64, big.NewInt(1))
	var hi int64
	var lo uint64
	if n.Sign() >= 0 {
		loBig := new(big.Int).And(n, mask64)
		hiBig := new(big.Int).Rsh(n, 64)
		hi = hiBig.Int64()
		lo = loBig.Uint64()
	} else {
		twoTo128 := new(big.Int).Lsh(big.NewInt(1), 128)
		u := new(big.Int).Add(twoTo128, n)
		loBig := new(big.Int).And(u, mask64)
		hiBig := new(big.Int).Rsh(u, 64)
		hi = int64(hiBig.Uint64())
		lo = loBig.Uint64()
	}
	p := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p}
}

// ─── redstone-specific fixture helpers ──────────────────────────
// Mirrors the Reflector helpers above but targets the RedStone
// Adapter's WritePrices shape (single REDSTONE topic, Map body with
// updater + updated_feeds, plus InvokeContract op args carrying the
// feed_ids list). These prove the OpArgs plumbing end-to-end.

// buildRedstoneWritePricesEvent constructs the RedStone Adapter's
// WritePrices event body:
//
//	topic[0] = Symbol("REDSTONE")
//	body     = Map { "updater": Address, "updated_feeds": Vec<PriceData> }
//	PriceData = Map { "price": U256, "package_timestamp": u64,
//	                  "write_timestamp": u64 }
func buildRedstoneWritePricesEvent(
	t *testing.T,
	contractID xdr.ContractId,
	updaterStrkey string,
	prices []*big.Int,
	packageTs, writeTs uint64,
) xdr.ContractEvent {
	t.Helper()

	redstoneSym := xdr.ScSymbol(redstone.EventTopic0)
	topicRedstone := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &redstoneSym}

	// updater address
	updaterAddrSv := accountStrkeyToAddressScVal(t, updaterStrkey)

	// updated_feeds: Vec<PriceData>
	items := make([]xdr.ScVal, len(prices))
	for i, p := range prices {
		priceSv := bigIntToU256ScVal(t, p)
		pkgU := xdr.Uint64(packageTs)
		wrU := xdr.Uint64(writeTs)
		pkgSv := xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &pkgU}
		wrSv := xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &wrU}
		pdKeys := []string{"price", "package_timestamp", "write_timestamp"}
		pdVals := []xdr.ScVal{priceSv, pkgSv, wrSv}
		pdMap := make(xdr.ScMap, len(pdKeys))
		for j, k := range pdKeys {
			sym := xdr.ScSymbol(k)
			pdMap[j] = xdr.ScMapEntry{
				Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
				Val: pdVals[j],
			}
		}
		pp := &pdMap
		items[i] = xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pp}
	}
	vec := xdr.ScVec(items)
	pvec := &vec
	feedsSv := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pvec}

	outerKeys := []string{"updated_feeds", "updater"}
	outerVals := []xdr.ScVal{feedsSv, updaterAddrSv}
	outer := make(xdr.ScMap, len(outerKeys))
	for i, k := range outerKeys {
		sym := xdr.ScSymbol(k)
		outer[i] = xdr.ScMapEntry{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
			Val: outerVals[i],
		}
	}
	pouter := &outer
	body := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pouter}

	cid := contractID
	return xdr.ContractEvent{
		Type:       xdr.ContractEventTypeContract,
		ContractId: &cid,
		Body: xdr.ContractEventBody{
			V: 0,
			V0: &xdr.ContractEventV0{
				Topics: xdr.ScVec{topicRedstone},
				Data:   body,
			},
		},
	}
}

// accountStrkeyToAddressScVal encodes a G-strkey as an ScVal::Address
// via the strkey→AccountId→ScAddress path. Used for both the event's
// updater field and the corresponding InvokeContract arg.
func accountStrkeyToAddressScVal(t *testing.T, gStrkey string) xdr.ScVal {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteAccountID, gStrkey)
	if err != nil {
		t.Fatalf("strkey decode %q: %v", gStrkey, err)
	}
	var pub xdr.Uint256
	copy(pub[:], raw)
	aid := xdr.AccountId{
		Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
		Ed25519: &pub,
	}
	addr := xdr.ScAddress{
		Type:      xdr.ScAddressTypeScAddressTypeAccount,
		AccountId: &aid,
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
}

// bigIntToU256ScVal splits a non-negative *big.Int into four uint64
// words and wraps into ScVal::U256. Mirrors the redstone package-
// level helper so the integration test doesn't import from the
// _test.go file.
func bigIntToU256ScVal(t *testing.T, n *big.Int) xdr.ScVal {
	t.Helper()
	if n.Sign() < 0 {
		t.Fatalf("u256 does not accept negative: %s", n)
	}
	buf := n.Bytes()
	if len(buf) > 32 {
		t.Fatalf("value exceeds 256 bits: %s", n)
	}
	padded := make([]byte, 32)
	copy(padded[32-len(buf):], buf)
	w := func(b []byte) uint64 {
		var v uint64
		for _, x := range b {
			v = v<<8 | uint64(x)
		}
		return v
	}
	parts := xdr.UInt256Parts{
		HiHi: xdr.Uint64(w(padded[0:8])),
		HiLo: xdr.Uint64(w(padded[8:16])),
		LoHi: xdr.Uint64(w(padded[16:24])),
		LoLo: xdr.Uint64(w(padded[24:32])),
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvU256, U256: &parts}
}

// seedRedstoneLedger writes a single ledger containing one Soroban
// transaction that:
//
//  1. Invokes the RedStone Adapter's write_prices(updater, feed_ids,
//     payload) function — the op args the dispatcher plumbs through
//     to the decoder as events.Event.OpArgs.
//  2. Emits one REDSTONE contract event whose body matches the
//     write_prices call (same updater, same feed count).
//
// This is the paired envelope+meta shape a real adapter tx produces
// on mainnet. The dispatcher's hash-matching + OpArgs extraction are
// exactly what's being exercised here.
func seedRedstoneLedger(
	t *testing.T,
	ctx context.Context,
	dir string,
	seq uint32,
	closedAt time.Time,
	adapterContractID xdr.ContractId,
	updaterStrkey string,
	feedIDs []string,
	ev xdr.ContractEvent,
) {
	t.Helper()
	store, err := datastore.NewFilesystemDataStoreWithPath(dir)
	if err != nil {
		t.Fatalf("open filesystem datastore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := datastore.DataStoreConfig{
		Type:              "Filesystem",
		Params:            map[string]string{"destination_path": dir},
		Schema:            datastore.DataStoreSchema{LedgersPerFile: 1, FilesPerPartition: 1},
		NetworkPassphrase: testPassphrase,
		Compression:       "zstd",
	}
	if _, _, err := datastore.PublishConfig(ctx, store, cfg); err != nil {
		t.Fatalf("publish manifest: %v", err)
	}

	// ─── InvokeContract op (write_prices(updater, feed_ids, payload)) ──
	// ContractAddress inside the invoke-op targets the adapter.
	adapterSv := xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &adapterContractID,
	}
	updaterArg := accountStrkeyToAddressScVal(t, updaterStrkey)

	// feed_ids Vec<String>
	feedItems := make([]xdr.ScVal, len(feedIDs))
	for i, id := range feedIDs {
		s := xdr.ScString(id)
		feedItems[i] = xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &s}
	}
	feedVec := xdr.ScVec(feedItems)
	pFeedVec := &feedVec
	feedIDsArg := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pFeedVec}

	// payload Bytes — decoder ignores content, just needs a valid ScVal
	payloadBytes := xdr.ScBytes{0x01, 0x02, 0x03}
	payloadArg := xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &payloadBytes}

	invokeOp := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
				HostFunction: xdr.HostFunction{
					Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
					InvokeContract: &xdr.InvokeContractArgs{
						ContractAddress: adapterSv,
						FunctionName:    xdr.ScSymbol(redstone.WriteFnName),
						Args:            []xdr.ScVal{updaterArg, feedIDsArg, payloadArg},
					},
				},
			},
		},
	}

	// ─── Soroban envelope carrying the op ─────────────────────
	srcSeed := [32]byte{0x20, 0xDE, 0xAD, 0xBE, 0xEF}
	for i := 5; i < 32; i++ {
		srcSeed[i] = byte(i) + 1
	}
	srcMuxed, err := xdr.NewMuxedAccount(xdr.CryptoKeyTypeKeyTypeEd25519, xdr.Uint256(srcSeed))
	if err != nil {
		t.Fatalf("NewMuxedAccount: %v", err)
	}
	envelope := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: srcMuxed,
				Fee:           200,
				SeqNum:        1,
				Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
				Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
				Operations:    []xdr.Operation{invokeOp},
				Ext: xdr.TransactionExt{
					V:           1,
					SorobanData: &xdr.SorobanTransactionData{},
				},
			},
		},
	}
	hash, err := network.HashTransactionInEnvelope(envelope, testPassphrase)
	if err != nil {
		t.Fatalf("hash envelope: %v", err)
	}

	// ─── tx result: TxSuccess + one (stub) InvokeHostFunctionResult ──
	// The dispatcher's classic-op walk iterates operations paired
	// with op results. We include exactly one result matching the
	// envelope's one op — success with empty sub-value so no
	// op-decoder consumes it (we registered none).
	invokeRes := xdr.InvokeHostFunctionResult{
		Code: xdr.InvokeHostFunctionResultCodeInvokeHostFunctionSuccess,
		// Success arm: Hash of the return value. Zero hash is fine —
		// the dispatcher doesn't inspect it.
		Success: new(xdr.Hash),
	}
	opResults := []xdr.OperationResult{{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type:                     xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionResult: &invokeRes,
		},
	}}
	result := xdr.TransactionResultPair{
		TransactionHash: xdr.Hash(hash),
		Result: xdr.TransactionResult{
			FeeCharged: 200,
			Result: xdr.TransactionResultResult{
				Code:    xdr.TransactionResultCodeTxSuccess,
				Results: &opResults,
			},
		},
	}

	// ─── tx meta with our event ───────────────────────────────
	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				Events:      []xdr.ContractEvent{ev},
				ReturnValue: xdr.ScVal{Type: xdr.ScValTypeScvVoid},
			},
		},
	}

	// ─── assemble + publish the LCM ───────────────────────────
	txProc := xdr.TransactionResultMeta{
		Result:            result,
		FeeProcessing:     xdr.LedgerEntryChanges{},
		TxApplyProcessing: meta,
	}
	phase := xdr.TransactionPhase{
		V: 0,
		V0Components: &[]xdr.TxSetComponent{
			{
				Type: xdr.TxSetComponentTypeTxsetCompTxsMaybeDiscountedFee,
				TxsMaybeDiscountedFee: &xdr.TxSetComponentTxsMaybeDiscountedFee{
					Txs: []xdr.TransactionEnvelope{envelope},
				},
			},
		},
	}
	lcm := xdr.LedgerCloseMeta{
		V: 1,
		V1: &xdr.LedgerCloseMetaV1{
			LedgerHeader: xdr.LedgerHeaderHistoryEntry{
				Header: xdr.LedgerHeader{
					LedgerSeq: xdr.Uint32(seq),
					ScpValue:  xdr.StellarValue{CloseTime: xdr.TimePoint(closedAt.Unix())},
				},
			},
			TxSet: xdr.GeneralizedTransactionSet{
				V: 1,
				V1TxSet: &xdr.TransactionSetV1{
					Phases: []xdr.TransactionPhase{phase},
				},
			},
			TxProcessing: []xdr.TransactionResultMeta{txProc},
		},
	}

	batch := xdr.LedgerCloseMetaBatch{
		StartSequence:    xdr.Uint32(seq),
		EndSequence:      xdr.Uint32(seq),
		LedgerCloseMetas: []xdr.LedgerCloseMeta{lcm},
	}
	encoder := compressxdr.NewXDREncoder(compressxdr.DefaultCompressor, batch)
	var buf bytes.Buffer
	if _, err := encoder.WriteTo(&buf); err != nil {
		t.Fatalf("encode batch seq=%d: %v", seq, err)
	}
	key := cfg.Schema.GetObjectKeyFromSequenceNumber(seq)
	if err := store.PutFile(ctx, key, byteSliceWriterTo(buf.Bytes()), nil); err != nil {
		t.Fatalf("put seq=%d: %v", seq, err)
	}
}
