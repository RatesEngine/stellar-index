package external

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
)

// errPoller is a Poller that returns an error from PollOnce. Used
// to verify runPoller's transient-error path: the loop must log +
// continue rather than abort, since poll-based sources hit
// rate-limits and network blips routinely.
type errPoller struct {
	name     string
	interval time.Duration
	err      error
	calls    int
}

func (p *errPoller) Name() string                { return p.name }
func (p *errPoller) Class() Class                { return ClassExchange }
func (p *errPoller) PollInterval() time.Duration { return p.interval }
func (p *errPoller) PollOnce(ctx context.Context, pairs []canonical.Pair) ([]canonical.Trade, []canonical.OracleUpdate, error) {
	p.calls++
	return nil, nil, p.err
}

// runPoller covers two output emission branches: updates (already
// pinned by TestRun_PollerFiresImmediatelyAndEmitsUpdates) and
// trades. This test exercises the trades branch.

func TestRun_PollerEmitsTrades(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p := &mockPoller{
		name:     "trade-poller",
		interval: 50 * time.Millisecond,
		trades:   []canonical.Trade{testTrade(t, "trade-poller", 1)},
	}
	sink := make(chan consumer.Event, 8)
	wait, err := Run(ctx, nil, []PollerSpec{{Poller: p, Pairs: []canonical.Pair{newTestPair(t)}}}, sink, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	select {
	case ev := <-sink:
		te, ok := ev.(TradeEvent)
		if !ok {
			t.Errorf("got %T want TradeEvent", ev)
		}
		if te.Trade.Source != "trade-poller" {
			t.Errorf("trade.Source = %q want trade-poller", te.Trade.Source)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected trade event, none received")
	}

	cancel()
	wait()
}

// runPoller's PollOnce-returns-error path must log + continue rather
// than abort the goroutine — transient errors are the norm for
// REST/HTTP pollers. Verify the function still survives long enough
// to retry on the next tick (calls ≥ 2).

func TestRun_PollerContinuesAfterError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	p := &errPoller{
		name:     "broken-poller",
		interval: 50 * time.Millisecond,
		err:      errors.New("upstream 503"),
	}
	sink := make(chan consumer.Event, 1)
	wait, err := Run(ctx, nil, []PollerSpec{{Poller: p, Pairs: []canonical.Pair{newTestPair(t)}}}, sink, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Let several intervals elapse, then cancel.
	time.Sleep(250 * time.Millisecond)
	cancel()
	wait()

	if p.calls < 2 {
		t.Errorf("calls = %d, want ≥2 (poller didn't retry after error)", p.calls)
	}
}
