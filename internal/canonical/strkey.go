package canonical

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
)

// Strkey validators backed by the go-stellar-sdk strkey package —
// SEP-23 base32 + CRC-16 checksum verification, not just format.
//
// Earlier revisions did a format-only regex check at this boundary
// because we hadn't yet taken the SDK dependency. Now that the SDK
// is widely used (ledgerstream, sdex, scval, …), there's no reason
// to accept a strkey whose prefix + length match but whose checksum
// is wrong — the SDK's decoder is the single source of truth.

// IsAccountID reports whether s is a valid Stellar account / issuer
// public key (G-strkey), CRC-checked.
func IsAccountID(s string) bool {
	_, err := strkey.Decode(strkey.VersionByteAccountID, s)
	return err == nil
}

// IsContractID reports whether s is a valid Soroban contract
// address (C-strkey), CRC-checked.
func IsContractID(s string) bool {
	_, err := strkey.Decode(strkey.VersionByteContract, s)
	return err == nil
}

// validateAccountID returns an error describing why s is not a
// valid account/issuer strkey, or nil on success.
func validateAccountID(s string) error {
	if !IsAccountID(s) {
		return fmt.Errorf("%w: %q is not a valid G-strkey (must be 56 chars starting with G with a valid CRC)",
			ErrInvalidStrkey, s)
	}
	return nil
}

// validateContractID is the contract-address analogue of validateAccountID.
func validateContractID(s string) error {
	if !IsContractID(s) {
		return fmt.Errorf("%w: %q is not a valid C-strkey (must be 56 chars starting with C with a valid CRC)",
			ErrInvalidStrkey, s)
	}
	return nil
}
