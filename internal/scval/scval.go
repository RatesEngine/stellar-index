// Package scval wraps github.com/stellar/go-stellar-sdk/xdr with the
// narrow set of SCVal primitives the source connectors need: parse a
// base64-encoded SCVal, typed accessors (symbol / u64 / i128 / vec /
// map / address), and a Map-by-field-name lookup that enforces the
// "decode-by-name-not-position" rule from
// docs/architecture/contract-schema-evolution.md.
//
// This is the only package in the tree allowed to import
// .../go-stellar-sdk/xdr directly. Connectors go through the helpers
// below so a future SDK swap (or a hand-rolled replacement) changes
// one package, not twenty.
//
// Errors in this package wrap a package-local sentinel; callers can
// errors.Is against [ErrScValType] / [ErrScValDecode] to distinguish
// "wrong XDR shape" (bug or malicious input) from "base64 decode
// failed" (truncation / corruption).
package scval

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// Re-exports — connectors work with these aliases so they never
// need to import github.com/stellar/go-stellar-sdk/xdr directly.
// ADR-0013 §2 explicitly scopes the xdr import to this package.
type (
	ScVal      = xdr.ScVal
	ScMapEntry = xdr.ScMapEntry
)

// Sentinel errors. Wrap (do not replace) when adding context.
var (
	// ErrScValDecode wraps an XDR unmarshal failure — truncation,
	// invalid wire encoding, base64 decode. Distinct from ErrScValType
	// because this class usually indicates upstream corruption, not a
	// schema mismatch.
	ErrScValDecode = errors.New("scval: decode failed")

	// ErrScValType wraps a "wrong SCVal kind" assertion — e.g. caller
	// expected Symbol but got I128. Usually indicates a schema change
	// in the target contract (per docs/architecture/contract-schema-
	// evolution.md) or a decoder writing to the wrong shape.
	ErrScValType = errors.New("scval: unexpected SCVal type")

	// ErrScValMissingKey — a map-lookup by field name found no entry.
	// Distinct from "found but wrong type" so callers can gate on
	// schema evolution (old WASM emits field X, new WASM omits it).
	ErrScValMissingKey = errors.New("scval: map missing expected field")
)

// Parse base64-decodes data and XDR-unmarshals it into an ScVal.
// Returns [ErrScValDecode]-wrapped errors on any failure so callers
// can distinguish wire-level problems from schema problems.
func Parse(b64 string) (xdr.ScVal, error) {
	var sv xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(b64, &sv); err != nil {
		return xdr.ScVal{}, fmt.Errorf("%w: %w", ErrScValDecode, err)
	}
	return sv, nil
}

// ParseBytes is the raw-bytes twin of [Parse] — XDR-unmarshals a
// pre-decoded byte slice into an ScVal. Used by decoders whose
// event body arrives as an ScVal::Bytes wrapping an XDR-encoded
// struct (see redstone.sdkDecodeBody); they call [AsBytes] first,
// then pass the raw bytes through here to get the inner ScVal.
func ParseBytes(raw []byte) (xdr.ScVal, error) {
	var sv xdr.ScVal
	if err := sv.UnmarshalBinary(raw); err != nil {
		return xdr.ScVal{}, fmt.Errorf("%w: %w", ErrScValDecode, err)
	}
	return sv, nil
}

// EncodeArgsAsScVec wraps a slice of base64-encoded ScVal blobs
// (the wire format used by [events.Event.OpArgs]) into a single
// XDR-marshalled ScVal::Vec blob.
//
// Returns nil + nil error for an empty input (callers can map nil
// to SQL NULL at the storage boundary). Returns a non-nil error
// when any of the input ScVals fails to parse — the source-of-truth
// for InvokeContract args is the dispatcher's own marshalling, so
// a parse failure here indicates a wire corruption.
//
// Powers the soroban_events landing zone (ADR-0029); the sink
// stores op_args as the marshalled ScVec bytes so downstream
// decoders can use the standard [scval.Parse] / [AsVec] pathway.
func EncodeArgsAsScVec(b64Args []string) ([]byte, error) {
	if len(b64Args) == 0 {
		return nil, nil
	}
	vec := make(xdr.ScVec, 0, len(b64Args))
	for i, a := range b64Args {
		sv, err := Parse(a)
		if err != nil {
			return nil, fmt.Errorf("arg[%d]: %w", i, err)
		}
		vec = append(vec, sv)
	}
	pp := &vec
	sv := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pp}
	return sv.MarshalBinary()
}

