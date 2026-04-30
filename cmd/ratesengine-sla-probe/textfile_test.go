package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTextfile_PassRun(t *testing.T) {
	fresh := 1.5
	rep := &report{
		BaseURL:     "https://api.example.com/v1",
		DurationSec: 30.0,
		Concurrency: 4,
		PerEndpoint: []stats{
			{
				Endpoint: "price", Path: "/price",
				Samples: 100, Successes: 100, AvailabilityPct: 100.0,
				LatencyMS:          latencyStats{P50: 12.0, P95: 45.0, P99: 78.0},
				ObservedAtFreshSec: &fresh,
			},
			{
				Endpoint: "healthz", Path: "/healthz",
				Samples: 50, Successes: 50, AvailabilityPct: 100.0,
				LatencyMS: latencyStats{P50: 3.0, P95: 8.0, P99: 12.0},
			},
		},
		Verdict: "pass",
	}
	var buf bytes.Buffer
	if err := writeTextfile(&buf, rep); err != nil {
		t.Fatalf("writeTextfile: %v", err)
	}
	out := buf.String()

	wants := []string{
		`ratesengine_sla_probe_latency_ms{endpoint="price",quantile="0.95"} 45.000`,
		`ratesengine_sla_probe_latency_ms{endpoint="healthz",quantile="0.5"} 3.000`,
		`ratesengine_sla_probe_availability_pct{endpoint="price"} 100.000`,
		`ratesengine_sla_probe_freshness_sec{endpoint="price"} 1.500`,
		`ratesengine_sla_probe_samples{endpoint="price"} 100`,
		`ratesengine_sla_probe_run_duration_seconds 30.000`,
		`ratesengine_sla_probe_unit_failed 0`,
		`ratesengine_sla_probe_last_pass_timestamp `, // unix value varies
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in textfile:\n%s", w, out)
		}
	}
}

// TestWriteTextfile_FailRun — fail verdict should set unit_failed=1
// and OMIT the last_pass_timestamp gauge so the staleness alert
// keys on the previous-scrape value.
func TestWriteTextfile_FailRun(t *testing.T) {
	rep := &report{
		PerEndpoint: []stats{{
			Endpoint: "price", Samples: 100, Successes: 50, AvailabilityPct: 50.0,
			LatencyMS: latencyStats{P50: 250, P95: 400, P99: 600},
		}},
		Verdict:       "fail",
		FailedReasons: []string{"price: availability=50.00% < target 99.90%"},
	}
	var buf bytes.Buffer
	if err := writeTextfile(&buf, rep); err != nil {
		t.Fatalf("writeTextfile: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "ratesengine_sla_probe_unit_failed 1") {
		t.Errorf("fail run should emit unit_failed=1; got:\n%s", out)
	}
	if strings.Contains(out, "ratesengine_sla_probe_last_pass_timestamp") {
		t.Errorf("fail run should NOT emit last_pass_timestamp; got:\n%s", out)
	}
}

// TestWriteTextfile_OmitsFreshnessBlockWhenAbsent — endpoints
// without observed_at (e.g. /healthz) shouldn't add the freshness
// HELP/TYPE block when no endpoint has freshness data.
func TestWriteTextfile_OmitsFreshnessBlockWhenAbsent(t *testing.T) {
	rep := &report{
		PerEndpoint: []stats{{
			Endpoint: "healthz", Samples: 10, Successes: 10, AvailabilityPct: 100,
			LatencyMS: latencyStats{P50: 5, P95: 9, P99: 11},
		}},
		Verdict: "pass",
	}
	var buf bytes.Buffer
	if err := writeTextfile(&buf, rep); err != nil {
		t.Fatalf("writeTextfile: %v", err)
	}
	if strings.Contains(buf.String(), "ratesengine_sla_probe_freshness_sec") {
		t.Errorf("should omit freshness block when no endpoint has freshness data:\n%s", buf.String())
	}
}

// TestWriteTextfile_DeterministicOrdering — same input should emit
// byte-identical output run-to-run (operator scrapers are happier
// with stable diff hygiene).
func TestWriteTextfile_DeterministicOrdering(t *testing.T) {
	rep := &report{
		PerEndpoint: []stats{
			{Endpoint: "zzz-pair", Samples: 10, AvailabilityPct: 100, LatencyMS: latencyStats{P50: 1, P95: 1, P99: 1}},
			{Endpoint: "aaa-pair", Samples: 10, AvailabilityPct: 100, LatencyMS: latencyStats{P50: 1, P95: 1, P99: 1}},
		},
		Verdict: "fail", // skip last_pass_timestamp to avoid time variation
	}
	var first, second bytes.Buffer
	if err := writeTextfile(&first, rep); err != nil {
		t.Fatal(err)
	}
	if err := writeTextfile(&second, rep); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Errorf("non-deterministic output:\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}
	// Sanity: aaa-pair appears before zzz-pair (sorted ascending).
	out := first.String()
	if strings.Index(out, `endpoint="aaa-pair"`) > strings.Index(out, `endpoint="zzz-pair"`) {
		t.Errorf("endpoints not sorted ascending; got:\n%s", out)
	}
}

func TestWriteTextfileAtomic_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sla_probe.prom")
	rep := &report{
		PerEndpoint: []stats{{
			Endpoint: "price", Samples: 1, AvailabilityPct: 100,
			LatencyMS: latencyStats{P50: 1, P95: 1, P99: 1},
		}},
		Verdict: "pass",
	}
	if err := writeTextfileAtomic(path, rep); err != nil {
		t.Fatalf("writeTextfileAtomic: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(body), "ratesengine_sla_probe_unit_failed 0") {
		t.Errorf("round-trip body missing unit_failed:\n%s", body)
	}
	// `<path>.tmp` should NOT exist after a clean rename.
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("temp file lingered after atomic write")
	}
}
