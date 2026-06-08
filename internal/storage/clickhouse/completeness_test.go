package clickhouse

import "testing"

func TestWatermark(t *testing.T) {
	tests := []struct {
		name             string
		from, chMax, gap uint32
		want             uint32
	}{
		{"ch not yet reached from", 100, 90, 0, 99},   // chMax<from → from-1 (idle)
		{"ch exactly at from-1", 100, 99, 0, 99},      // same boundary
		{"complete to tip, no gap", 100, 200, 0, 200}, // firstGap==0 → chMax
		{"gap above from", 100, 200, 150, 149},        // hole at 150 → 149
		{"gap right at from", 100, 200, 100, 99},      // hole at from itself → from-1 (idle)
		{"gap one past from", 100, 200, 101, 100},     // first ledger present, hole next
		{"single complete ledger at from", 100, 100, 0, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := watermark(tt.from, tt.chMax, tt.gap); got != tt.want {
				t.Errorf("watermark(from=%d, chMax=%d, gap=%d) = %d, want %d",
					tt.from, tt.chMax, tt.gap, got, tt.want)
			}
		})
	}
}
