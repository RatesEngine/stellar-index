package orchestrator

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// captureSink records every snapshot the orchestrator hands to it.
// Used for the per-call-site contract tests below.
type captureSink struct {
	got []RewrittenSnapshot
	err error
}

func (s *captureSink) InsertRewrittenVWAPSnapshot(_ context.Context, snap RewrittenSnapshot) error {
	s.got = append(s.got, snap)
	return s.err
}

func mustPair(t *testing.T, base, quote string) canonical.Pair {
	t.Helper()
	b, err := canonical.ParseAsset(base)
	if err != nil {
		t.Fatalf("ParseAsset(%q): %v", base, err)
	}
	q, err := canonical.ParseAsset(quote)
	if err != nil {
		t.Fatalf("ParseAsset(%q): %v", quote, err)
	}
	p, err := canonical.NewPair(b, q)
	if err != nil {
		t.Fatalf("NewPair(%s, %s): %v", base, quote, err)
	}
	return p
}

func mustAmount(t *testing.T, raw string) canonical.Amount {
	t.Helper()
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		t.Fatalf("big.Int.SetString(%q) failed", raw)
	}
	return canonical.NewAmount(v)
}

func tradeOn(t *testing.T, source string, base, quote canonical.Amount) canonical.Trade {
	t.Helper()
	pair := mustPair(t, "native", "fiat:USD")
	return canonical.Trade{
		Source:      source,
		Ledger:      62000000,
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
		OpIndex:     0,
		Timestamp:   time.Unix(1745000000, 0).UTC(),
		Pair:        pair,
		BaseAmount:  base,
		QuoteAmount: quote,
	}
}

// TestPublishRewrittenSnapshot_ForwardsToSink — the happy path:
// fiat-quoted target + sink wired → snapshot lands. Volume base
// + USD aggregate from the trades; sources de-duplicated.
func TestPublishRewrittenSnapshot_ForwardsToSink(t *testing.T) {
	sink := &captureSink{}
	o := &Orchestrator{
		cfg: Config{RewrittenSnapshotSink: sink},
	}
	o.logger = silentLogger()
	pair := mustPair(t, "native", "fiat:USD")
	trades := []canonical.Trade{
		tradeOn(t, "soroswap", mustAmount(t, "150000000"), mustAmount(t, "23636164")),
		tradeOn(t, "sdex", mustAmount(t, "1000000"), mustAmount(t, "157000")),
		tradeOn(t, "soroswap", mustAmount(t, "500000"), mustAmount(t, "78500")),
	}
	now := time.Now().UTC()

	o.publishRewrittenSnapshot(context.Background(), pair, 5*time.Minute, "0.157352774926", trades, now)

	if len(sink.got) != 1 {
		t.Fatalf("sink.got len=%d, want 1", len(sink.got))
	}
	snap := sink.got[0]
	if snap.WindowSeconds != 300 {
		t.Errorf("WindowSeconds = %d, want 300", snap.WindowSeconds)
	}
	if snap.VWAP != "0.157352774926" {
		t.Errorf("VWAP = %q, want 0.157352774926", snap.VWAP)
	}
	if snap.TradeCount != 3 {
		t.Errorf("TradeCount = %d, want 3", snap.TradeCount)
	}
	// Volume: 150_000_000 + 1_000_000 + 500_000 = 151_500_000
	if snap.Volume != "151500000" {
		t.Errorf("Volume = %q, want 151500000", snap.Volume)
	}
	// USD: (23_636_164 + 157_000 + 78_500) / 1e8 = 23_871_664 / 1e8 = 0.23871664
	if snap.VolumeUSD != "0.23871664" {
		t.Errorf("VolumeUSD = %q, want 0.23871664", snap.VolumeUSD)
	}
	// Sources de-duplicated: [soroswap, sdex] in some order.
	if len(snap.Sources) != 2 {
		t.Errorf("Sources len = %d, want 2 (soroswap and sdex de-duplicated)", len(snap.Sources))
	}
}

