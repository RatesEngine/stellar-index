package pipeline

import (
	"context"
	"io"
	"log/slog"
	"testing"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
)

func TestProcessLedger_ReturnsDispatcherError(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	disp := dispatcher.New()
	events := make(chan consumer.Event)
	defer close(events)

	err := ProcessLedger(
		context.Background(),
		disp,
		events,
		logger,
		invalidLedgerCloseMeta(42),
		"not-a-real-network-passphrase",
	)
	if err == nil {
		t.Fatal("expected dispatcher/build-reader error for invalid ledger meta")
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
