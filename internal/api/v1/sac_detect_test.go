package v1

import "testing"

// nativeSACPubnet is the well-known Stellar Asset Contract id for native XLM
// on pubnet — the busiest contract on the network. buildKnownSACs must derive
// exactly this from the native asset + the pubnet passphrase.
const nativeSACPubnet = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"

func TestIsKnownSAC_NativeDerivation(t *testing.T) {
	s := &Server{
		networkPassphrase: "Public Global Stellar Network ; September 2015",
		sacWrappers:       map[string]string{"CWRAPPED": "FOO:GISSUER"},
	}
	if !s.isKnownSAC(nativeSACPubnet) {
		t.Errorf("native SAC %s not detected; computed set = %v", nativeSACPubnet, s.knownSACs)
	}
	// sac_wrappers entries are included verbatim.
	if !s.isKnownSAC("CWRAPPED") {
		t.Error("operator sac_wrappers entry not detected")
	}
	// A random contract id is not a SAC.
	if s.isKnownSAC("CBNOTASACXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX") {
		t.Error("non-SAC contract id falsely detected as SAC")
	}
}

// Without a passphrase the computed half is skipped, but sac_wrappers still
// apply — the check must never panic on the empty-passphrase path.
func TestIsKnownSAC_NoPassphrase(t *testing.T) {
	s := &Server{sacWrappers: map[string]string{"CWRAPPED": "FOO:GISSUER"}}
	if !s.isKnownSAC("CWRAPPED") {
		t.Error("sac_wrappers entry not detected without passphrase")
	}
	if s.isKnownSAC(nativeSACPubnet) {
		t.Error("native SAC detected without a passphrase (computation should be skipped)")
	}
}