// DecodeScVecToArgs is the inverse of [EncodeArgsAsScVec]: it
// takes the XDR-marshalled bytes of a ScVal::Vec (as stored in the
// `soroban_events.op_args_xdr` column) and returns the slice of
// base64-encoded ScVal blobs that [events.Event.OpArgs] expects.
//
// Returns (nil, nil) for empty input (the stored column was NULL).
// Returns an error when the bytes don't unmarshal to a ScVal::Vec
// — the schema commits to that exact shape (see EncodeArgsAsScVec)
// so a mismatch indicates a corrupt row, not a decoder concern.
//
// Used by `ratesengine-ops projector-replay` (and the lake-replay
// rebuild paths) that reconstruct events.Event values from
// soroban_events rows and feed them back through the live decoders.
// (The per-source `<source>-backfill` subcommands this once named
// were deleted in ADR-0032 Phase 5.)
func DecodeScVecToArgs(b []byte) ([]string, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var sv xdr.ScVal
	if err := sv.UnmarshalBinary(b); err != nil {
		return nil, fmt.Errorf("unmarshal scval: %w", err)
	}
	if sv.Type != xdr.ScValTypeScvVec {
		return nil, fmt.Errorf("decode: expected ScVal::Vec, got %v", sv.Type)
	}
	if sv.Vec == nil || *sv.Vec == nil {
		return nil, nil
	}
	vec := **sv.Vec
	out := make([]string, 0, len(vec))
	for _, elem := range vec {
		blob, err := elem.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("marshal elem: %w", err)
		}
		out = append(out, base64.StdEncoding.EncodeToString(blob))
	}
	return out, nil
}

// MustEncodeSymbol returns the base64-encoded SCVal::Symbol(s) blob
// used for topic matching (both in stellar-rpc getEvents filters and
// for byte-comparison against Event.Topic entries).
//
// Panics on invalid inputs (empty string, non-ASCII, longer than
// SorobanObjectSizeLimit). Only call with compile-time constants —
// never with network-received data. The name "Must…" is a Go
// convention flag for "panic on programmer error." Validated against
// xdr.ScSymbol's upstream bounds.
func MustEncodeSymbol(s string) string {
	b64, err := EncodeSymbol(s)
	if err != nil {
		panic("scval.MustEncodeSymbol: " + err.Error())
	}
	return b64
}

// MustEncodeString returns the base64-encoded SCVal::String(s) blob.
// Some Soroban contracts emit topic[0] as a String (not Symbol) —
// e.g. Soroswap's `("SoroswapPair", symbol_short!("swap"))` where
// the first element is a string literal. SCVal::String has wider
// character-set support than SCVal::Symbol (no identifier-only
// restriction) and no length cap beyond SorobanObjectSizeLimit.
//
// Panics on marshal error — only call with compile-time constants.
func MustEncodeString(s string) string {
	b64, err := EncodeString(s)
	if err != nil {
		panic("scval.MustEncodeString: " + err.Error())
	}
	return b64
}

