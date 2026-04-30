package accounts

import (
	"errors"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/dispatcher"
)

// G-strkeys used across the test file. Both are the canonical
// "test public key" values from the SDK's test fixtures, so they
// strkey-encode cleanly.
// Both G-strkeys are derived from synthetic 32-byte public keys
// at test time — picked over real-world Stellar accounts so the
// tests don't accidentally encode a meaningful issuer / SDF
// reserve / wallet address into the test fixtures.
const (
	gWatched   = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"
	gUnwatched = "GAAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQDZ7H"
)

func mustEd25519FromStrkey(t *testing.T, gAddr string) [32]byte {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteAccountID, gAddr)
	if err != nil {
		t.Fatalf("strkey.Decode(%q): %v", gAddr, err)
	}
	if len(raw) != 32 {
		t.Fatalf("strkey raw length = %d, want 32", len(raw))
	}
	var k [32]byte
	copy(k[:], raw)
	return k
}

func makeAccountChange(t *testing.T, gAddr string, balance int64, homeDomain string) xdr.LedgerEntryChange {
	t.Helper()
	pk := mustEd25519FromStrkey(t, gAddr)
	aid := xdr.AccountId{
		Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
		Ed25519: (*xdr.Uint256)(&pk),
	}
	return xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
		Updated: &xdr.LedgerEntry{
			Data: xdr.LedgerEntryData{
				Type: xdr.LedgerEntryTypeAccount,
				Account: &xdr.AccountEntry{
					AccountId:  aid,
					Balance:    xdr.Int64(balance),
					HomeDomain: xdr.String32(homeDomain),
					Flags:      xdr.Uint32(0),
					SeqNum:     xdr.SequenceNumber(42),
				},
			},
		},
	}
}

func makeContractCodeChange() xdr.LedgerEntryChange {
	return xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryCreated,
		Created: &xdr.LedgerEntry{
			Data: xdr.LedgerEntryData{
				Type:         xdr.LedgerEntryTypeContractCode,
				ContractCode: &xdr.ContractCodeEntry{},
			},
		},
	}
}

func TestNewObserver_RejectsEmpty(t *testing.T) {
	if _, err := NewObserver(nil); !errors.Is(err, ErrEmptyWatchSet) {
		t.Errorf("nil watched: err=%v want ErrEmptyWatchSet", err)
	}
	if _, err := NewObserver([]string{}); !errors.Is(err, ErrEmptyWatchSet) {
		t.Errorf("empty watched: err=%v want ErrEmptyWatchSet", err)
	}
	if _, err := NewObserver([]string{""}); err == nil {
		t.Errorf("empty G-strkey in list should error")
	}
}

func TestObserver_MatchesWatchedAccountEntry(t *testing.T) {
	o, err := NewObserver([]string{gWatched})
	if err != nil {
		t.Fatalf("NewObserver: %v", err)
	}
	if !o.Matches(makeAccountChange(t, gWatched, 1_000_000, "")) {
		t.Errorf("expected match on watched account")
	}
}

func TestObserver_SkipsUnwatchedAccountEntry(t *testing.T) {
	o, err := NewObserver([]string{gWatched})
	if err != nil {
		t.Fatalf("NewObserver: %v", err)
	}
	if o.Matches(makeAccountChange(t, gUnwatched, 1_000_000, "")) {
		t.Errorf("expected NO match on unwatched account")
	}
}

// TestObserver_SkipsNonAccountEntry — the type discriminator
// pre-filter blocks ContractCode / Trustline / etc. before the
// watched-set check runs. Defence-in-depth: a future bug that
// added a watched contract C-strkey to the set wouldn't suddenly
// fire on contract-code changes.
func TestObserver_SkipsNonAccountEntry(t *testing.T) {
	o, err := NewObserver([]string{gWatched})
	if err != nil {
		t.Fatalf("NewObserver: %v", err)
	}
	if o.Matches(makeContractCodeChange()) {
		t.Errorf("expected NO match on ContractCode change")
	}
}

