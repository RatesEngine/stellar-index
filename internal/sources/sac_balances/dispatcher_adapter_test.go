package sac_balances

import (
	"errors"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
)

const (
	// Valid C-strkey (zero contract id) generated at test-design
	// time so the test fixture isn't dependent on encoding helpers.
	cSAC    = "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"
	gHolder = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"
)

func mustEdAccount(t *testing.T, gAddr string) [32]byte {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteAccountID, gAddr)
	if err != nil {
		t.Fatalf("strkey.Decode(%q): %v", gAddr, err)
	}
	var k [32]byte
	copy(k[:], raw)
	return k
}

func mustContractID(t *testing.T, cAddr string) [32]byte {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteContract, cAddr)
	if err != nil {
		t.Fatalf("strkey.Decode(%q): %v", cAddr, err)
	}
	var k [32]byte
	copy(k[:], raw)
	return k
}

func makeBalanceKey(t *testing.T, holder string) xdr.ScVal {
	t.Helper()
	holderPK := mustEdAccount(t, holder)
	holderAID := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: (*xdr.Uint256)(&holderPK)}
	addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &holderAID}
	addrSV := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}

	sym := xdr.ScSymbol("Balance")
	symSV := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}

	vec := xdr.ScVec{symSV, addrSV}
	vp := &vec
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &vp}
}

func makeI128Val(amount int64) xdr.ScVal {
	return xdr.ScVal{
		Type: xdr.ScValTypeScvI128,
		I128: &xdr.Int128Parts{Hi: 0, Lo: xdr.Uint64(amount)},
	}
}

func makeBalanceMapVal(amount int64) xdr.ScVal {
	amtSV := makeI128Val(amount)
	amtSym := xdr.ScSymbol("amount")
	authSym := xdr.ScSymbol("authorized")
	clbSym := xdr.ScSymbol("clawback")
	trueB := true
	m := xdr.ScMap{
		{Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &amtSym}, Val: amtSV},
		{Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &authSym}, Val: xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &trueB}},
		{Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &clbSym}, Val: xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &trueB}},
	}
	mp := &m
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mp}
}

func makeContractDataChange(t *testing.T, contractID string, key xdr.ScVal, val xdr.ScVal) xdr.LedgerEntryChange {
	t.Helper()
	cid := mustContractID(t, contractID)
	contract := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: (*xdr.ContractId)(&cid)}
	return xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
		Updated: &xdr.LedgerEntry{
			Data: xdr.LedgerEntryData{
				Type: xdr.LedgerEntryTypeContractData,
				ContractData: &xdr.ContractDataEntry{
					Contract:   contract,
					Key:        key,
					Durability: xdr.ContractDataDurabilityPersistent,
					Val:        val,
				},
			},
		},
	}
}

func TestNewObserver_RejectsEmpty(t *testing.T) {
	if _, err := NewObserver(nil); !errors.Is(err, ErrEmptyWrapperMap) {
		t.Errorf("nil: err=%v want ErrEmptyWrapperMap", err)
	}
	if _, err := NewObserver(map[string]string{"": "USDC:G..."}); err == nil {
		t.Errorf("empty contract id should error")
	}
	if _, err := NewObserver(map[string]string{cSAC: ""}); err == nil {
		t.Errorf("empty asset_key should error")
	}
}

func TestObserver_MatchesWatchedSAC_I128Val(t *testing.T) {
	o, err := NewObserver(map[string]string{cSAC: "USDC:G..."})
	if err != nil {
		t.Fatalf("NewObserver: %v", err)
	}
	change := makeContractDataChange(t, cSAC, makeBalanceKey(t, gHolder), makeI128Val(1_000_000))
	if !o.Matches(change) {
		t.Errorf("expected match on watched SAC + i128 balance value")
	}
}

func TestObserver_MatchesWatchedSAC_MapVal(t *testing.T) {
	o, err := NewObserver(map[string]string{cSAC: "USDC:G..."})
	if err != nil {
		t.Fatalf("NewObserver: %v", err)
	}
	change := makeContractDataChange(t, cSAC, makeBalanceKey(t, gHolder), makeBalanceMapVal(1_000_000))
	if !o.Matches(change) {
		t.Errorf("expected match on watched SAC + BalanceValue map")
	}
}

