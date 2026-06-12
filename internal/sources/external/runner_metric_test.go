package external

import (
	"context"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
)

// Pin the metric labels emitted by runPoller so a refactor can't
// silently change "skipped" → "success" (which would defeat the
// staleness alert) or fail to increment "error" (which would re-open
// the original blind-spot bug).

func TestRunPoller_MetricLabels(t *testing.T) {
	cases := []struct {
		name        string
		scripted    scriptedReturn
		wantOutcome string
	}{
		{
			name: "success",
			scripted: scriptedReturn{
				updates: []canonical.OracleUpdate{testOracleUpdate(t, "metric-poller")},
			},
			wantOutcome: "success",
		},
		{
			name:        "error",
			scripted:    scriptedReturn{err: errMock("boom")},
			wantOutcome: "error",
		},
		{
			// Cooldown convention: (nil, nil, nil). Must NOT count as
			// success — alerting on absence of `outcome="success"` would
			// silently mask the very condition the metric is for.
			name:        "skipped_during_cooldown",
			scripted:    scriptedReturn{},
			wantOutcome: "skipped",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := "metric-test-" + tc.name
			before := readPollsCounter(t, source, tc.wantOutcome)

			p := &scriptedPoller{
				name:     source,
				interval: 50 * time.Millisecond,
				returns:  []scriptedReturn{tc.scripted},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			sink := make(chan consumer.Event, 4)
			wait, err := Run(ctx, nil, []PollerSpec{{Poller: p, Pairs: []canonical.Pair{newTestPair(t)}}}, sink, nil)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			// Drain whatever the poll emits so the writer goroutine
			// doesn't block on the sink during shutdown.
			go func() {
				for range sink {
				}
			}()
			// Give the immediate-startup poll time to fire.
			time.Sleep(150 * time.Millisecond)
			cancel()
			wait()

			after := readPollsCounter(t, source, tc.wantOutcome)
			if after <= before {
				t.Errorf("ExternalPollerPollsTotal{source=%q,outcome=%q}: before=%v after=%v (expected increment)",
					source, tc.wantOutcome, before, after)
			}
		})
	}
}

// scriptedReturn is one PollOnce return value; scriptedPoller cycles
// through a slice of these and repeats the last forever.
type scriptedReturn struct {
	trades  []canonical.Trade
	updates []canonical.OracleUpdate
	err     error
}

// scriptedPoller returns a sequence of (trades, updates, err) and
// repeats the last entry forever once exhausted.
type scriptedPoller struct {
	name     string
	interval time.Duration
	returns  []scriptedReturn
	idx      int
}

func (s *scriptedPoller) Name() string                { return s.name }
func (s *scriptedPoller) Class() Class                { return ClassExchange }
func (s *scriptedPoller) PollInterval() time.Duration { return s.interval }
func (s *scriptedPoller) PollOnce(_ context.Context, _ []canonical.Pair) ([]canonical.Trade, []canonical.OracleUpdate, error) {
	r := s.returns[s.idx]
	if s.idx < len(s.returns)-1 {
		s.idx++
	}
	return r.trades, r.updates, r.err
}

type errMock string

func (e errMock) Error() string { return string(e) }

// readPollsCounter peeks the current value of
// ExternalPollerPollsTotal{source, outcome}. Reset semantics:
// prometheus counters can't be reset between subtests in the same
// process, so each subtest uses a unique source label and asserts
// strict increment rather than exact value.
func readPollsCounter(t *testing.T, source, outcome string) float64 {
	t.Helper()
	m, err := obs.ExternalPollerPollsTotal.GetMetricWithLabelValues(source, outcome)
	if err != nil {
		t.Fatalf("get metric: %v", err)
	}
	var pb dto.Metric
	if err := m.Write(&pb); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	return pb.GetCounter().GetValue()
}
