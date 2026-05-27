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

	// AssetFiat is an off-chain reference currency (USD, EUR, …).
	// NOT a Stellar asset. Wire form: `fiat:<ISO4217>`. See ADR-0010.
	AssetFiat AssetType = "fiat"

	// AssetCrypto is an off-chain crypto-ticker reference (BTC, ETH,
	// USDT, …). NOT a Stellar asset — distinct from AssetSoroban
	// (which requires a real on-chain C-address). Used by
	// Reflector's CEX oracle, which publishes prices keyed on bare
	// global tickers. Wire form: `crypto:<TICKER>`. See ADR-0014.
	AssetCrypto AssetType = "crypto"

	// AssetRWA is an off-chain tokenized real-world asset (tokenized
	// treasuries, money-market funds, equity ETFs — BENJI, GILTS,
	// SPXU, …). NOT a Stellar asset, and deliberately NOT AssetCrypto:
	// a tokenized T-bill is not a cryptocurrency, and lumping it into
	// `crypto` would mis-feed every crypto-scoped surface. Used by
	// RedStone's RWA push feeds. Wire form: `rwa:<CODE>`. See ADR-0028.
	AssetRWA AssetType = "rwa"
)

// Asset is a canonical identifier for any asset we price.
//
// The six valid shapes:
//
//   - Native:  {Type: AssetNative}
//   - Classic: {Type: AssetClassic, Code: "USDC", Issuer: "G..."}
//   - Soroban: {Type: AssetSoroban, ContractID: "C..."}
//   - Fiat:    {Type: AssetFiat,   Code: "USD"}            ADR-0010
//   - Crypto:  {Type: AssetCrypto, Code: "BTC"}            ADR-0014
//   - RWA:     {Type: AssetRWA,    Code: "BENJI"}          ADR-0028
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
	if err := validateClassicAssetCode(code); err != nil {
		return Asset{}, err
	}
	if err := validateAccountID(issuer); err != nil {
		return Asset{}, err
	}
	return Asset{Type: AssetClassic, Code: code, Issuer: issuer}, nil
}

// validateClassicAssetCode enforces Stellar's alphanum4/alphanum12
// asset-code rules: length 1–12, chars [a-zA-Z0-9] only. Rejected
// inputs include empty, over-length, and non-ASCII-alphanumeric
// characters (emoji, spaces, punctuation). Matches XDR
// ASSET_TYPE_CREDIT_ALPHANUM4 / ALPHANUM12 validity.
func validateClassicAssetCode(code string) error {
	l := len(code)
	if l == 0 || l > 12 {
		return fmt.Errorf("%w: code %q length %d (expected 1-12)",
			ErrInvalidAsset, code, l)
	}
	for i := 0; i < l; i++ {
		c := code[i]
		ok := (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9')
		if !ok {
			return fmt.Errorf("%w: code %q contains non-alphanumeric byte %q",
				ErrInvalidAsset, code, c)
		}
	}
	return nil
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
	case AssetFiat:
		return "fiat:" + a.Code
	case AssetCrypto:
		return "crypto:" + a.Code
	case AssetRWA:
		return "rwa:" + a.Code
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
func (a Asset) Validate() error { //nolint:gocognit,gocyclo // dispatch-heavy; one case per AssetType, splitting would reduce linearity
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
		if err := validateClassicAssetCode(a.Code); err != nil {
			return err
		}
		return validateAccountID(a.Issuer)
	case AssetSoroban:
		if a.Code != "" || a.Issuer != "" {
			return fmt.Errorf("%w: soroban asset must not carry code/issuer", ErrInvalidAsset)
		}
		return validateContractID(a.ContractID)
	case AssetFiat:
		if a.Issuer != "" || a.ContractID != "" {
			return fmt.Errorf("%w: fiat asset must not carry issuer/contract_id", ErrInvalidAsset)
		}
		if !IsKnownFiat(a.Code) {
			return fmt.Errorf("%w: unknown fiat code %q (see ADR-0010)", ErrInvalidAsset, a.Code)
		}
		return nil
	case AssetCrypto:
		if a.Issuer != "" || a.ContractID != "" {
			return fmt.Errorf("%w: crypto asset must not carry issuer/contract_id", ErrInvalidAsset)
		}
		if !IsKnownCrypto(a.Code) {
			return fmt.Errorf("%w: unknown crypto code %q (see ADR-0014)", ErrInvalidAsset, a.Code)
		}
		return nil
	case AssetRWA:
		if a.Issuer != "" || a.ContractID != "" {
			return fmt.Errorf("%w: rwa asset must not carry issuer/contract_id", ErrInvalidAsset)
		}
		if !IsKnownRWA(a.Code) {
			return fmt.Errorf("%w: unknown rwa code %q (see ADR-0028)", ErrInvalidAsset, a.Code)
		}
		return nil
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
	// Shorthand for native (F-0024). CG/CMC users type "XLM" and
	// expect the native Stellar asset. Accept case-insensitive XLM
	// + any case variant of "native" as aliases. Broader slug ->
	// asset_id resolution (USDC -> Circle, AQUA -> AQUA-G... etc)
	// lives at the API layer via the verified-currency catalogue,
	// not here — keeping ParseAsset pure of catalogue state.
	if strings.EqualFold(s, "XLM") || strings.EqualFold(s, "native") {
		return NativeAsset(), nil
	}

	// Fiat — unambiguous prefix dispatch (ADR-0010).
	if rest, ok := strings.CutPrefix(s, "fiat:"); ok {
		return NewFiatAsset(rest)
	}

	// Crypto — unambiguous prefix dispatch (ADR-0014).
	if rest, ok := strings.CutPrefix(s, "crypto:"); ok {
		return NewCryptoAsset(rest)
	}

	// RWA — unambiguous prefix dispatch (ADR-0028).
	if rest, ok := strings.CutPrefix(s, "rwa:"); ok {
		return NewRWAAsset(rest)
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
		return fmt.Errorf("%w: must be string or object: %w", ErrInvalidAsset, err)
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
