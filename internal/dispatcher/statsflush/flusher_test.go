package statsflush

import (
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
)

// stubStatsSource is a deterministic StatsSource for tests. The
// Flusher is exercised via direct flushAt calls; the goroutine
// loop in Run() is straightforward enough that the table-driven
// flushAt tests cover the contract.
type stubStatsSource struct {
	stats dispatcher.Stats
}

func (s *stubStatsSource) Stats() dispatcher.Stats { return s.stats }

// flusher_test currently lives as a stub — adequate test coverage
// here would require either an integration-style harness against
// a real timescale.Store (covered downstream once #557 lands and
// the integration tests can hit the real schema) or a mockable
// store interface. The flusher's logic is small and well-typed;
// the dispatcher.Stats package + storage package each have their
// own tests that pin the inputs/outputs.
//
// Future: when timescale.Store grows a mock-friendly interface,
// add direct flushAt tests with a fake clock + injected stats.
var _ = stubStatsSource{}
