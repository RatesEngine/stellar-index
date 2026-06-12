package archivecompleteness_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/archivecompleteness"
)

// TestWriteTextfile_BasicShape — happy path: a populated snapshot
// produces well-formed Prometheus text exposition with the expected
// HELP/TYPE prefix and metric lines.
func TestWriteTextfile_BasicShape(t *testing.T) {
	snap := archivecompleteness.NewMetricsSnapshot()
	snap.FilesMissing["cross-anchor"] = 0
	snap.FilesMissing["galexie-archive"] = 12
	snap.RunDurationSeconds = 73.421
	snap.LastSuccessTimestamp = time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	snap.RepairAttempts["sdf-core-live-001"] = 3
	snap.RepairFailures["multi-source-exhausted"] = 1

	var buf bytes.Buffer
	if err := archivecompleteness.WriteTextfile(&buf, snap); err != nil {
		t.Fatalf("WriteTextfile: %v", err)
	}
	out := buf.String()

	expectedSubstrings := []string{
		"# HELP archive_files_missing",
		"# TYPE archive_files_missing gauge",
		`archive_files_missing{archive="cross-anchor"} 0`,
		`archive_files_missing{archive="galexie-archive"} 12`,
		"# TYPE archive_completeness_last_success_timestamp gauge",
		"archive_completeness_last_success_timestamp 1777370400",
		"# TYPE archive_completeness_run_duration_seconds gauge",
		"archive_completeness_run_duration_seconds 73.421",
		"# TYPE archive_completeness_repair_attempts_total counter",
		`archive_completeness_repair_attempts_total{source="sdf-core-live-001"} 3`,
		"# TYPE archive_completeness_repair_failures_total counter",
		`archive_completeness_repair_failures_total{source="multi-source-exhausted"} 1`,
	}
	for _, want := range expectedSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("textfile output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// TestWriteTextfile_EmptySnapshotProducesOnlyHelp — a snapshot with
// no missing files and no repair activity should still emit
// well-formed output (just the help/type for files_missing, with
// no sample lines below it). Empty maps are valid input.
func TestWriteTextfile_EmptySnapshotProducesOnlyHelp(t *testing.T) {
	snap := archivecompleteness.NewMetricsSnapshot()
	var buf bytes.Buffer
	if err := archivecompleteness.WriteTextfile(&buf, snap); err != nil {
		t.Fatalf("WriteTextfile: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "archive_files_missing gauge") {
		t.Errorf("missing files_missing TYPE line:\n%s", out)
	}
	// No sample lines for files_missing because the map is empty.
	if strings.Contains(out, "archive_files_missing{") {
		t.Errorf("unexpected sample line in empty snapshot:\n%s", out)
	}
}

// TestWriteTextfile_NilSnapshotIsNoop — defensive guard against
// callers passing nil. Should produce no output and no error.
func TestWriteTextfile_NilSnapshotIsNoop(t *testing.T) {
	var buf bytes.Buffer
	if err := archivecompleteness.WriteTextfile(&buf, nil); err != nil {
		t.Errorf("nil snapshot should be a no-op, got error: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("nil snapshot wrote %d bytes, want 0", buf.Len())
	}
}

// TestWriteTextfileAtomic_RoundTrip — writes via the atomic path
// (.tmp + rename) and the final file is a valid Prometheus textfile.
func TestWriteTextfileAtomic_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archive_completeness.prom")

	snap := archivecompleteness.NewMetricsSnapshot()
	snap.FilesMissing["cross-anchor"] = 5

	if err := archivecompleteness.WriteTextfileAtomic(path, snap); err != nil {
		t.Fatalf("WriteTextfileAtomic: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), `archive_files_missing{archive="cross-anchor"} 5`) {
		t.Errorf("textfile content missing expected metric:\n%s", body)
	}

	// The .tmp companion must NOT exist after a successful rename.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp file should be removed after rename; err=%v", err)
	}
}

// TestPopulateFromReport — convenience wrapper preserves the
// per-archive missing-counts on the snapshot.
func TestPopulateFromReport(t *testing.T) {
	r := archivecompleteness.NewReport(2, 1000)
	r.SetCrossAnchor("/srv/history-archive", archivecompleteness.CrossAnchorResult{
		Expected: 10, Found: 7, Missing: []uint32{63, 127, 191},
	})

	snap := archivecompleteness.NewMetricsSnapshot()
	snap.PopulateFromReport(r)

	if snap.FilesMissing["cross-anchor"] != 3 {
		t.Errorf("cross-anchor missing = %d, want 3", snap.FilesMissing["cross-anchor"])
	}
	if _, present := snap.FilesMissing["galexie-archive"]; present {
		t.Errorf("galexie-archive should not be in snapshot when Report.Primary is nil")
	}
}

// TestPopulateFromFillResult — repair-attempts and -failures get
// populated from a FillResult.
func TestPopulateFromFillResult(t *testing.T) {
	res := archivecompleteness.FillResult{
		Filled:           5,
		PerSourceSuccess: map[string]int{"sdf-core-live-001": 4, "lobstr-v1": 1},
		Failed:           []archivecompleteness.FillFailure{{Seq: 63, Reason: "exhausted"}},
	}
	snap := archivecompleteness.NewMetricsSnapshot()
	snap.PopulateFromFillResult(res)

	if snap.RepairAttempts["sdf-core-live-001"] != 4 {
		t.Errorf("sdf-001 attempts = %d, want 4", snap.RepairAttempts["sdf-core-live-001"])
	}
	if snap.RepairFailures["multi-source-exhausted"] != 1 {
		t.Errorf("multi-source-exhausted failures = %d, want 1", snap.RepairFailures["multi-source-exhausted"])
	}
}

// TestWriteTextfile_StableOrdering — multiple runs of the same
// snapshot produce byte-identical output. Important for ops because
// a node_exporter scrape comparing successive textfiles uses
// content equality to detect "nothing changed".
func TestWriteTextfile_StableOrdering(t *testing.T) {
	snap := archivecompleteness.NewMetricsSnapshot()
	snap.FilesMissing["cross-anchor"] = 0
	snap.FilesMissing["galexie-archive"] = 0
	snap.RepairAttempts["lobstr-v1"] = 1
	snap.RepairAttempts["sdf-core-live-001"] = 2
	snap.RepairAttempts["sdf-core-live-002"] = 1

	var first, second bytes.Buffer
	_ = archivecompleteness.WriteTextfile(&first, snap)
	_ = archivecompleteness.WriteTextfile(&second, snap)

	if first.String() != second.String() {
		t.Errorf("output not stable across writes:\n--- first ---\n%s\n--- second ---\n%s", first.String(), second.String())
	}
}
