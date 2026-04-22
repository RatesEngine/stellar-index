package canonical

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
)

// AssetType tags the shape of an Asset.
type AssetType string

const (
	// AssetNative is XLM, the native Stellar asset.
	AssetNative AssetType = "native"

	// AssetClassic is a classic Stellar asset with a code and issuer
	// G-address. Codes are 1-4 chars (AssetTypeCreditAlphanum4) or
	// 5-12 chars (AssetTypeCreditAlphanum12).
	AssetClassic AssetType = "classic"

	// AssetSoroban is a Soroban contract-based asset. The identity
	// is a single C-address; code/issuer may be resolvable via
	// SEP-41 symbol()/decimals() but are not part of canonical identity.
	AssetSoroban AssetType = "soroban"
)

// Asset is a canonical identifier for any asset we price.
//
// The three valid shapes:
//
//   - Native:  {Type: AssetNative}
//   - Classic: {Type: AssetClassic, Code: "USDC", Issuer: "G..."}
//   - Soroban: {Type: AssetSoroban, ContractID: "C..."}
//
// A SAC-wrapped classic asset can be represented either way —
// canonical form is the **classic** representation; the SAC contract
// address is metadata, accessed via [Asset.SacContractID] (to land
// later alongside the Stellar Asset Contract bridge in
// `internal/sources/sdex`).
//
// Zero value is **not** a valid Asset; use [NativeAsset],
// [NewClassicAsset], or [NewSorobanAsset].
type Asset struct {
	Type AssetType `json:"type"`

	// Code is the 1-12 character asset code for classic assets.
	// Empty for native / soroban.
	Code string `json:"code,omitempty"`

	// Issuer is the G-address of the issuer for classic assets.
	// Empty for native / soroban.
	Issuer string `json:"issuer,omitempty"`

	// ContractID is the C-address for Soroban tokens (or for the
	// SAC bridge of a classic asset, when we use the Soroban form).
	// Empty for native / classic-only.
	ContractID string `json:"contract_id,omitempty"`
}

// NativeAsset returns the XLM singleton.
func NativeAsset() Asset {
	return Asset{Type: AssetNative}
}

// NewClassicAsset constructs a classic Stellar asset. Returns an
// error if code or issuer fail validation.
func NewClassicAsset(code, issuer string) (Asset, error) {
	if l := len(code); l == 0 || l > 12 {
		return Asset{}, fmt.Errorf("%w: code %q length %d (expected 1-12)",
			ErrInvalidAsset, code, l)
	}
	if err := validateAccountID(issuer); err != nil {
		return Asset{}, err
	}
	return Asset{Type: AssetClassic, Code: code, Issuer: issuer}, nil
}

// NewSorobanAsset constructs a Soroban contract-based asset.
func NewSorobanAsset(contractID string) (Asset, error) {
	if err := validateContractID(contractID); err != nil {
		return Asset{}, err
	}
	return Asset{Type: AssetSoroban, ContractID: contractID}, nil
}

// String returns the canonical wire identifier as specified in
// docs/reference/api-design.md §3:
//
//   - "native"                                       (native)
//   - "<code>-<issuer>"                              (classic)
//   - "<contract_id>"                                (soroban)
//
// The `<code>:<issuer>` alias is accepted on input by [ParseAsset]
// but output is always dash-separated for consistency.
func (a Asset) String() string {
	switch a.Type {
	case AssetNative:
		return "native"
	case AssetClassic:
		return a.Code + "-" + a.Issuer
	case AssetSoroban:
		return a.ContractID
	default:
		return "invalid-asset"
	}
}

// IsZero reports whether a is the zero Asset (never valid).
func (a Asset) IsZero() bool {
	return a.Type == "" && a.Code == "" && a.Issuer == "" && a.ContractID == ""
}

// Validate returns nil if a is one of the three valid shapes; an
// error otherwise.
func (a Asset) Validate() error {
	switch a.Type {
	case AssetNative:
		if a.Code != "" || a.Issuer != "" || a.ContractID != "" {
			return fmt.Errorf("%w: native asset must not carry code/issuer/contract_id", ErrInvalidAsset)
		}
		return nil
	case AssetClassic:
		if a.ContractID != "" {
			return fmt.Errorf("%w: classic asset must not carry contract_id", ErrInvalidAsset)
		}
		if l := len(a.Code); l == 0 || l > 12 {
			return fmt.Errorf("%w: classic asset code %q length %d", ErrInvalidAsset, a.Code, l)
		}
		return validateAccountID(a.Issuer)
	case AssetSoroban:
		if a.Code != "" || a.Issuer != "" {
			return fmt.Errorf("%w: soroban asset must not carry code/issuer", ErrInvalidAsset)
		}
		return validateContractID(a.ContractID)
	default:
		return fmt.Errorf("%w: unknown type %q", ErrInvalidAsset, a.Type)
	}
}

// Equal reports whether a and b name the same asset. Note a classic
// asset and its SAC-wrapped Soroban form are NOT equal under this
// function — they are different canonical representations. Bridging
// the two is the job of the SAC registry (future).
func (a Asset) Equal(b Asset) bool {
	return a.Type == b.Type &&
		a.Code == b.Code &&
		a.Issuer == b.Issuer &&
		a.ContractID == b.ContractID
}

// ParseAsset is the inverse of String. Accepts all three canonical
// forms plus the `<code>:<issuer>` alias for classic assets.
func ParseAsset(s string) (Asset, error) {
	if s == "" {
		return Asset{}, fmt.Errorf("%w: empty asset identifier", ErrInvalidAsset)
	}

	// Native
	if s == "native" {
		return NativeAsset(), nil
	}

	// Soroban — starts with C, 56 chars
	if IsContractID(s) {
		return NewSorobanAsset(s)
	}

	// Classic — "CODE-ISSUER" or "CODE:ISSUER"
	sep := strings.IndexAny(s, "-:")
	if sep > 0 && sep < len(s)-1 {
		code := s[:sep]
		issuer := s[sep+1:]
		return NewClassicAsset(code, issuer)
	}

	return Asset{}, fmt.Errorf("%w: %q does not match any known asset format", ErrInvalidAsset, s)
}

// ─── JSON ──────────────────────────────────────────────────────────

// MarshalJSON emits the **canonical string form**, not the struct.
// This matches the API wire contract: asset identifiers are flat
// strings, not JSON objects. Callers who need the structured form
// access the fields directly.
func (a Asset) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

// UnmarshalJSON accepts the canonical string form or the structured
// object form. String is preferred; object form is used by storage
// layers that keep the fields split.
func (a *Asset) UnmarshalJSON(b []byte) error {
	// Try string first
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		parsed, perr := ParseAsset(s)
		if perr != nil {
			return perr
		}
		*a = parsed
		return nil
	}

	// Structured form
	type raw Asset
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("%w: must be string or object: %v", ErrInvalidAsset, err)
	}
	*a = Asset(r)
	return a.Validate()
}

// ─── database/sql ──────────────────────────────────────────────────

// Value implements driver.Valuer — emits the canonical string form.
// Suitable for a TEXT or VARCHAR column keyed as the asset identifier.
func (a Asset) Value() (driver.Value, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return a.String(), nil
}

// Scan implements sql.Scanner.
func (a *Asset) Scan(src any) error {
	if src == nil {
		*a = Asset{}
		return nil
	}
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("%w: cannot scan %T", ErrInvalidAsset, src)
	}
	parsed, err := ParseAsset(s)
	if err != nil {
		return err
	}
	*a = parsed
	return nil
}
