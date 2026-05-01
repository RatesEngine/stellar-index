package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"
)

// TestMaybeWriteWasmCode_AcceptsAllEntryBearingChangeTypes is the
// regression test for the 2026-05-01 r1-walk audit finding: the
// original implementation only matched Created + Restored, which
// caused the tool to return MISSING for every hash even though
// the wasm-history walker (which handles Created + Updated +
// Restored) read the same archive cleanly. The fix adds Updated
// and State so any LedgerEntry-bearing change carrying matching
// ContractCode bytes is captured.
func TestMaybeWriteWasmCode_AcceptsAllEntryBearingChangeTypes(t *testing.T) {
	tmp := t.TempDir()

	var hash sdkxdr.Hash
	for i := range hash {
		hash[i] = byte(i + 1)
	}
	hashHex := "01020304050607080910111213141516171819202122232425262728293031" +
		"32"
	wantHashes := map[sdkxdr.Hash]struct{}{hash: {}}
	wantHexes := map[sdkxdr.Hash]string{hash: hashHex}

	mkChange := func(t sdkxdr.LedgerEntryChangeType, code []byte) sdkxdr.LedgerEntryChange {
		entry := &sdkxdr.LedgerEntry{
			Data: sdkxdr.LedgerEntryData{
				Type: sdkxdr.LedgerEntryTypeContractCode,
				ContractCode: &sdkxdr.ContractCodeEntry{
					Hash: hash,
					Code: code,
				},
			},
		}
		change := sdkxdr.LedgerEntryChange{Type: t}
		switch t {
		case sdkxdr.LedgerEntryChangeTypeLedgerEntryCreated:
			change.Created = entry
		case sdkxdr.LedgerEntryChangeTypeLedgerEntryUpdated:
			change.Updated = entry
		case sdkxdr.LedgerEntryChangeTypeLedgerEntryState:
			change.State = entry
		case sdkxdr.LedgerEntryChangeTypeLedgerEntryRestored:
			change.Restored = entry
		}
		return change
	}

	for _, tc := range []struct {
		name string
		typ  sdkxdr.LedgerEntryChangeType
	}{
		{"Created", sdkxdr.LedgerEntryChangeTypeLedgerEntryCreated},
		{"Updated", sdkxdr.LedgerEntryChangeTypeLedgerEntryUpdated},
		{"State", sdkxdr.LedgerEntryChangeTypeLedgerEntryState},
		{"Restored", sdkxdr.LedgerEntryChangeTypeLedgerEntryRestored},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outDir := filepath.Join(tmp, tc.name)
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			found := map[sdkxdr.Hash]string{}
			body := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0xde, 0xad}
			change := mkChange(tc.typ, body)
			maybeWriteWasmCode(&change, wantHashes, outDir, wantHexes, &mu, found)

			if _, ok := found[hash]; !ok {
				t.Fatalf("change type %s: hash not found in result map", tc.name)
			}
			path := filepath.Join(outDir, hashHex+".wasm")
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read output: %v", err)
			}
			if string(got) != string(body) {
				t.Errorf("body mismatch")
			}
		})
	}
}

// TestMaybeWriteWasmCode_IgnoresRemoved verifies the function
// short-circuits on REMOVED changes (which carry a LedgerKey, not
// a LedgerEntry — different XDR shape, no bytes to extract).
func TestMaybeWriteWasmCode_IgnoresRemoved(t *testing.T) {
	tmp := t.TempDir()
	var hash sdkxdr.Hash
	hashHex := "0102"
	wantHashes := map[sdkxdr.Hash]struct{}{hash: {}}
	wantHexes := map[sdkxdr.Hash]string{hash: hashHex}
	change := sdkxdr.LedgerEntryChange{
		Type: sdkxdr.LedgerEntryChangeTypeLedgerEntryRemoved,
	}
	var mu sync.Mutex
	found := map[sdkxdr.Hash]string{}
	maybeWriteWasmCode(&change, wantHashes, tmp, wantHexes, &mu, found)
	if len(found) != 0 {
		t.Fatalf("expected no-op on Removed; got %d entries", len(found))
	}
}
