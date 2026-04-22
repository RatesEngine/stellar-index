package canonical

import (
	"fmt"
	"regexp"
)

// Strkey format validators.
//
// Full strkey encoding (SEP-23) is a base32-encoded payload with a
// CRC-16 checksum byte. Validating the checksum requires the
// base32 decoder + CRC computation. At this package boundary we
// do a **format-only** check — the prefix letter + total length.
//
// Full SEP-23 validation (including CRC) will come from the
// stellar-go SDK's strkey package when we import it for the decoder
// wrapper. Until then, the format check is sufficient because every
// strkey that reaches this package was already produced by an SDK
// decoder upstream.
//
// TODO(#0): switch to github.com/stellar/go-stellar-sdk/strkey once
// we take the SDK dependency for the ledger-meta decoder.

var (
	// G-address: account / issuer.  56 base32 chars starting with G.
	accountIDPattern = regexp.MustCompile(`^G[A-Z2-7]{55}$`)

	// C-address: Soroban contract.  56 base32 chars starting with C.
	contractIDPattern = regexp.MustCompile(`^C[A-Z2-7]{55}$`)
)

// IsAccountID reports whether s looks like a Stellar account / issuer
// public key (format-only, not CRC-verified).
func IsAccountID(s string) bool {
	return accountIDPattern.MatchString(s)
}

// IsContractID reports whether s looks like a Soroban contract
// address (format-only, not CRC-verified).
func IsContractID(s string) bool {
	return contractIDPattern.MatchString(s)
}

// validateAccountID returns an error describing why s is not a
// valid account/issuer strkey, or nil if it passes the format check.
func validateAccountID(s string) error {
	if !IsAccountID(s) {
		return fmt.Errorf("%w: %q is not a valid G-strkey (expected 56 chars starting with G)",
			ErrInvalidStrkey, s)
	}
	return nil
}

// validateContractID is the contract-address analogue of validateAccountID.
func validateContractID(s string) error {
	if !IsContractID(s) {
		return fmt.Errorf("%w: %q is not a valid C-strkey (expected 56 chars starting with C)",
			ErrInvalidStrkey, s)
	}
	return nil
}