func TestObserver_DecodeBuildsObservation(t *testing.T) {
	o, err := NewObserver([]string{gWatched})
	if err != nil {
		t.Fatalf("NewObserver: %v", err)
	}
	now := time.Unix(1_770_000_000, 0).UTC()
	change := makeAccountChange(t, gWatched, 999_888_777, "ratesengine.net")
	outs, err := o.Decode(dispatcher.LedgerEntryChangeContext{
		Ledger:   123_456,
		ClosedAt: now,
		TxHash:   "deadbeef",
		OpIndex:  2,
		Change:   change,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("got %d outputs, want 1", len(outs))
	}
	obs, ok := outs[0].(Observation)
	if !ok {
		t.Fatalf("output is not Observation: %T", outs[0])
	}
	if obs.AccountID != gWatched {
		t.Errorf("AccountID=%q, want %q", obs.AccountID, gWatched)
	}
	if obs.Balance.Int64() != 999_888_777 {
		t.Errorf("Balance=%s, want 999888777", obs.Balance)
	}
	if obs.HomeDomain != "ratesengine.net" {
		t.Errorf("HomeDomain=%q, want ratesengine.net", obs.HomeDomain)
	}
	if obs.Ledger != 123_456 {
		t.Errorf("Ledger=%d, want 123456", obs.Ledger)
	}
	if !obs.ObservedAt.Equal(now) {
		t.Errorf("ObservedAt=%v, want %v", obs.ObservedAt, now)
	}
	if obs.IsRemoval {
		t.Errorf("IsRemoval=true on Updated change, want false")
	}
}

// TestObserver_DecodeRemoval — Removed-variant changes produce
// IsRemoval=true with zeroed balance/home_domain.
func TestObserver_DecodeRemoval(t *testing.T) {
	o, err := NewObserver([]string{gWatched})
	if err != nil {
		t.Fatalf("NewObserver: %v", err)
	}
	pk := mustEd25519FromStrkey(t, gWatched)
	aid := xdr.AccountId{
		Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
		Ed25519: (*xdr.Uint256)(&pk),
	}
	change := xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryRemoved,
		Removed: &xdr.LedgerKey{
			Type: xdr.LedgerEntryTypeAccount,
			Account: &xdr.LedgerKeyAccount{
				AccountId: aid,
			},
		},
	}
	outs, err := o.Decode(dispatcher.LedgerEntryChangeContext{
		Ledger: 999,
		Change: change,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("got %d outputs, want 1", len(outs))
	}
	obs := outs[0].(Observation)
	if !obs.IsRemoval {
		t.Errorf("IsRemoval=false on Removed change, want true")
	}
	if obs.Balance.Sign() != 0 {
		t.Errorf("removal balance=%s, want 0", obs.Balance)
	}
	if obs.HomeDomain != "" {
		t.Errorf("removal HomeDomain=%q, want empty", obs.HomeDomain)
	}
}

// TestObserver_RoundTripThroughDispatcher — registers the observer
// against a real dispatcher and verifies routing end-to-end via
// the RouteEntryChange test-harness entry point.
func TestObserver_RoundTripThroughDispatcher(t *testing.T) {
	o, err := NewObserver([]string{gWatched})
	if err != nil {
		t.Fatalf("NewObserver: %v", err)
	}
	disp := dispatcher.New()
	disp.AddEntryDecoder(o)

	outs, err := disp.RouteEntryChange(dispatcher.LedgerEntryChangeContext{
		Ledger: 1,
		Change: makeAccountChange(t, gWatched, 100_000, "example.com"),
	})
	if err != nil {
		t.Fatalf("RouteEntryChange: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("got %d outputs, want 1", len(outs))
	}
	if outs[0].EventKind() != ObservationKind {
		t.Errorf("EventKind=%q, want %q", outs[0].EventKind(), ObservationKind)
	}
}
