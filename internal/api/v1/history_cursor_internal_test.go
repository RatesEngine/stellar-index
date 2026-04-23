package v1

import (
	"testing"
	"time"
)

// TestHistoryCursorRoundTrip pins the timestamp-precision contract.
// Postgres TIMESTAMPTZ stores microsecond precision, but the encoder
// preserves nanoseconds so it stays correct if the storage layer
// ever upgrades. Pre-epoch and leap-day timestamps round-trip too.
func TestHistoryCursorRoundTrip(t *testing.T) {
	cases := map[string]historyCursor{
		"whole second": {
			ts:      time.Unix(1_772_000_000, 0).UTC(),
			ledger:  12345,
			source:  "soroswap",
			txHash:  "0000000000000000000000000000000000000000000000000000000000000001",
			opIndex: 0,
		},
		"microsecond precision": {
			ts:      time.Unix(1_772_000_000, 123_456_000).UTC(),
			ledger:  12345,
			source:  "aquarius",
			txHash:  "fadefadefadefadefadefadefadefadefadefadefadefadefadefadefadefade",
			opIndex: 7,
		},
		"nanosecond precision": {
			ts:      time.Unix(1_772_000_000, 123_456_789).UTC(),
			ledger:  math_MaxUint32,
			source:  "phoenix",
			txHash:  "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			opIndex: math_MaxUint32,
		},
		"pre-epoch": {
			ts:      time.Unix(-5_000_000, 0).UTC(),
			ledger:  1,
			source:  "reflector-dex",
			txHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			opIndex: 0,
		},
		"leap day utc": {
			// 2024-02-29 12:34:56.789012345 UTC — proves the encoder
			// doesn't assume any calendar convention; it's just
			// Unix nanos.
			ts:      time.Date(2024, 2, 29, 12, 34, 56, 789_012_345, time.UTC),
			ledger:  55_555,
			source:  "soroswap",
			txHash:  "1111111111111111111111111111111111111111111111111111111111111111",
			opIndex: 3,
		},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			enc := encodeHistoryCursor(want)
			got, err := decodeHistoryCursor(enc)
			if err != nil {
				t.Fatalf("decode %q: %v", enc, err)
			}
			if !got.ts.Equal(want.ts) {
				t.Errorf("ts mismatch: got %v, want %v", got.ts, want.ts)
			}
			if got.ts.UnixNano() != want.ts.UnixNano() {
				t.Errorf("ts unixnano mismatch: got %d, want %d",
					got.ts.UnixNano(), want.ts.UnixNano())
			}
			if got.ledger != want.ledger ||
				got.source != want.source ||
				got.txHash != want.txHash ||
				got.opIndex != want.opIndex {
				t.Errorf("non-ts fields mismatch:\n  got  %+v\n  want %+v", got, want)
			}
		})
	}
}

// math_MaxUint32 avoids importing math just for one constant in a
// table-driven test.
const math_MaxUint32 = uint32(1<<32 - 1)
