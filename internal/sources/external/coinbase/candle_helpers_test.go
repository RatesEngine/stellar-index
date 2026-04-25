package coinbase

import (
	"testing"
)

// coinbaseCandle is "[time, low, high, open, close, volume]" per
// the Coinbase /products/<id>/candles JSON shape. The accessors
// intAt / closeFloat / volumeFloat handle the type-uncertainty
// quirks of upstream sometimes serialising numbers as strings vs
// JSON numbers.

func TestCoinbaseCandle_intAt(t *testing.T) {
	cases := []struct {
		name string
		row  coinbaseCandle
		idx  int
		want int64
		ok   bool
	}{
		{"float64 in range", coinbaseCandle{1.7e9}, 0, 1_700_000_000, true},
		{"string parses", coinbaseCandle{"42"}, 0, 42, true},
		{"string parse failure", coinbaseCandle{"not-a-number"}, 0, 0, false},
		{"unsupported type", coinbaseCandle{true}, 0, 0, false},
		{"index out of range", coinbaseCandle{1.0}, 5, 0, false},
		{"empty row", coinbaseCandle{}, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.row.intAt(tc.idx)
			if got != tc.want || ok != tc.ok {
				t.Errorf("intAt(%d) = (%d, %v), want (%d, %v)", tc.idx, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestCoinbaseCandle_closeFloat(t *testing.T) {
	t.Run("happy", func(t *testing.T) {
		row := coinbaseCandle{1.7e9, 0.10, 0.20, 0.11, 0.18, 12345.67}
		got, ok := row.closeFloat()
		if !ok || got != 0.18 {
			t.Errorf("closeFloat() = (%v, %v), want (0.18, true)", got, ok)
		}
	})
	t.Run("too short", func(t *testing.T) {
		row := coinbaseCandle{1.7e9, 0.10}
		_, ok := row.closeFloat()
		if ok {
			t.Error("closeFloat() ok = true, want false (row too short)")
		}
	})
	t.Run("wrong type at index", func(t *testing.T) {
		row := coinbaseCandle{1.7e9, 0.10, 0.20, 0.11, "0.18", 12345.67}
		_, ok := row.closeFloat()
		if ok {
			t.Error("closeFloat() ok = true, want false (close field is string)")
		}
	})
}

func TestCoinbaseCandle_volumeFloat(t *testing.T) {
	t.Run("happy", func(t *testing.T) {
		row := coinbaseCandle{1.7e9, 0.10, 0.20, 0.11, 0.18, 12345.67}
		got, ok := row.volumeFloat()
		if !ok || got != 12345.67 {
			t.Errorf("volumeFloat() = (%v, %v), want (12345.67, true)", got, ok)
		}
	})
	t.Run("too short", func(t *testing.T) {
		row := coinbaseCandle{1.7e9, 0.10, 0.20, 0.11, 0.18}
		_, ok := row.volumeFloat()
		if ok {
			t.Error("volumeFloat() ok = true, want false (row too short)")
		}
	})
	t.Run("wrong type at index", func(t *testing.T) {
		row := coinbaseCandle{1.7e9, 0.10, 0.20, 0.11, 0.18, "12345.67"}
		_, ok := row.volumeFloat()
		if ok {
			t.Error("volumeFloat() ok = true, want false (volume field is string)")
		}
	})
}

func TestCoinbaseCandle_openTimeSec(t *testing.T) {
	row := coinbaseCandle{1_770_000_000.0, 0.10, 0.20, 0.11, 0.18, 12345.67}
	got, ok := row.openTimeSec()
	if !ok || got != 1_770_000_000 {
		t.Errorf("openTimeSec() = (%v, %v), want (1770000000, true)", got, ok)
	}
}
