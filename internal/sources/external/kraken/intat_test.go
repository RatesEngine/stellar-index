package kraken

import "testing"

// krakenCandle is a positional []any. intAt at position i must:
//   - return ok=false when i is out of range
//   - parse a float64 (Kraken sometimes serialises timestamps that way)
//   - parse a string-of-digits (the more common form)
//   - return ok=false for any other type (avoids silently coercing
//     a bool/object into 0)

func TestKrakenCandle_intAt_outOfRange(t *testing.T) {
	c := krakenCandle{float64(1_745_000_000)}
	if _, ok := c.intAt(7); ok {
		t.Error("intAt(out-of-range) returned ok, want false")
	}
}

func TestKrakenCandle_intAt_float64(t *testing.T) {
	c := krakenCandle{float64(1_745_000_000)}
	got, ok := c.intAt(0)
	if !ok {
		t.Fatal("intAt(float64) returned !ok")
	}
	if got != 1_745_000_000 {
		t.Errorf("got %d, want 1745000000", got)
	}
}

func TestKrakenCandle_intAt_stringNumeric(t *testing.T) {
	c := krakenCandle{"1745000000"}
	got, ok := c.intAt(0)
	if !ok {
		t.Fatal("intAt(string-of-digits) returned !ok")
	}
	if got != 1_745_000_000 {
		t.Errorf("got %d, want 1745000000", got)
	}
}

func TestKrakenCandle_intAt_stringNonNumericRejected(t *testing.T) {
	// A string that won't ParseInt (e.g. floating-point text) must
	// return ok=false rather than swallowing the error and yielding
	// 0 — downstream code uses ok to decide whether the row is
	// trustworthy.
	c := krakenCandle{"not-a-number"}
	if _, ok := c.intAt(0); ok {
		t.Error("intAt(\"not-a-number\") returned ok, want false")
	}
}

func TestKrakenCandle_intAt_unsupportedTypeRejected(t *testing.T) {
	// JSON's `true` decodes to bool; bool→int coercion would be
	// silently lossy. The function must return ok=false.
	c := krakenCandle{true}
	if _, ok := c.intAt(0); ok {
		t.Error("intAt(bool) returned ok, want false")
	}
}
