package soroswap_router

import (
	"context"
	"log/slog"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
)

// Sink consumes RouterSwap events and persists them. For Phase A
// (this PR) the sink is INFO-logging only — operators can verify
// the dispatcher routes router calls correctly via the journal,
// then Phase B (separate PR) wires the actual storage write +
// trades.routed_via tagger.
//
// Why log-only first: the storage shape needs an ADR-confirmed
// schema (one new table for router intents? extend trades?
// separate hypertable with FK to trades?). Better to defer that
// design decision until we have a corpus of real router events
// to size against, rather than pick a schema and migrate later.
type Sink struct {
	Logger *slog.Logger
}

// Persist implements consumer.Sink. Logs each routed swap at INFO
// level with the call shape: tx_hash, function, path, amounts.
// The pipeline's PersistEvents loop calls this once per dispatched
// Event.
func (s *Sink) Persist(ctx context.Context, ev consumer.Event) error {
	re, ok := ev.(Event)
	if !ok {
		return nil // not ours; pipeline already filtered but be defensive
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("soroswap-router swap routed",
		"source", SourceName,
		"tx_hash", re.Swap.TxHash,
		"ledger", re.Swap.Ledger,
		"function", re.Swap.Function,
		"path_len", len(re.Swap.Path),
		"recipient", re.Swap.Recipient,
		"amount_in", re.Swap.AmountIn.String(),
		"amount_out", re.Swap.AmountOut.String(),
	)
	return nil
}