// EncodeString is the non-panicking form of [MustEncodeString].
func EncodeString(s string) (string, error) {
	sv := xdr.ScVal{Type: xdr.ScValTypeScvString}
	str := xdr.ScString(s)
	sv.Str = &str
	b, err := sv.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrScValDecode, err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// AsString returns the String value of sv, wrapping ErrScValType if
// sv is not a String. Analogous to AsSymbol, kept distinct so
// callers can't accidentally confuse the two types — they look
// similar on-wire but have different semantics (Symbols are
// identifier-constrained; Strings are arbitrary bytes).
func AsString(sv xdr.ScVal) (string, error) {
	if sv.Type != xdr.ScValTypeScvString {
		return "", fmt.Errorf("%w: want String, got %s", ErrScValType, sv.Type.String())
	}
	return string(*sv.Str), nil
}

// EncodeSymbol is the non-panicking form of [MustEncodeSymbol].
// Used by tests and by any future code that needs to encode a
// runtime-supplied symbol.
func EncodeSymbol(s string) (string, error) {
	// xdr.ScSymbol is a []byte alias. The contract-event macro's
	// topic slot uses the same Symbol scheme as all SCVals. Bounds
	// (1..=32 bytes, ASCII alphanumeric + underscore) are enforced
	// by the runtime; mirror that so we fail fast at encode time.
	if len(s) == 0 || len(s) > 32 {
		return "", fmt.Errorf("scval: symbol length %d out of range [1,32]", len(s))
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_'
		if !ok {
			return "", fmt.Errorf("scval: symbol contains non-ASCII-identifier byte 0x%02x", c)
		}
	}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol}
	sym := xdr.ScSymbol(s)
	sv.Sym = &sym
	b, err := sv.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrScValDecode, err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// AsSymbol returns the Symbol string from sv, wrapping ErrScValType
// if sv is not a Symbol. Use this to read symbol-typed topic/body
// fields; match against compile-time constants via MustEncodeSymbol
// for byte-level equality on the wire.
func AsSymbol(sv xdr.ScVal) (string, error) {
	if sv.Type != xdr.ScValTypeScvSymbol {
		return "", fmt.Errorf("%w: want Symbol, got %s", ErrScValType, sv.Type.String())
	}
	return string(*sv.Sym), nil
}

// AsU64 returns the uint64 value from sv. Timestamps in Soroban
// events (e.g. Reflector's topic[2]) are `u64`, NOT `Timepoint`, so
// this is the correct accessor — not MustTimepoint.
func AsU64(sv xdr.ScVal) (uint64, error) {
	if sv.Type != xdr.ScValTypeScvU64 {
		return 0, fmt.Errorf("%w: want U64, got %s", ErrScValType, sv.Type.String())
	}
	return uint64(*sv.U64), nil
}

// AsU32 returns the uint32 value from sv. Used for view-function
// returns like Soroswap factory all_pairs_length() -> u32.
func AsU32(sv xdr.ScVal) (uint32, error) {
	if sv.Type != xdr.ScValTypeScvU32 {
		return 0, fmt.Errorf("%w: want U32, got %s", ErrScValType, sv.Type.String())
	}
	return uint32(*sv.U32), nil
}

// AsBytes returns the raw []byte value from sv. Used by decoders
// whose event body is a Bytes-wrapped XDR-encoded struct (e.g.
// Redstone: the adapter's Rust contract does `self.to_xdr(env).to_val()`
// which wraps the serialized struct in ScVal::Bytes rather than
// emitting the struct directly as a Map).
func AsBytes(sv xdr.ScVal) ([]byte, error) {
	if sv.Type != xdr.ScValTypeScvBytes {
		return nil, fmt.Errorf("%w: want Bytes, got %s", ErrScValType, sv.Type.String())
	}
	return []byte(*sv.Bytes), nil
}

// AsBool returns the bool value from sv, wrapping ErrScValType if sv
// is not a Bool. Used by decoders reading Soroban #[contracttype]
// structs whose fields include a `bool` (e.g. Blend's ReserveConfig
// `enabled` flag).
func AsBool(sv xdr.ScVal) (bool, error) {
	if sv.Type != xdr.ScValTypeScvBool {
		return false, fmt.Errorf("%w: want Bool, got %s", ErrScValType, sv.Type.String())
	}
	return bool(*sv.B), nil
}

// NewU32 builds an ScVal wrapping a uint32, suitable for passing as
// a contract-invocation argument (e.g. factory.all_pairs(i: u32)).
// Sibling of the existing Encode* helpers but returns the ScVal
// directly rather than its base64 wire form — callers building
// InvokeContract args work with ScVals, not blobs.
func NewU32(v uint32) xdr.ScVal {
	u := xdr.Uint32(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u}
}

// AsAmountFromI128 converts sv's I128 parts to canonical.Amount.
// Preserves the full 128-bit signed range per ADR-0003 — the common
// failure we guard against is truncating to int64(parts.Lo), which
// drops the hi 64 bits and silently mis-reports large values.
func AsAmountFromI128(sv xdr.ScVal) (canonical.Amount, error) {
	if sv.Type != xdr.ScValTypeScvI128 {
		return canonical.Amount{}, fmt.Errorf("%w: want I128, got %s", ErrScValType, sv.Type.String())
	}
	p := *sv.I128
	return canonical.FromInt128Parts(int64(p.Hi), uint64(p.Lo)), nil
}

// AsAmountFromU128 is the unsigned twin of AsAmountFromI128. Soroban
// amounts are usually i128; reserves and supplies sometimes come as
// u128. Caller picks the right accessor based on contract schema.
func AsAmountFromU128(sv xdr.ScVal) (canonical.Amount, error) {
	if sv.Type != xdr.ScValTypeScvU128 {
		return canonical.Amount{}, fmt.Errorf("%w: want U128, got %s", ErrScValType, sv.Type.String())
	}
	p := *sv.U128
	return canonical.FromUInt128Parts(uint64(p.Hi), uint64(p.Lo)), nil
}

// AsAmountFromU256 decodes a full 256-bit unsigned Soroban value.
// Required by Redstone's PriceData.price (common/src/lib.rs:15);
// most other connectors stop at u128. The four 64-bit words compose
// big-endian (HiHi = most significant).
func AsAmountFromU256(sv xdr.ScVal) (canonical.Amount, error) {
	if sv.Type != xdr.ScValTypeScvU256 {
		return canonical.Amount{}, fmt.Errorf("%w: want U256, got %s", ErrScValType, sv.Type.String())
	}
	p := *sv.U256
	return canonical.FromUInt256Parts(
		uint64(p.HiHi), uint64(p.HiLo),
		uint64(p.LoHi), uint64(p.LoLo),
	), nil
}

// AsAddressStrkey returns the strkey-encoded form of an
// ScVal::Address (G… / C… / M… / B… / L…). Delegates checksum +
// format to the SDK's strkey package — no shortcuts like "first
// char is G so it's an account"; this is the path that catches a
// malformed address before it reaches the database.
//
// CAP-67 / Protocol 23 (mainnet 2025-09-03) extended ScAddress
// from the original two variants (Account, Contract) to five:
// added Muxed Account, Claimable Balance, and Liquidity Pool. A
// SEP-41 transfer event's `to` field can carry any of these —
// the cascade-window drain dry-run on 2026-05-28 found that every
// transfer event hitting a liquidity-pool destination tripped
// `unknown ScAddress type 4` and was silently dropped.
//
// Strkey payload shapes are pinned by the SDK's strkey/decode_test.go
// table:
//   - Muxed (M-…): 40 bytes = 32-byte ed25519 || 8-byte big-endian id
//   - Claimable Balance (B-…): 33 bytes = 1-byte type prefix (0x00 = V0)
//     || 32-byte hash
//   - Liquidity Pool (L-…): 32 bytes = PoolId hash directly
func AsAddressStrkey(sv xdr.ScVal) (string, error) {
	if sv.Type != xdr.ScValTypeScvAddress {
		return "", fmt.Errorf("%w: want Address, got %s", ErrScValType, sv.Type.String())
	}
	addr := *sv.Address
	switch addr.Type {
	case xdr.ScAddressTypeScAddressTypeAccount:
		raw := addr.MustAccountId().Ed25519
		return strkey.Encode(strkey.VersionByteAccountID, raw[:])
	case xdr.ScAddressTypeScAddressTypeContract:
		raw := addr.MustContractId()
		return strkey.Encode(strkey.VersionByteContract, raw[:])
	case xdr.ScAddressTypeScAddressTypeMuxedAccount:
		m := addr.MustMuxedAccount()
		raw := make([]byte, 40)
		copy(raw, m.Ed25519[:])
		binary.BigEndian.PutUint64(raw[32:], uint64(m.Id))
		return strkey.Encode(strkey.VersionByteMuxedAccount, raw)
	case xdr.ScAddressTypeScAddressTypeClaimableBalance:
		cb := addr.MustClaimableBalanceId()
		if cb.V0 == nil {
			return "", fmt.Errorf("%w: claimable balance V0 hash is nil", ErrScValType)
		}
		raw := make([]byte, 33)
		raw[0] = byte(cb.Type) // V0 = 0
		copy(raw[1:], cb.V0[:])
		return strkey.Encode(strkey.VersionByteClaimableBalance, raw)
	case xdr.ScAddressTypeScAddressTypeLiquidityPool:
		lp := addr.MustLiquidityPoolId()
		return strkey.Encode(strkey.VersionByteLiquidityPool, lp[:])
	default:
		return "", fmt.Errorf("%w: unknown ScAddress type %d", ErrScValType, addr.Type)
	}
}

// AsVec returns the elements of an ScVal::Vec. A nil Vec (empty but
// present) returns an empty slice, not nil — the distinction matters
// in Go range-over-nil-slice, and we want every caller to see the
// same empty-vs-present shape without a nil-check.
func AsVec(sv xdr.ScVal) ([]xdr.ScVal, error) {
	if sv.Type != xdr.ScValTypeScvVec {
		return nil, fmt.Errorf("%w: want Vec, got %s", ErrScValType, sv.Type.String())
	}
	vec := *sv.Vec
	if vec == nil {
		return []xdr.ScVal{}, nil
	}
	return []xdr.ScVal(*vec), nil
}

// AsMap returns the entries of an ScVal::Map. Like AsVec, a present-
// but-nil map yields an empty slice.
func AsMap(sv xdr.ScVal) ([]xdr.ScMapEntry, error) {
	if sv.Type != xdr.ScValTypeScvMap {
		return nil, fmt.Errorf("%w: want Map, got %s", ErrScValType, sv.Type.String())
	}
	m := *sv.Map
	if m == nil {
		return []xdr.ScMapEntry{}, nil
	}
	return []xdr.ScMapEntry(*m), nil
}

// MapField looks up a map entry whose key is Symbol(key). Returns
// the value and true on hit, zero and false on miss. Use
// [MustMapField] at sites where a miss is a schema violation that
// should surface ErrScValMissingKey.
//
// This is the canonical "decode by field name, not by position"
// entry point. Per docs/architecture/contract-schema-evolution.md,
// new contract versions may add, reorder, or remove fields; lookup
// by symbolic key makes decoders resilient to all three.
func MapField(entries []xdr.ScMapEntry, key string) (xdr.ScVal, bool) {
	for i := range entries {
		if entries[i].Key.Type != xdr.ScValTypeScvSymbol {
			continue
		}
		if string(*entries[i].Key.Sym) == key {
			return entries[i].Val, true
		}
	}
	return xdr.ScVal{}, false
}

// MustMapField is MapField with a strict miss. Returns
// ErrScValMissingKey-wrapped error if the key is absent. Use at
// sites where absence is a schema violation, not optional data.
func MustMapField(entries []xdr.ScMapEntry, key string) (xdr.ScVal, error) {
	v, ok := MapField(entries, key)
	if !ok {
		return xdr.ScVal{}, fmt.Errorf("%w: %q", ErrScValMissingKey, key)
	}
	return v, nil
}

// AsTupleN asserts sv is a Vec of exactly n elements and returns
// them. Soroban's "tuple" runtime representation is Vec — a 2-tuple
// (a, b) is Vec[a, b], a 3-tuple is Vec[a, b, c], etc. Callers
// that decode Reflector's Vec<(Val, i128)> body use AsTupleN(2).
func AsTupleN(sv xdr.ScVal, n int) ([]xdr.ScVal, error) {
	elts, err := AsVec(sv)
	if err != nil {
		return nil, err
	}
	if len(elts) != n {
		return nil, fmt.Errorf("%w: want %d-tuple (Vec), got Vec of length %d", ErrScValType, n, len(elts))
	}
	return elts, nil
}

// AddressOrSymbol is the outcome of DecodeAddressOrSymbol — exactly
// one of .Address (strkey) or .Symbol is non-empty. Used by Reflector
// (Asset::Stellar | Asset::Other variant) and anywhere else a
// contract emits a union over these two runtime forms.
type AddressOrSymbol struct {
	Address string // non-empty when sv is ScAddress (strkey encoded)
	Symbol  string // non-empty when sv is ScSymbol
}

// DecodeAddressOrSymbol dispatches on sv's kind: returns
// {Address: G|C…} for ScAddress, {Symbol: "…"} for ScSymbol,
// and ErrScValType for anything else.
//
// Lets connectors consume union-of-Address-or-Symbol shapes (common
// in Soroban oracle payloads) without importing xdr. Keeps the
// "scval is the single xdr-boundary package" rule from ADR-0013.
func DecodeAddressOrSymbol(sv xdr.ScVal) (AddressOrSymbol, error) {
	switch sv.Type {
	case xdr.ScValTypeScvAddress:
		addr, err := AsAddressStrkey(sv)
		if err != nil {
			return AddressOrSymbol{}, err
		}
		return AddressOrSymbol{Address: addr}, nil
	case xdr.ScValTypeScvSymbol:
		sym, err := AsSymbol(sv)
		if err != nil {
			return AddressOrSymbol{}, err
		}
		return AddressOrSymbol{Symbol: sym}, nil
	default:
		return AddressOrSymbol{}, fmt.Errorf("%w: want Address or Symbol, got %s",
			ErrScValType, sv.Type.String())
	}
}
