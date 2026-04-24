package dispatcher

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

const testPassphrase = "Test SDF Network ; September 2015"

// emptyLedgerCloseMeta builds a minimal LedgerCloseMetaV1 with zero
// transactions. Sufficient for exercising the reader construction
// path without going through full tx+event fixture assembly. For
// event-routing coverage, the dispatchOne unit tests use synthetic
// events.Event directly.
func emptyLedgerCloseMeta(t *testing.T, seq uint32) xdr.LedgerCloseMeta {
	t.Helper()
	return xdr.LedgerCloseMeta{
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
}
