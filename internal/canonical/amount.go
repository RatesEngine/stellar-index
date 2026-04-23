package canonical

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"math/big"
)

// Amount is a precision-safe wrapper over *big.Int for every token
// quantity, reserve, price numerator, and supply value we handle.
//
// i128 invariant (ADR-0003): every value in flight from a Soroban
// contract event through our pipeline to the API response is
// preserved with full 128-bit (or wider) precision. Never int64,
// never float64.
//
// Zero value is a valid zero Amount (big.Int's zero value is 0).
// Callers MUST NOT mutate the underlying *big.Int — create a new
// Amount for any arithmetic result.
//
// JSON wire form is ALWAYS a decimal string, never a JSON number —
// JSON numbers are IEEE 754 doubles (53-bit precision) and silently
// lose precision above 2^53. See [Amount.MarshalJSON].
//
// Parse-side DoS guard: [FromString] rejects inputs longer than
// [MaxAmountStringLen] digits (512 — far past any legitimate i128
// or Postgres NUMERIC value). Prevents a multi-MB decimal string
// from an untrusted source (future POST body, malformed JSON)
// triggering an unbounded big.Int allocation.
type Amount struct {
	// value is never nil after a successful construction. Helpers
	// below guard against nil by constructing on demand.
	value *big.Int
}

// NewAmount wraps any value that can be expressed as a *big.Int.
//
// For typical Soroban decoders, prefer [FromInt128Parts] /
// [FromUInt128Parts] which handle the hi/lo split + two's-complement
// sign correctly.
func NewAmount(v *big.Int) Amount {
	if v == nil {
		return Amount{value: new(big.Int)}
	}
	// Copy to prevent shared mutation of the caller's *big.Int.
	return Amount{value: new(big.Int).Set(v)}
}

// FromInt128Parts reconstructs a signed 128-bit integer from its
// Soroban-XDR hi/lo representation. hi is the signed high word;
// lo is the unsigned low word.
//
// This is the reference path for any i128 reaching us from a
// Soroban event or contract read. Correctness-critical — backed
// by regression fixtures in amount_test.go including the
// KALIEN-incident case documented in ADR-0003.
func FromInt128Parts(hi int64, lo uint64) Amount {
	// hi contributes the top 64 bits (signed); lo the bottom 64
	// bits (unsigned). Compose via big.Int arithmetic rather than
	// bit-shifting to keep two's-complement sign propagation
	// correct.
	h := big.NewInt(hi)
	h.Lsh(h, 64)
	l := new(big.Int).SetUint64(lo)
	return Amount{value: new(big.Int).Add(h, l)}
}

// FromUInt128Parts reconstructs an unsigned 128-bit integer from
// its Soroban-XDR hi/lo representation. Both words are unsigned.
func FromUInt128Parts(hi, lo uint64) Amount {
	h := new(big.Int).SetUint64(hi)
	h.Lsh(h, 64)
	l := new(big.Int).SetUint64(lo)
	return Amount{value: new(big.Int).Add(h, l)}
}

// MaxAmountStringLen caps the decimal-digit budget accepted by
// [FromString] / [Amount.UnmarshalJSON] / [Amount.Scan]. 512 digits
// is comfortably past any legitimate Soroban i128 / Postgres
// NUMERIC value (~39 / ~131072 digits respectively) but forecloses
// the DoS path of feeding us a multi-megabyte decimal string and
// watching big.Int allocate. Internal call sites are trusted (XDR
// decode, DB scan) but JSON UnmarshalJSON flows through untrusted
// input on future POST endpoints — the cap lives here so every
// entry point gets it for free.
const MaxAmountStringLen = 512

// FromString parses a decimal string (no scientific notation, no
// thousands separators). Empty string returns a zero Amount.
// Rejects inputs longer than [MaxAmountStringLen] digits as a DoS
// guard — far past any real i128 value.
func FromString(s string) (Amount, error) {
	if s == "" {
		return Amount{value: new(big.Int)}, nil
	}
	if len(s) > MaxAmountStringLen {
		return Amount{}, fmt.Errorf("canonical: amount string length %d exceeds cap %d: %w",
			len(s), MaxAmountStringLen, ErrInvalidAmount)
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return Amount{}, fmt.Errorf("canonical: %q is not a valid decimal integer: %w", s, ErrInvalidAmount)
	}
	return Amount{value: v}, nil
}

// BigInt returns a copy of the underlying *big.Int. Safe for
// caller mutation.
func (a Amount) BigInt() *big.Int {
	if a.value == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(a.value)
}

// String returns the decimal representation. This is the
// wire format: our JSON responses always serialise Amount as a
// JSON string for i128 safety (see ADR-0003).
func (a Amount) String() string {
	if a.value == nil {
		return "0"
	}
	return a.value.String()
}

// IsZero reports whether the amount is zero.
func (a Amount) IsZero() bool {
	return a.value == nil || a.value.Sign() == 0
}

// Sign returns -1, 0, or +1 depending on the amount's sign.
func (a Amount) Sign() int {
	if a.value == nil {
		return 0
	}
	return a.value.Sign()
}

// Cmp compares a and b, returning -1 if a<b, 0 if equal, +1 if
// a>b. Matches (*big.Int).Cmp semantics.
func (a Amount) Cmp(b Amount) int {
	return a.BigInt().Cmp(b.BigInt())
}

// ─── JSON ─────────────────────────────────────────────────────────

// MarshalJSON serialises as a JSON string, never a JSON number.
// JSON numbers are IEEE 754 doubles (53-bit precision); anything
// larger than 2^53 would silently lose precision in the client
// before ever being observed. ADR-0003.
func (a Amount) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

// UnmarshalJSON accepts either a JSON string or a JSON number (for
// small amounts emitted by lax producers). String form is preferred.
func (a *Amount) UnmarshalJSON(b []byte) error {
	// Try string first.
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := FromString(s)
		if err != nil {
			return err
		}
		*a = v
		return nil
	}
	// Fall back to json.Number (lossless for integer-valued
	// numerics).
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("canonical: Amount must be a JSON string or number: %w", ErrInvalidAmount)
	}
	v, err := FromString(n.String())
	if err != nil {
		return err
	}
	*a = v
	return nil
}

// ─── database/sql ────────────────────────────────────────────────

// Value implements driver.Valuer. Postgres NUMERIC columns accept
// arbitrary-precision decimal strings; we hand them exactly that.
func (a Amount) Value() (driver.Value, error) {
	return a.String(), nil
}

// Scan implements sql.Scanner. Accepts the string form Postgres
// NUMERIC returns, or []byte equivalent.
func (a *Amount) Scan(src any) error {
	if src == nil {
		*a = Amount{value: new(big.Int)}
		return nil
	}
	switch v := src.(type) {
	case string:
		parsed, err := FromString(v)
		if err != nil {
			return err
		}
		*a = parsed
		return nil
	case []byte:
		parsed, err := FromString(string(v))
		if err != nil {
			return err
		}
		*a = parsed
		return nil
	case int64:
		*a = Amount{value: big.NewInt(v)}
		return nil
	default:
		return fmt.Errorf("canonical: cannot scan %T into Amount: %w", src, ErrInvalidAmount)
	}
}
