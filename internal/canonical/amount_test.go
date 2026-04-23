package canonical

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"
)

// TestAmount_FromInt128Parts_KALIENIncident is the canonical
// regression test for ADR-0003 (i128 no-truncation invariant).
//
// The KALIEN balance in the production incident the ADR references
// was 40,000,005,972,900,000,000 — well above i64 max of ~9.22e18.
// It was stored with hi=2, lo=3106517825480896768. A naive
// low-word-only decoder shows 310,651,782,548.0896768 (wrong by
// orders of magnitude).
//
// This test asserts the correct 128-bit reconstruction. If it
// ever fails, our core amount handling is broken and a SEV-1 is
// warranted.
func TestAmount_FromInt128Parts_KALIENIncident(t *testing.T) {
	t.Parallel()

	const (
		hi             int64  = 2
		lo             uint64 = 3106517825480896768
		expectedString        = "40000005972900000000"
	)

	got := FromInt128Parts(hi, lo)
	if got.String() != expectedString {
		t.Fatalf("KALIEN incident regression: got %q, want %q", got.String(), expectedString)
	}

	// Cross-check via BigInt.
	want, ok := new(big.Int).SetString(expectedString, 10)
	if !ok {
		t.Fatal("setup: expected string did not parse as big.Int")
	}
	if got.BigInt().Cmp(want) != 0 {
		t.Fatalf("BigInt compare: got %s, want %s", got.BigInt(), want)
	}
}

// TestAmount_FromInt128Parts covers representative corner cases of
// the hi/lo → big.Int reconstruction.
func TestAmount_FromInt128Parts(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		hi   int64
		lo   uint64
		want string
	}{
		"zero": {
			hi: 0, lo: 0, want: "0",
		},
		"positive_small": {
			hi: 0, lo: 42, want: "42",
		},
		"i64_max_exactly": {
			hi: 0, lo: math.MaxInt64, want: "9223372036854775807",
		},
		"one_above_i64_max": {
			hi: 0, lo: uint64(math.MaxInt64) + 1, want: "9223372036854775808",
		},
		"kalien_incident_amount": {
			hi: 2, lo: 3106517825480896768, want: "40000005972900000000",
		},
		"negative_small": {
			hi: -1, lo: math.MaxUint64 - 0, want: "-1",
		},
		"negative_large": {
			hi: -1, lo: math.MaxUint64 - 41, want: "-42",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := FromInt128Parts(tc.hi, tc.lo).String()
			if got != tc.want {
				t.Fatalf("hi=%d lo=%d: got %q, want %q", tc.hi, tc.lo, got, tc.want)
			}
		})
	}
}

// TestAmount_FromUInt128Parts verifies the unsigned path.
func TestAmount_FromUInt128Parts(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		hi   uint64
		lo   uint64
		want string
	}{
		"zero": {
			hi: 0, lo: 0, want: "0",
		},
		"u64_max_in_low_word": {
			hi: 0, lo: math.MaxUint64, want: "18446744073709551615",
		},
		"one_above_u64_max": {
			hi: 1, lo: 0, want: "18446744073709551616",
		},
		"u128_max": {
			hi: math.MaxUint64, lo: math.MaxUint64,
			want: "340282366920938463463374607431768211455",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := FromUInt128Parts(tc.hi, tc.lo).String()
			if got != tc.want {
				t.Fatalf("hi=%d lo=%d: got %q, want %q", tc.hi, tc.lo, got, tc.want)
			}
		})
	}
}

// TestAmount_JSONRoundTrip asserts that Amount always serialises
// to a JSON string (ADR-0003) and deserialises losslessly —
// including values far beyond JSON-number precision.
func TestAmount_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	values := []string{
		"0",
		"1",
		"9223372036854775807",  // i64 max
		"9223372036854775808",  // i64 max + 1 (traditional overflow boundary)
		"18446744073709551616", // u64 max + 1
		"40000005972900000000", // KALIEN incident
		"340282366920938463463374607431768211455", // u128 max
		"-1",
		"-9223372036854775808", // i64 min
	}

	for _, s := range values {
		t.Run(s, func(t *testing.T) {
			original, err := FromString(s)
			if err != nil {
				t.Fatalf("FromString(%q): %v", s, err)
			}

			// Marshal — must be a JSON string.
			b, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if !strings.HasPrefix(string(b), `"`) {
				t.Fatalf("Amount marshalled to non-string JSON: %s (ADR-0003 violation)", b)
			}

			// Unmarshal — must round-trip losslessly.
			var round Amount
			if err := json.Unmarshal(b, &round); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if round.Cmp(original) != 0 {
				t.Fatalf("round-trip: got %q, want %q", round, original)
			}
		})
	}
}

