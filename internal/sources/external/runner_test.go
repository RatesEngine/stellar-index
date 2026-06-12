package external

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
)

// mockStreamer drives a hand-controlled channel for test purposes.
type mockStreamer struct {
	name    string
	trades  []canonical.Trade
	startFn func(context.Context, []canonical.Pair) (<-chan canonical.Trade, error)
}

func (m *mockStreamer) Name() string { return m.name }
func (m *mockStreamer) Class() Class { return ClassExchange }
func (m *mockStreamer) Start(ctx context.Context, pairs []canonical.Pair) (<-chan canonical.Trade, error) {
	if m.startFn != nil {
		return m.startFn(ctx, pairs)
	}
	out := make(chan canonical.Trade, len(m.trades))
	for _, t := range m.trades {
		out <- t
	}
	go func() {
		<-ctx.Done()
		close(out)
	}()
	return out, nil
}

// newTestPair builds a simple XLM/USDT canonical.Pair.
func newTestPair(t *testing.T) canonical.Pair {
	t.Helper()
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	p, err := canonical.NewPair(xlm, usdt)
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}
	return p
}

// testTrade builds a canonical.Trade with minimum valid fields.
func testTrade(t *testing.T, source string, ledger uint32) canonical.Trade {
	t.Helper()
	return canonical.Trade{
		Source:      source,
		Ledger:      ledger,
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000000",
		OpIndex:     0,
		Timestamp:   time.Unix(1_745_000_000, 0).UTC(),
		Pair:        newTestPair(t),
		BaseAmount:  canonical.NewAmount(big.NewInt(100000000)),
		QuoteAmount: canonical.NewAmount(big.NewInt(17500000)),
	}
}

func TestRun_NoStreamers_ReturnsUsableWait(t *testing.T) {
	ctx := context.Background()
	wait, err := Run(ctx, nil, nil, make(chan consumer.Event, 1), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if wait == nil {
		t.Fatal("Run returned nil wait func")
	}
	// Calling wait() on an empty runner is a no-op — must return
	// promptly.
	wait()
}

func TestRun_ForwardsTradesAndWrapsAsEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	trades := []canonical.Trade{
		testTrade(t, "mock-exchange-1", 1),
		testTrade(t, "mock-exchange-1", 2),
	}
	m := &mockStreamer{name: "mock-exchange-1", trades: trades}

	sink := make(chan consumer.Event, 4)
	wait, err := Run(ctx, []StreamerSpec{{Streamer: m, Pairs: []canonical.Pair{newTestPair(t)}}}, nil, sink, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Drain.
	got := make([]consumer.Event, 0, 2)
	for len(got) < 2 {
		select {
		case e := <-sink:
			got = append(got, e)
		case <-ctx.Done():
			t.Fatalf("timeout waiting for events; got %d", len(got))
		}
	}

	cancel()
	wait()

	// Each event must be a TradeEvent wrapping the original Trade.
	for i, e := range got {
		te, ok := e.(TradeEvent)
		if !ok {
			t.Errorf("got[%d] is %T, want TradeEvent", i, e)
			continue
		}
		if te.Source() != "mock-exchange-1" {
			t.Errorf("got[%d].Source() = %q want mock-exchange-1", i, te.Source())
		}
		if te.EventKind() != "external.trade" {
			t.Errorf("got[%d].EventKind() = %q", i, te.EventKind())
		}
	}
}

func TestRun_FatalStartError_SurfacesSynchronously(t *testing.T) {
	// A streamer whose Start returns an error must cause Run to
	// return a non-nil error without spawning the goroutine.
	m := &mockStreamer{
		name: "mock-failing",
		startFn: func(ctx context.Context, pairs []canonical.Pair) (<-chan canonical.Trade, error) {
			return nil, errors.New("bad config")
		},
	}
	ctx := context.Background()
	_, err := Run(ctx, []StreamerSpec{{Streamer: m, Pairs: []canonical.Pair{newTestPair(t)}}}, nil, make(chan consumer.Event, 1), nil)
	if err == nil {
		t.Fatal("expected Run to return an error; got nil")
	}
}

// mockPoller satisfies the Poller interface with scripted outputs.
type mockPoller struct {
	name     string
	interval time.Duration
	trades   []canonical.Trade
	updates  []canonical.OracleUpdate
	calls    int
}

func (m *mockPoller) Name() string                { return m.name }
func (m *mockPoller) Class() Class                { return ClassExchange }
func (m *mockPoller) PollInterval() time.Duration { return m.interval }
func (m *mockPoller) PollOnce(ctx context.Context, pairs []canonical.Pair) ([]canonical.Trade, []canonical.OracleUpdate, error) {
	m.calls++
	return m.trades, m.updates, nil
}

func testOracleUpdate(t *testing.T, source string) canonical.OracleUpdate {
	t.Helper()
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	return canonical.OracleUpdate{
		Source:    source,
		Ledger:    0,
		TxHash:    "0000000000000000000000000000000000000000000000000000000000000000",
		OpIndex:   0,
		Timestamp: time.Unix(1_745_000_000, 0).UTC(),
		Asset:     xlm,
		Quote:     usd,
		Price:     canonical.NewAmount(big.NewInt(17500000)),
		Decimals:  8,
	}
}

func TestRun_PollerFiresImmediatelyAndEmitsUpdates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p := &mockPoller{
		name:     "mock-poller",
		interval: 50 * time.Millisecond,
		updates: []canonical.OracleUpdate{
			testOracleUpdate(t, "mock-poller"),
		},
	}

	sink := make(chan consumer.Event, 8)
	wait, err := Run(ctx, nil, []PollerSpec{{Poller: p, Pairs: []canonical.Pair{newTestPair(t)}}}, sink, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// First event arrives from the immediate startup poll, not
	// after the interval elapses.
	select {
	case ev := <-sink:
		ue, ok := ev.(UpdateEvent)
		if !ok {
			t.Errorf("got %T want UpdateEvent", ev)
		}
		if ue.Source() != "mock-poller" {
			t.Errorf("source = %q want mock-poller", ue.Source())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected immediate poll; no event received")
	}

	cancel()
	wait()

	if p.calls < 1 {
		t.Errorf("PollOnce called %d times, want ≥1", p.calls)
	}
}

func TestRun_RejectsNonPositivePollInterval(t *testing.T) {
	p := &mockPoller{
		name:     "bad-poller",
		interval: 0,
	}
	_, err := Run(context.Background(), nil, []PollerSpec{{Poller: p, Pairs: []canonical.Pair{newTestPair(t)}}}, make(chan consumer.Event, 1), nil)
	if err == nil {
		t.Error("expected error for non-positive PollInterval")
	}
}

func TestRun_CtxCancelClosesForwarders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &mockStreamer{name: "mock-exchange-2"} // no trades — will hang until ctx done

	wait, err := Run(ctx, []StreamerSpec{{Streamer: m, Pairs: []canonical.Pair{newTestPair(t)}}}, nil, make(chan consumer.Event, 1), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Cancel and confirm forwarder exits.
	cancel()

	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run's wait() did not complete after ctx cancel")
	}
}
