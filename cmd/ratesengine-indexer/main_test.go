package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
)

func TestProcessAndPersistCursor_ReturnsDispatcherErrorBeforeCursorWrite(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	disp := dispatcher.New()
	events := make(chan consumer.Event)
	defer close(events)

	err := processAndPersistCursor(
		context.Background(),
		disp,
		events,
		nil,
		logger,
		invalidLedgerCloseMeta(42),
		"not-a-real-network-passphrase",
	)
	if err == nil {
		t.Fatal("expected dispatcher/build-reader error for invalid ledger meta")
	}
}

func TestRecordCursorMetric_UsesSingleLabelCardinality(t *testing.T) {
	t.Parallel()

	// The live cursor gauge is declared with one `source` label.
	// This helper exists to keep the write path pinned to that
	// cardinality and avoid reintroducing the prior panic-class
	// mismatch.
	recordCursorMetric(123)
}

func TestEmitDiscoveryDropMetricDelta_AddsOnlyNewDrops(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	before := testutil.ToFloat64(obs.DiscoveryDroppedHitsTotal)

	last := emitDiscoveryDropMetricDelta(2, 5, logger)
	if last != 5 {
		t.Fatalf("last = %d, want 5", last)
	}
	mid := testutil.ToFloat64(obs.DiscoveryDroppedHitsTotal)
	if got := mid - before; got != 3 {
		t.Fatalf("counter delta after first emit = %v, want 3", got)
	}

	last = emitDiscoveryDropMetricDelta(last, 5, logger)
	if last != 5 {
		t.Fatalf("last after no-op emit = %d, want 5", last)
	}
	after := testutil.ToFloat64(obs.DiscoveryDroppedHitsTotal)
	if got := after - mid; got != 0 {
		t.Fatalf("counter delta after second emit = %v, want 0", got)
	}
}

func invalidLedgerCloseMeta(seq uint32) sdkxdr.LedgerCloseMeta {
	component := sdkxdr.TxSetComponent{
		Type: sdkxdr.TxSetComponentTypeTxsetCompTxsMaybeDiscountedFee,
		TxsMaybeDiscountedFee: &sdkxdr.TxSetComponentTxsMaybeDiscountedFee{
			Txs: []sdkxdr.TransactionEnvelope{{}},
		},
	}
	components := []sdkxdr.TxSetComponent{component}
	return sdkxdr.LedgerCloseMeta{
		V: 1,
		V1: &sdkxdr.LedgerCloseMetaV1{
			LedgerHeader: sdkxdr.LedgerHeaderHistoryEntry{
				Header: sdkxdr.LedgerHeader{
					LedgerSeq: sdkxdr.Uint32(seq),
				},
			},
			TxSet: sdkxdr.GeneralizedTransactionSet{
				V: 1,
				V1TxSet: &sdkxdr.TransactionSetV1{
					Phases: []sdkxdr.TransactionPhase{{
						V:            0,
						V0Components: &components,
					}},
				},
			},
		},
	}
}
