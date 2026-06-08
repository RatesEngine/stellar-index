package completeness

import (
	"context"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/sources/sorobanevents"
)

// ─── fakes ──────────────────────────────────────────────────────

type fakeStreamer struct{ rows []sorobanevents.Row }

func (f fakeStreamer) StreamSorobanEvents(
	_ context.Context, from, to uint32, _ []string, _ []string, _ []string,
	fn func(sorobanevents.Row) error,
) error {
	for _, r := range f.rows {
		if r.Ledger < from || r.Ledger > to {
			continue
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

// matchDecoder matches rows whose ContractID is "MATCH" and emits one
// output each — enough to exercise per-ledger output counting.
type matchDecoder struct{}

func (matchDecoder) Matches(ev events.Event) bool { return ev.ContractID == "MATCH" }
func (matchDecoder) Decode(events.Event) ([]consumer.Event, error) {
	return []consumer.Event{fakeOutput{}}, nil
}

type fakeOutput struct{}

func (fakeOutput) Source() string    { return "fake" }
func (fakeOutput) EventKind() string { return "trade" }

// twoKindDecoder emits one "x.trade" and one "x.liquidity" output per
// matched event — the multi-table shape (one decoder, two destinations).
type twoKindDecoder struct{}

func (twoKindDecoder) Matches(ev events.Event) bool { return ev.ContractID == "MATCH" }
func (twoKindDecoder) Decode(events.Event) ([]consumer.Event, error) {
	return []consumer.Event{kindOutput("x.trade"), kindOutput("x.liquidity")}, nil
}

type kindOutput string

func (k kindOutput) Source() string    { return "x" }
func (k kindOutput) EventKind() string { return string(k) }

func rowAt(ledger uint32, contractID string) sorobanevents.Row {
	return sorobanevents.Row{
		Ledger:          ledger,
		LedgerCloseTime: time.Unix(int64(ledger), 0).UTC(),
		TxHash:          make([]byte, 32),
		ContractID:      contractID,
		ContractIDHex:   make([]byte, 32),
		TopicCount:      1,
		Topic0XDR:       []byte{0x00, 0x00, 0x00, 0x01},
		BodyXDR:         []byte{0x00, 0x00, 0x00, 0x00},
	}
}

// ─── tests ──────────────────────────────────────────────────────

func TestReDeriveOutputCounts(t *testing.T) {
	s := fakeStreamer{rows: []sorobanevents.Row{
		rowAt(100, "MATCH"),   // → 1 output
		rowAt(100, "MATCH"),   // → 1 output (ledger 100 total 2)
		rowAt(101, "NOMATCH"), // Matches=false → 0
		rowAt(102, "MATCH"),   // → 1 output
		rowAt(999, "MATCH"),   // out of range → skipped
	}}
	got, err := ReDeriveOutputCounts(context.Background(), s, matchDecoder{}, nil, nil, 100, 102)
	if err != nil {
		t.Fatalf("ReDeriveOutputCounts: %v", err)
	}
	want := map[uint32]int{100: 2, 102: 1}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for l, w := range want {
		if got[l] != w {
			t.Errorf("ledger %d = %d, want %d", l, got[l], w)
		}
	}
}

func TestReDeriveOutputCountsByKind(t *testing.T) {
	s := fakeStreamer{rows: []sorobanevents.Row{
		rowAt(100, "MATCH"),   // → x.trade@100, x.liquidity@100
		rowAt(100, "MATCH"),   // → x.trade@100, x.liquidity@100 (each kind = 2 @100)
		rowAt(101, "NOMATCH"), // skipped
		rowAt(102, "MATCH"),   // → x.trade@102, x.liquidity@102
	}}
	byKind, err := ReDeriveOutputCountsByKind(context.Background(), s, twoKindDecoder{}, nil, nil, 100, 102)
	if err != nil {
		t.Fatalf("ReDeriveOutputCountsByKind: %v", err)
	}
	if byKind["x.trade"][100] != 2 || byKind["x.trade"][102] != 1 {
		t.Errorf("x.trade counts = %v, want {100:2,102:1}", byKind["x.trade"])
	}
	if byKind["x.liquidity"][100] != 2 || byKind["x.liquidity"][102] != 1 {
		t.Errorf("x.liquidity counts = %v, want {100:2,102:1}", byKind["x.liquidity"])
	}

	// SumKinds projects to the table's kinds only — trades table sees
	// only x.trade, not x.liquidity (the overcount bug this fixes).
	trades := SumKinds(byKind, "x.trade")
	if trades[100] != 2 || trades[102] != 1 || len(trades) != 2 {
		t.Errorf("SumKinds(x.trade) = %v, want {100:2,102:1}", trades)
	}
	both := SumKinds(byKind, "x.trade", "x.liquidity")
	if both[100] != 4 || both[102] != 2 {
		t.Errorf("SumKinds(both) = %v, want {100:4,102:2}", both)
	}
	if len(SumKinds(byKind, "nonexistent.kind")) != 0 {
		t.Error("SumKinds(unknown kind) should be empty")
	}
}

func TestReconcileCounts(t *testing.T) {
	tests := []struct {
		name             string
		expected, actual map[uint32]int
		want             []ProjectionGap
	}{
		{
			name:     "all match",
			expected: map[uint32]int{100: 2, 101: 1},
			actual:   map[uint32]int{100: 2, 101: 1},
			want:     nil,
		},
		{
			name:     "projection drop",
			expected: map[uint32]int{100: 2, 101: 1},
			actual:   map[uint32]int{100: 2, 101: 0},
			want:     []ProjectionGap{{Ledger: 101, Expected: 1, Actual: 0}},
		},
		{
			name:     "phantom row",
			expected: map[uint32]int{100: 2},
			actual:   map[uint32]int{100: 2, 200: 3},
			want:     []ProjectionGap{{Ledger: 200, Expected: 0, Actual: 3}},
		},
		{
			name:     "sorted multi-gap",
			expected: map[uint32]int{300: 5, 100: 1, 200: 2},
			actual:   map[uint32]int{300: 4, 100: 1, 200: 0},
			want:     []ProjectionGap{{Ledger: 200, Expected: 2, Actual: 0}, {Ledger: 300, Expected: 5, Actual: 4}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ReconcileCounts(tc.expected, tc.actual)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("gap[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
