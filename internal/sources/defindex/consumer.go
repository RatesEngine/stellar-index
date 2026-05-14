package defindex

import (
	"context"
	"log/slog"

	"github.com/RatesEngine/rates-engine/internal/consumer"
)

// Sink consumes VaultFlow events and persists them. For Phase A
// (this PR) the sink is INFO-logging only — operators verify the
// dispatcher routes vault events correctly via the journal, then
// Phase B (separate PR) wires:
//
//   - trades.routed_via tagging on same-tx Blend / Soroswap legs.
//   - aggregator_exposures rows from the periodic vault-state
//     ticker.
//
// Why log-only first: the routed_via attribution path needs the
// router-attribution observer (a cross-cutting tx-batch hook,
// shared with the soroswap-router source) which doesn't exist
// yet. Better to ship the decoder + verify wire-shape on r1 with
// real traffic before tying the persist contract to a particular
// observer design.
type Sink struct {
	Logger *slog.Logger
}

// Persist implements consumer.Sink. Logs each vault flow at INFO
// level with the call shape: vault, direction, depositor, amounts,
// share-token delta. The pipeline's PersistEvents loop calls this
// once per dispatched Event.
func (s *Sink) Persist(_ context.Context, ev consumer.Event) error {
	ve, ok := ev.(Event)
	if !ok {
		return nil // not ours; pipeline already filtered but be defensive
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	// Stringify amounts for the log line. Multi-asset vaults
	// produce multi-element slices; the trio in scope today is
	// single-asset (len == 1) so the slice rendering stays
	// compact.
	amounts := make([]string, len(ve.Flow.Amounts))
	for i, a := range ve.Flow.Amounts {
		amounts[i] = a.String()
	}
	logger.Info("defindex vault flow",
		"source", SourceName,
		"tx_hash", ve.Flow.TxHash,
		"ledger", ve.Flow.Ledger,
		"vault", ve.Flow.VaultName,
		"contract_id", ve.Flow.ContractID,
		"direction", string(ve.Flow.Direction),
		"counterparty", ve.Flow.Counterparty,
		"amounts", amounts,
		"df_token_delta", ve.Flow.DfTokenDelta.String(),
	)
	return nil
}
