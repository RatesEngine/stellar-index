package clickhouse

import (
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"
)

const ecTestG = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

func TestEntryChangeRow_CreatedAccount(t *testing.T) {
	entry := xdr.LedgerEntry{
		LastModifiedLedgerSeq: 100,
		Data: xdr.LedgerEntryData{
			Type: xdr.LedgerEntryTypeAccount,
			Account: &xdr.AccountEntry{
				AccountId: xdr.MustAddress(ecTestG),
				Balance:   1000,
			},
		},
	}
	c := xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryCreated, Created: &entry}

	row, ok := entryChangeRow(100, time.Unix(0, 0).UTC(), "txabc", 0, 0, c)
	if !ok {
		t.Fatal("entryChangeRow returned ok=false for a valid created-account change")
	}
	if row.ChangeType != "created" || row.EntryType != "account" {
		t.Errorf("change/entry type = %q/%q, want created/account", row.ChangeType, row.EntryType)
	}
	if row.KeyXDR == "" || row.EntryXDR == "" {
		t.Errorf("key/entry XDR empty: key=%q entry=%q", row.KeyXDR, row.EntryXDR)
	}
	if row.OpIndex != 0 || row.TxHash != "txabc" {
		t.Errorf("row identity = %+v", row)
	}
}

func TestEntryChangeRow_RemovedTrustline(t *testing.T) {
	key := xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeTrustline,
		TrustLine: &xdr.LedgerKeyTrustLine{
			AccountId: xdr.MustAddress(ecTestG),
			Asset:     xdr.MustNewCreditAsset("USDC", ecTestG).ToTrustLineAsset(),
		},
	}
	c := xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryRemoved, Removed: &key}

	row, ok := entryChangeRow(100, time.Unix(0, 0).UTC(), "txdef", -1, 5, c)
	if !ok {
		t.Fatal("entryChangeRow returned ok=false for a valid removed-trustline change")
	}
	if row.ChangeType != "removed" || row.EntryType != "trustline" {
		t.Errorf("change/entry type = %q/%q, want removed/trustline", row.ChangeType, row.EntryType)
	}
	if row.KeyXDR == "" {
		t.Error("removed change should still carry the key XDR")
	}
	if row.EntryXDR != "" {
		t.Errorf("removed change should have no entry XDR, got %q", row.EntryXDR)
	}
	if row.OpIndex != -1 { // tx-level / fee-meta marker
		t.Errorf("op_index = %d, want -1", row.OpIndex)
	}
}

func TestEntryTypeName(t *testing.T) {
	cases := map[xdr.LedgerEntryType]string{
		xdr.LedgerEntryTypeAccount:          "account",
		xdr.LedgerEntryTypeTrustline:        "trustline",
		xdr.LedgerEntryTypeOffer:            "offer",
		xdr.LedgerEntryTypeContractData:     "contract_data",
		xdr.LedgerEntryTypeClaimableBalance: "claimable_balance",
	}
	for in, want := range cases {
		if got := entryTypeName(in); got != want {
			t.Errorf("entryTypeName(%v) = %q, want %q", in, got, want)
		}
	}
}