// TestPublishRewrittenSnapshot_NoSinkIsNoop — without a sink wired
// the orchestrator skips snapshot work entirely. The Redis write
// upstream is unaffected; this test pins the no-op path.
func TestPublishRewrittenSnapshot_NoSinkIsNoop(t *testing.T) {
	o := &Orchestrator{cfg: Config{}}
	o.logger = silentLogger()
	pair := mustPair(t, "native", "fiat:USD")
	// No panic, no crash; should silently return.
	o.publishRewrittenSnapshot(context.Background(), pair, 5*time.Minute, "0.157", nil, time.Now())
}

// TestPublishRewrittenSnapshot_NonFiatTargetIsNoop — only fiat-quoted
// targets are part of the stablecoin-fiat-proxy expansion; other
// targets shouldn't land in the rewritten table at all (storing them
// would mislead consumers that the table holds rewritten data).
func TestPublishRewrittenSnapshot_NonFiatTargetIsNoop(t *testing.T) {
	sink := &captureSink{}
	o := &Orchestrator{cfg: Config{RewrittenSnapshotSink: sink}}
	o.logger = silentLogger()
	cryptoCryptoPair := mustPair(t, "native", "crypto:BTC")
	o.publishRewrittenSnapshot(context.Background(), cryptoCryptoPair, 5*time.Minute, "0.000003", nil, time.Now())
	if len(sink.got) != 0 {
		t.Errorf("sink received %d snapshots for non-fiat target, want 0", len(sink.got))
	}
}

// TestPublishRewrittenSnapshot_SinkErrorDoesNotPropagate — a sink
// failure must not affect the orchestrator's tick. The metric
// counter records the failure; the function returns silently.
func TestPublishRewrittenSnapshot_SinkErrorDoesNotPropagate(t *testing.T) {
	sink := &captureSink{err: errors.New("postgres unreachable")}
	o := &Orchestrator{cfg: Config{RewrittenSnapshotSink: sink}}
	o.logger = silentLogger()
	pair := mustPair(t, "native", "fiat:USD")
	// No assertion on a return value (the function returns nothing);
	// this just must not panic.
	o.publishRewrittenSnapshot(context.Background(), pair, 5*time.Minute, "0.157", nil, time.Now())
	if len(sink.got) != 1 {
		t.Errorf("sink should still have been called once even on error, got %d calls", len(sink.got))
	}
}

// TestPublishRewrittenSnapshot_NonUSDFiatOmitsUSDVolume — a target
// like XLM/EUR is fiat-quoted but the uniform-1e8 USD-volume
// convention only applies to USD, so the USD field stays empty.
// Base volume + sources still populate.
func TestPublishRewrittenSnapshot_NonUSDFiatOmitsUSDVolume(t *testing.T) {
	sink := &captureSink{}
	o := &Orchestrator{cfg: Config{RewrittenSnapshotSink: sink}}
	o.logger = silentLogger()
	xlm, _ := canonical.ParseAsset("native")
	eur, _ := canonical.ParseAsset("fiat:EUR")
	xlmEUR, _ := canonical.NewPair(xlm, eur)
	trades := []canonical.Trade{
		tradeOn(t, "ecb", mustAmount(t, "100000"), mustAmount(t, "150000")),
	}
	o.publishRewrittenSnapshot(context.Background(), xlmEUR, 5*time.Minute, "1.5", trades, time.Now())
	if len(sink.got) != 1 {
		t.Fatalf("sink.got len=%d, want 1", len(sink.got))
	}
	snap := sink.got[0]
	if snap.Volume != "100000" {
		t.Errorf("Volume = %q, want 100000", snap.Volume)
	}
	if snap.VolumeUSD != "" {
		t.Errorf("VolumeUSD = %q, want empty (uniform-1e8 USD convention only applies to fiat:USD)", snap.VolumeUSD)
	}
}
