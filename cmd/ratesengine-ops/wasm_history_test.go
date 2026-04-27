package main

import (
	"testing"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"
)

// TestRecordWasmTransition_FirstSeen confirms an initial deploy
// opens an entry whose ToLedger is unset (open range).
func TestRecordWasmTransition_FirstSeen(t *testing.T) {
	state := map[sdkxdr.Hash]*wasmContractState{}
	var contract sdkxdr.Hash
	contract[0] = 0x01
	recordWasmTransition(state, contract, "abc", 100)

	if got := len(state[contract].ranges); got != 1 {
		t.Fatalf("ranges len = %d, want 1", got)
	}
	r := state[contract].ranges[0]
	if r.WasmHash != "abc" || r.FromLedger != 100 || r.ToLedger != 0 {
		t.Errorf("range = %+v, want {WasmHash:abc FromLedger:100 ToLedger:0}", r)
	}
	if state[contract].current != "abc" {
		t.Errorf("current = %q, want abc", state[contract].current)
	}
}

// TestRecordWasmTransition_RepeatSameHash is a no-op — the same
// hash seen at successive ledgers must NOT split into ranges.
func TestRecordWasmTransition_RepeatSameHash(t *testing.T) {
	state := map[sdkxdr.Hash]*wasmContractState{}
	var contract sdkxdr.Hash
	recordWasmTransition(state, contract, "abc", 100)
	recordWasmTransition(state, contract, "abc", 101)
	recordWasmTransition(state, contract, "abc", 200)

	if got := len(state[contract].ranges); got != 1 {
		t.Fatalf("ranges len = %d, want 1 (idempotent on same hash)", got)
	}
}

// TestRecordWasmTransition_Upgrade closes the prior range and
// opens a new one when the hash flips.
func TestRecordWasmTransition_Upgrade(t *testing.T) {
	state := map[sdkxdr.Hash]*wasmContractState{}
	var contract sdkxdr.Hash
	recordWasmTransition(state, contract, "v1hash", 100)
	recordWasmTransition(state, contract, "v1hash", 150) // no-op
	recordWasmTransition(state, contract, "v2hash", 200) // upgrade!
	recordWasmTransition(state, contract, "v3hash", 300) // upgrade again

	ranges := state[contract].ranges
	if len(ranges) != 3 {
		t.Fatalf("ranges len = %d, want 3", len(ranges))
	}
	want := []wasmRange{
		{WasmHash: "v1hash", FromLedger: 100, ToLedger: 199},
		{WasmHash: "v2hash", FromLedger: 200, ToLedger: 299},
		{WasmHash: "v3hash", FromLedger: 300, ToLedger: 0},
	}
	for i, w := range want {
		if ranges[i] != w {
			t.Errorf("range[%d] = %+v, want %+v", i, ranges[i], w)
		}
	}
}

// TestScanLedgerEntryChange_IgnoresUnrelatedContract — a watched
// contract update should NOT trigger when an unrelated contract
// changes.
func TestScanLedgerEntryChange_IgnoresUnrelatedContract(t *testing.T) {
	var watched, other sdkxdr.Hash
	watched[0] = 0xAA
	other[0] = 0xBB

	watch := map[sdkxdr.Hash]string{watched: "CDLZ_watched"}
	state := map[sdkxdr.Hash]*wasmContractState{}

	change := makeUpdateChange(t, other, [32]byte{1, 2, 3})
	scanLedgerEntryChange(change, watch, state, 100)

	if len(state) != 0 {
		t.Errorf("state populated for unwatched contract: %v", state)
	}
}

// TestScanLedgerEntryChange_CapturesWatchedUpgrade — a watched
// contract update emits a transition.
func TestScanLedgerEntryChange_CapturesWatchedUpgrade(t *testing.T) {
	var watched sdkxdr.Hash
	watched[0] = 0xAA

	watch := map[sdkxdr.Hash]string{watched: "CDLZ_watched"}
	state := map[sdkxdr.Hash]*wasmContractState{}

	wasmHash := [32]byte{0xDE, 0xAD, 0xBE, 0xEF}
	change := makeUpdateChange(t, watched, wasmHash)
	scanLedgerEntryChange(change, watch, state, 12345)

	if len(state) != 1 {
		t.Fatalf("state has %d entries, want 1", len(state))
	}
	got := state[watched].ranges
	if len(got) != 1 {
		t.Fatalf("ranges len = %d, want 1", len(got))
	}
	if got[0].FromLedger != 12345 {
		t.Errorf("from = %d, want 12345", got[0].FromLedger)
	}
	wantHashHex := "deadbeef00000000000000000000000000000000000000000000000000000000"
	if got[0].WasmHash != wantHashHex {
		t.Errorf("hash = %s, want %s", got[0].WasmHash, wantHashHex)
	}
}

// makeUpdateChange constructs a synthetic LedgerEntryChange of type
// Updated whose ContractData entry corresponds to the given
// contract's instance row, with the given executable WASM hash.
func makeUpdateChange(t *testing.T, contract sdkxdr.Hash, wasmHash [32]byte) sdkxdr.LedgerEntryChange {
	t.Helper()
	contractID := sdkxdr.ContractId(contract)
	w := sdkxdr.Hash(wasmHash)
	scAddr := sdkxdr.ScAddress{
		Type:       sdkxdr.ScAddressTypeScAddressTypeContract,
		ContractId: &contractID,
	}
	instance := sdkxdr.ScContractInstance{
		Executable: sdkxdr.ContractExecutable{
			Type:     sdkxdr.ContractExecutableTypeContractExecutableWasm,
			WasmHash: &w,
		},
	}
	val := sdkxdr.ScVal{
		Type:     sdkxdr.ScValTypeScvContractInstance,
		Instance: &instance,
	}
	key := sdkxdr.ScVal{Type: sdkxdr.ScValTypeScvLedgerKeyContractInstance}
	cd := sdkxdr.ContractDataEntry{
		Contract:   scAddr,
		Key:        key,
		Durability: sdkxdr.ContractDataDurabilityPersistent,
		Val:        val,
	}
	data := sdkxdr.LedgerEntryData{
		Type:         sdkxdr.LedgerEntryTypeContractData,
		ContractData: &cd,
	}
	entry := &sdkxdr.LedgerEntry{Data: data}
	return sdkxdr.LedgerEntryChange{
		Type:    sdkxdr.LedgerEntryChangeTypeLedgerEntryUpdated,
		Updated: entry,
	}
}
