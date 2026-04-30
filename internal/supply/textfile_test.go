package supply

import (
	"bytes"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteSnapshotMetrics_PassRun(t *testing.T) {
	snap := Supply{
		AssetKey:          "XLM",
		TotalSupply:       big.NewInt(50_001_806_812 * 10_000_000), // 50B XLM in stroops
		CirculatingSupply: big.NewInt(30_000_000_000 * 10_000_000),
		MaxSupply:         big.NewInt(50_001_806_812 * 10_000_000),
		Basis:             BasisXLMSDFReserveExclusion,
		LedgerSequence:    50_000_000,
		ObservedAt:        time.Unix(1_770_000_000, 0).UTC(),
	}
	var buf bytes.Buffer
	if err := writeSnapshotMetrics(&buf, snap, 1.234, true); err != nil {
		t.Fatalf("writeSnapshotMetrics: %v", err)
	}
	out := buf.String()
	wants := []string{
		`ratesengine_supply_snapshot_total_xlm{asset_key="XLM"} 50001806812.000`,
		`ratesengine_supply_snapshot_circulating_xlm{asset_key="XLM"} 30000000000.000`,
		`ratesengine_supply_snapshot_max_xlm{asset_key="XLM"} 50001806812.000`,
		`ratesengine_supply_snapshot_ledger{asset_key="XLM"} 50000000`,
		`ratesengine_supply_snapshot_observed_at_seconds{asset_key="XLM"} 1770000000`,
		`ratesengine_supply_snapshot_run_duration_seconds 1.234`,
		`ratesengine_supply_snapshot_unit_failed{asset_key="XLM"} 0`,
		`ratesengine_supply_snapshot_last_success_timestamp{asset_key="XLM"}`,
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}
}

// TestWriteSnapshotMetrics_OmitsMaxWhenNil — max_supply is null
// for uncapped assets per ADR-0011 ("we don't fabricate"). The
// metric should be absent rather than emitting a zero or NaN.
func TestWriteSnapshotMetrics_OmitsMaxWhenNil(t *testing.T) {
	snap := Supply{
		AssetKey:          "USDC:GA5...",
		TotalSupply:       big.NewInt(1_000_000),
		CirculatingSupply: big.NewInt(900_000),
		MaxSupply:         nil, // uncapped
		Basis:             BasisIssuerExclusion,
		LedgerSequence:    100,
		ObservedAt:        time.Now().UTC(),
	}
	var buf bytes.Buffer
	if err := writeSnapshotMetrics(&buf, snap, 0.5, true); err != nil {
		t.Fatalf("writeSnapshotMetrics: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "ratesengine_supply_snapshot_max_xlm") {
		t.Errorf("nil MaxSupply should omit max_xlm metric:\n%s", out)
	}
	if !strings.Contains(out, `ratesengine_supply_snapshot_total_xlm{asset_key="USDC:GA5..."}`) {
		t.Errorf("total_xlm should still emit:\n%s", out)
	}
}

// TestWriteSnapshotMetrics_FailRun — failed runs (pass=false) emit
// unit_failed=1 and OMIT last_success_timestamp. The staleness alert
// keys on time-since-last-success, so omitting on failure preserves
// the previous-scrape value.
func TestWriteSnapshotMetrics_FailRun(t *testing.T) {
	snap := Supply{
		AssetKey:          "XLM",
		TotalSupply:       big.NewInt(0),
		CirculatingSupply: big.NewInt(0),
		ObservedAt:        time.Now().UTC(),
	}
	var buf bytes.Buffer
	if err := writeSnapshotMetrics(&buf, snap, 0.1, false); err != nil {
		t.Fatalf("writeSnapshotMetrics: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `ratesengine_supply_snapshot_unit_failed{asset_key="XLM"} 1`) {
		t.Errorf("fail run should emit unit_failed=1:\n%s", out)
	}
	if strings.Contains(out, "ratesengine_supply_snapshot_last_success_timestamp") {
		t.Errorf("fail run should NOT emit last_success_timestamp:\n%s", out)
	}
}

func TestWriteFailureMetrics_NoValueGauges(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFailureMetrics(&buf, "native", 0.5); err != nil {
		t.Fatalf("writeFailureMetrics: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `ratesengine_supply_snapshot_unit_failed{asset_key="native"} 1`) {
		t.Errorf("failure path should emit unit_failed=1:\n%s", out)
	}
	if strings.Contains(out, "ratesengine_supply_snapshot_total_xlm") {
		t.Errorf("failure path has no Supply, should not emit value gauges:\n%s", out)
	}
	if strings.Contains(out, "ratesengine_supply_snapshot_last_success_timestamp") {
		t.Errorf("failure path should NOT emit last_success_timestamp:\n%s", out)
	}
}

func TestWriteSnapshotTextfile_AtomicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supply_snapshot.prom")
	snap := Supply{
		AssetKey:          "XLM",
		TotalSupply:       big.NewInt(1_000_000),
		CirculatingSupply: big.NewInt(900_000),
		MaxSupply:         big.NewInt(1_000_000),
		LedgerSequence:    1,
		ObservedAt:        time.Now().UTC(),
	}
	if err := WriteSnapshotTextfile(path, snap, 1.0, true); err != nil {
		t.Fatalf("WriteSnapshotTextfile: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(body), "ratesengine_supply_snapshot_unit_failed") {
		t.Errorf("round-trip missing unit_failed:\n%s", body)
	}
	// `<path>.tmp` should be gone after a clean rename.
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("temp file lingered after atomic write")
	}
}

func TestStroopsToXLM(t *testing.T) {
	cases := []struct {
		stroops string
		want    float64
	}{
		{"10000000", 1.0},                     // 1 XLM = 10^7 stroops
		{"50001806812000000", 5.0001806812e9}, // network total
		{"0", 0.0},
	}
	for _, tc := range cases {
		s, _ := new(big.Int).SetString(tc.stroops, 10)
		got := stroopsToXLM(s)
		if abs(got-tc.want) > 1e-3 {
			t.Errorf("stroopsToXLM(%s) = %g, want %g", tc.stroops, got, tc.want)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
