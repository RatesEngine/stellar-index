package clickhouse

import (
	"time"

	"github.com/stellar/go-stellar-sdk/ingest"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// extractEntryChanges populates ext.Changes with one row per LedgerEntryChange
// in a transaction's meta — the substrate the account-state explorer (ADR-0038
// Phase C) re-derives current balances/trustlines/offers/contract-data from,
// and the ADR-0034 "re-derive the LedgerEntry supply observers from the lake"
// promise. Closes the G12-03 known gap in ExtractLedger.
//
// Mirrors dispatcher.walkEntryChanges EXACTLY so the lake's rows match what the
// live LedgerEntryChangeDecoder hook sees: fee-meta + TxChangesBefore/After at
// op_index -1, per-operation changes at their op_index. change_index is a
// monotonic per-transaction counter (stable across re-ingest → idempotent under
// the ReplacingMergeTree). Resilient: a change that won't marshal is skipped,
// never fatal.
func extractEntryChanges(ext *LedgerExtract, tx ingest.LedgerTransaction, seq uint32, closeTime time.Time, txHash string) {
	var changeIdx uint32
	emit := func(opIndex int, c xdr.LedgerEntryChange) {
		row, ok := entryChangeRow(seq, closeTime, txHash, int32(opIndex), changeIdx, c)
		if !ok {
			return
		}
		ext.Changes = append(ext.Changes, row)
		changeIdx++
	}

	// Tx-level fee changes (every successful tx debits a fee) — op_index -1.
	for i := range tx.FeeChanges {
		emit(-1, tx.FeeChanges[i])
	}
	switch tx.UnsafeMeta.V {
	case 3:
		v3 := tx.UnsafeMeta.MustV3()
		emitChangeSet(v3.TxChangesBefore, -1, emit)
		for opIdx := range v3.Operations {
			emitChangeSet(v3.Operations[opIdx].Changes, opIdx, emit)
		}
		emitChangeSet(v3.TxChangesAfter, -1, emit)
	case 4:
		v4 := tx.UnsafeMeta.MustV4()
		emitChangeSet(v4.TxChangesBefore, -1, emit)
		for opIdx := range v4.Operations {
			emitChangeSet(v4.Operations[opIdx].Changes, opIdx, emit)
		}
		emitChangeSet(v4.TxChangesAfter, -1, emit)
	}
}

func emitChangeSet(changes []xdr.LedgerEntryChange, opIdx int, emit func(int, xdr.LedgerEntryChange)) {
	for i := range changes {
		emit(opIdx, changes[i])
	}
}

// entryChangeRow builds one LedgerEntryChangeRow from an xdr.LedgerEntryChange.
// ok=false when the change can't be marshalled (skip + tolerate). For
// created/updated/state the key is derived from the entry and the entry XDR is
// retained; for removed only the key is present.
func entryChangeRow(seq uint32, closeTime time.Time, txHash string, opIndex int32, changeIdx uint32, c xdr.LedgerEntryChange) (LedgerEntryChangeRow, bool) {
	row := LedgerEntryChangeRow{
		LedgerSeq:   seq,
		CloseTime:   closeTime,
		TxHash:      txHash,
		OpIndex:     opIndex,
		ChangeIndex: changeIdx,
		ChangeType:  changeTypeName(c.Type),
	}

	var key xdr.LedgerKey
	switch c.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated, xdr.LedgerEntryChangeTypeLedgerEntryUpdated, xdr.LedgerEntryChangeTypeLedgerEntryState:
		entry, ok := ledgerEntryOf(c)
		if !ok {
			return LedgerEntryChangeRow{}, false
		}
		k, err := entry.LedgerKey()
		if err != nil {
			return LedgerEntryChangeRow{}, false
		}
		key = k
		row.EntryType = entryTypeName(entry.Data.Type)
		entryB64, err := xdr.MarshalBase64(entry)
		if err != nil {
			return LedgerEntryChangeRow{}, false
		}
		row.EntryXDR = entryB64
	case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
		k, ok := c.GetRemoved()
		if !ok {
			return LedgerEntryChangeRow{}, false
		}
		key = k
		row.EntryType = entryTypeName(k.Type)
	default:
		return LedgerEntryChangeRow{}, false
	}

	keyB64, err := xdr.MarshalBase64(key)
	if err != nil {
		return LedgerEntryChangeRow{}, false
	}
	row.KeyXDR = keyB64
	return row, true
}

// ledgerEntryOf returns the LedgerEntry for a created/updated/state change.
func ledgerEntryOf(c xdr.LedgerEntryChange) (xdr.LedgerEntry, bool) {
	switch c.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		return c.GetCreated()
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		return c.GetUpdated()
	case xdr.LedgerEntryChangeTypeLedgerEntryState:
		return c.GetState()
	default:
		return xdr.LedgerEntry{}, false
	}
}

// changeTypeName maps the XDR change-type enum to a stable lake value.
func changeTypeName(t xdr.LedgerEntryChangeType) string {
	switch t {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		return "created"
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		return "updated"
	case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
		return "removed"
	case xdr.LedgerEntryChangeTypeLedgerEntryState:
		return "state"
	default:
		return "unknown"
	}
}

// entryTypeName maps the XDR ledger-entry-type enum to a stable lake value.
func entryTypeName(t xdr.LedgerEntryType) string {
	switch t {
	case xdr.LedgerEntryTypeAccount:
		return "account"
	case xdr.LedgerEntryTypeTrustline:
		return "trustline"
	case xdr.LedgerEntryTypeOffer:
		return "offer"
	case xdr.LedgerEntryTypeData:
		return "data"
	case xdr.LedgerEntryTypeClaimableBalance:
		return "claimable_balance"
	case xdr.LedgerEntryTypeLiquidityPool:
		return "liquidity_pool"
	case xdr.LedgerEntryTypeContractData:
		return "contract_data"
	case xdr.LedgerEntryTypeContractCode:
		return "contract_code"
	case xdr.LedgerEntryTypeTtl:
		return "ttl"
	case xdr.LedgerEntryTypeConfigSetting:
		return "config_setting"
	default:
		return "unknown"
	}
}