// TestObserver_SkipsWrongKey — the contract is watched but the
// Key isn't a Balance entry (e.g. it's a metadata key). Match
// should reject.
func TestObserver_SkipsWrongKey(t *testing.T) {
	o, _ := NewObserver(map[string]string{cSAC: "USDC:G..."})
	wrongSym := xdr.ScSymbol("Allowance")
	wrongVec := xdr.ScVec{xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &wrongSym}}
	wp := &wrongVec
	wrongKey := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &wp}
	change := makeContractDataChange(t, cSAC, wrongKey, makeI128Val(1))
	if o.Matches(change) {
		t.Errorf("expected NO match — key is Allowance, not Balance")
	}
}

func TestObserver_DecodeI128(t *testing.T) {
	o, _ := NewObserver(map[string]string{cSAC: "USDC:G..."})
	change := makeContractDataChange(t, cSAC, makeBalanceKey(t, gHolder), makeI128Val(987_654_321))
	outs, err := o.Decode(dispatcher.LedgerEntryChangeContext{
		Ledger: 1, Change: change, ClosedAt: time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	obs := outs[0].(Observation)
	if obs.AssetKey != "USDC:G..." {
		t.Errorf("AssetKey=%q want USDC:G...", obs.AssetKey)
	}
	if obs.Holder != gHolder {
		t.Errorf("Holder=%q want %q", obs.Holder, gHolder)
	}
	if obs.Balance.Int64() != 987_654_321 {
		t.Errorf("Balance=%s want 987654321", obs.Balance)
	}
	if obs.IsRemoval {
		t.Errorf("IsRemoval=true on Updated change, want false")
	}
}

func TestObserver_DecodeMapVal(t *testing.T) {
	o, _ := NewObserver(map[string]string{cSAC: "USDC:G..."})
	change := makeContractDataChange(t, cSAC, makeBalanceKey(t, gHolder), makeBalanceMapVal(555))
	outs, err := o.Decode(dispatcher.LedgerEntryChangeContext{
		Ledger: 1, Change: change,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	obs := outs[0].(Observation)
	if obs.Balance.Int64() != 555 {
		t.Errorf("Balance=%s want 555 (from BalanceValue map)", obs.Balance)
	}
}

// TestObserver_DecodeRemoved — Removed-variant SAC entries emit
// IsRemoval=true with Balance=0. Asset_key still populates from
// the operator map.
func TestObserver_DecodeRemoved(t *testing.T) {
	o, _ := NewObserver(map[string]string{cSAC: "USDC:G..."})
	cid := mustContractID(t, cSAC)
	contract := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: (*xdr.ContractId)(&cid)}
	change := xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryRemoved,
		Removed: &xdr.LedgerKey{
			Type: xdr.LedgerEntryTypeContractData,
			ContractData: &xdr.LedgerKeyContractData{
				Contract:   contract,
				Key:        makeBalanceKey(t, gHolder),
				Durability: xdr.ContractDataDurabilityPersistent,
			},
		},
	}
	if !o.Matches(change) {
		t.Fatalf("expected match on Removed SAC balance entry")
	}
	outs, err := o.Decode(dispatcher.LedgerEntryChangeContext{
		Ledger: 1, Change: change,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	obs := outs[0].(Observation)
	if !obs.IsRemoval {
		t.Errorf("IsRemoval=false on Removed change, want true")
	}
	if obs.Balance.Sign() != 0 {
		t.Errorf("Balance=%s want 0 (removed)", obs.Balance)
	}
	if obs.AssetKey != "USDC:G..." {
		t.Errorf("AssetKey=%q want USDC:G... (from operator map)", obs.AssetKey)
	}
}

func TestObserver_RoundTripThroughDispatcher(t *testing.T) {
	o, _ := NewObserver(map[string]string{cSAC: "USDC:G..."})
	disp := dispatcher.New()
	disp.AddEntryDecoder(o)

	change := makeContractDataChange(t, cSAC, makeBalanceKey(t, gHolder), makeI128Val(100))
	outs, err := disp.RouteEntryChange(dispatcher.LedgerEntryChangeContext{
		Ledger: 1, Change: change,
	})
	if err != nil {
		t.Fatalf("RouteEntryChange: %v", err)
	}
	if len(outs) != 1 || outs[0].EventKind() != ObservationKind {
		t.Errorf("dispatcher round-trip lost the observation")
	}
}