// TestAmount_UnmarshalJSONNumber accepts the lax JSON-number form
// (for compatibility with producers that don't follow our string
// convention yet) and still round-trips correctly for integer
// values that fit in JSON's lossless integer range.
func TestAmount_UnmarshalJSONNumber(t *testing.T) {
	t.Parallel()

	// Only test values that fit in JSON's lossless integer range
	// (i.e. the accepted but-not-recommended path).
	const raw = `123456789`
	var got Amount
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.String() != raw {
		t.Fatalf("Number form: got %q, want %q", got, raw)
	}
}

// TestAmount_SQLRoundTrip verifies the database/sql Valuer + Scanner
// round-trip via both string and []byte paths (Postgres NUMERIC
// drivers use both historically).
func TestAmount_FromStringRejectsOversize(t *testing.T) {
	// DoS guard: a multi-megabyte decimal string must be rejected
	// at parse time rather than triggering a giant big.Int alloc.
	// Any input past MaxAmountStringLen is refused.
	t.Parallel()
	oversize := strings.Repeat("9", MaxAmountStringLen+1)
	_, err := FromString(oversize)
	if err == nil {
		t.Fatal("oversize decimal string must be rejected")
	}
	if !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("err should wrap ErrInvalidAmount, got %v", err)
	}
	// Exactly at the cap still parses.
	atCap := strings.Repeat("9", MaxAmountStringLen)
	if _, err := FromString(atCap); err != nil {
		t.Errorf("at-cap input (len=%d) must parse, got %v", MaxAmountStringLen, err)
	}
}

func TestAmount_UnmarshalJSONRejectsOversize(t *testing.T) {
	// JSON path also enforces the cap (inherited via FromString).
	// Covers untrusted-input entry points like future POST bodies.
	t.Parallel()
	oversize := `"` + strings.Repeat("9", MaxAmountStringLen+1) + `"`
	var a Amount
	if err := a.UnmarshalJSON([]byte(oversize)); err == nil {
		t.Fatal("oversize JSON string must be rejected")
	}
}

func TestAmount_SQLRoundTrip(t *testing.T) {
	t.Parallel()

	original, err := FromString("40000005972900000000")
	if err != nil {
		t.Fatal(err)
	}

	// Valuer must emit a string.
	v, err := original.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("Value returned %T, want string", v)
	}

	// Scanner via string.
	var viaString Amount
	if err := viaString.Scan(s); err != nil {
		t.Fatalf("Scan(string): %v", err)
	}
	if viaString.Cmp(original) != 0 {
		t.Fatalf("Scan(string) round-trip: got %q, want %q", viaString, original)
	}

	// Scanner via []byte (common Postgres driver path).
	var viaBytes Amount
	if err := viaBytes.Scan([]byte(s)); err != nil {
		t.Fatalf("Scan([]byte): %v", err)
	}
	if viaBytes.Cmp(original) != 0 {
		t.Fatalf("Scan([]byte) round-trip: got %q, want %q", viaBytes, original)
	}
}

func TestAmount_ScanNullProducesZero(t *testing.T) {
	// Every trade/oracle column storing an Amount is NOT NULL per
	// migrations/0001 + 0003, so nil-src Scan shouldn't fire in
	// production. Keep the defensive path test-pinned so a refactor
	// that removes it — or changes nil → error — surfaces in CI.
	var a Amount
	if err := a.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if !a.IsZero() {
		t.Errorf("Scan(nil) must produce zero Amount, got %q", a.String())
	}
	// And the subsequent Value() returns "0" (never nil), so a
	// round-tripped NULL lands in a NOT-NULL column safely.
	v, err := a.Value()
	if err != nil {
		t.Fatalf("Value after Scan(nil): %v", err)
	}
	if v != "0" {
		t.Errorf("Value after Scan(nil) = %v, want \"0\"", v)
	}
}

func TestAmount_ScanRejectsUnsupportedTypes(t *testing.T) {
	// Defensive: time.Time, float64, bool, etc. are not NUMERIC-
	// compatible and must surface as errors rather than silently
	// coerce to zero or panic.
	var a Amount
	for _, src := range []any{
		3.14,
		true,
		struct{}{},
	} {
		if err := a.Scan(src); err == nil {
			t.Errorf("Scan(%T) should error, got nil", src)
		}
	}
}
