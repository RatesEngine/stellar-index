package supply_test

import (
	"strings"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/supply"
)

// TestLockedSet_IsEmpty — the IsEmpty discriminator drives a
// per-algorithm short-circuit; both nil-slice and empty-slice
// inputs must report empty.
func TestLockedSet_IsEmpty(t *testing.T) {
	tests := []struct {
		name string
		set  supply.LockedSet
		want bool
	}{
		{"zero value", supply.LockedSet{}, true},
		{"empty slices", supply.LockedSet{Accounts: []string{}, Contracts: []string{}}, true},
		{"only accounts", supply.LockedSet{Accounts: []string{"GA..."}}, false},
		{"only contracts", supply.LockedSet{Contracts: []string{"C..."}}, false},
		{"both populated", supply.LockedSet{Accounts: []string{"GA..."}, Contracts: []string{"C..."}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.set.IsEmpty(); got != tc.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPolicy_MaxSupplyOverride_Present — happy path: configured
// override returns a parsed *big.Int.
func TestPolicy_MaxSupplyOverride_Present(t *testing.T) {
	p := supply.Policy{
		MaxSupplyOverrides: map[string]string{"USDC:GA1": "21000000000000000"},
	}
	got, ok, err := p.MaxSupplyOverride("USDC:GA1")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got.String() != "21000000000000000" {
		t.Errorf("got = %s, want 21000000000000000", got)
	}
}

// TestPolicy_MaxSupplyOverride_AbsentOrEmpty — both "key missing"
// and "key present with empty value" fall through with ok=false; the
// empty-string sentinel is documented as "fall through to next
// source" in the Policy struct doc.
func TestPolicy_MaxSupplyOverride_AbsentOrEmpty(t *testing.T) {
	p := supply.Policy{
		MaxSupplyOverrides: map[string]string{"USDC:GA1": ""},
	}
	for _, key := range []string{"missing-key", "USDC:GA1"} {
		_, ok, err := p.MaxSupplyOverride(key)
		if err != nil {
			t.Errorf("%s: err = %v, want nil", key, err)
		}
		if ok {
			t.Errorf("%s: ok = true, want false", key)
		}
	}
}

// TestPolicy_MaxSupplyOverride_BadValue — non-numeric values must
// error rather than silently returning zero (which would coerce a
// typo'd YAML to a "supply has been entirely burned" reading).
func TestPolicy_MaxSupplyOverride_BadValue(t *testing.T) {
	p := supply.Policy{
		MaxSupplyOverrides: map[string]string{"X": "not-a-number"},
	}
	_, _, err := p.MaxSupplyOverride("X")
	if err == nil {
		t.Fatal("expected error for non-numeric override; got nil")
	}
	if !strings.Contains(err.Error(), "decimal integer") {
		t.Errorf("error message lacks the diagnostic phrase: %v", err)
	}
}

// TestPolicy_Validate_Clean — a policy with no entries is valid;
// the per-algorithm computers are responsible for handling the
// empty-config case (XLM returns total==circulating).
func TestPolicy_Validate_Clean(t *testing.T) {
	p := supply.Policy{}
	if err := p.Validate(); err != nil {
		t.Errorf("zero-value policy should validate; got %v", err)
	}
}

// TestPolicy_Validate_RejectsEmptyEntries — empty strings in the
// account / contract lists are typos in YAML; loud rejection at
// startup is better than producing a "balance for empty account ID"
// downstream.
func TestPolicy_Validate_RejectsEmptyEntries(t *testing.T) {
	p := supply.Policy{
		SDFReserveAccounts: []string{"GA...", ""},
		PerAsset: map[string]supply.LockedSet{
			"USDC:GA1": {Accounts: []string{""}, Contracts: []string{""}},
		},
		MaxSupplyOverrides: map[string]string{"X": "garbage"},
	}
	err := p.Validate()
	if err == nil {
		t.Fatal("expected validation errors; got nil")
	}
	// errors.Join formats multiple errors one per line.
	for _, want := range []string{
		"SDFReserveAccounts[1]",
		`PerAsset["USDC:GA1"].Accounts[0]`,
		`PerAsset["USDC:GA1"].Contracts[0]`,
		"decimal integer",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("validate output missing %q: %v", want, err)
		}
	}
}
