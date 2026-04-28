package archivecompleteness_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/archivecompleteness"
)

// makeTestArchive builds a minimal `<root>/ledger/XX/YY/ZZ/ledger-…xdr.gz`
// tree with checkpoint files at the supplied sequences. Each file
// contains a one-byte gzip stream — enough for os.Stat to confirm
// presence; the package doesn't read content for structural checks.
func makeTestArchive(t *testing.T, present []uint32) string {
	t.Helper()
	root := t.TempDir()
	for _, seq := range present {
		hex := fmt.Sprintf("%08x", seq)
		dir := filepath.Join(root, "ledger", hex[0:2], hex[2:4], hex[4:6])
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		path := filepath.Join(dir, "ledger-"+hex+".xdr.gz")
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write([]byte("x"))
		_ = gz.Close()
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

// TestCheck_AllPresent — fully-populated archive across the requested
// range produces missing-count 0.
func TestCheck_AllPresent(t *testing.T) {
	// Range [0, 191] covers checkpoints at 63, 127, 191.
	root := makeTestArchive(t, []uint32{63, 127, 191})
	c := archivecompleteness.NewCrossAnchorChecker(root)

	res, err := c.Check(0, 191)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Expected != 3 {
		t.Errorf("Expected = %d, want 3", res.Expected)
	}
	if res.Found != 3 {
		t.Errorf("Found = %d, want 3", res.Found)
	}
	if len(res.Missing) != 0 {
		t.Errorf("Missing = %v, want empty", res.Missing)
	}
}

// TestCheck_AllMissing — empty archive across a 3-checkpoint range.
func TestCheck_AllMissing(t *testing.T) {
	root := makeTestArchive(t, nil) // archive root exists but no checkpoint files
	c := archivecompleteness.NewCrossAnchorChecker(root)

	res, err := c.Check(0, 191)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Expected != 3 {
		t.Errorf("Expected = %d, want 3", res.Expected)
	}
	if res.Found != 0 {
		t.Errorf("Found = %d, want 0", res.Found)
	}
	if len(res.Missing) != 3 {
		t.Errorf("Missing = %v, want 3 entries", res.Missing)
	}
	wantSeqs := []uint32{63, 127, 191}
	for i, want := range wantSeqs {
		if res.Missing[i] != want {
			t.Errorf("Missing[%d] = %d, want %d", i, res.Missing[i], want)
		}
	}
}

// TestCheck_PartialMissing — some present, some missing. Verifies
// the Missing list contains exactly the absent positions and the
// counts match.
func TestCheck_PartialMissing(t *testing.T) {
	// Present: 63 and 191. Missing: 127.
	root := makeTestArchive(t, []uint32{63, 191})
	c := archivecompleteness.NewCrossAnchorChecker(root)

	res, err := c.Check(0, 191)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Expected != 3 || res.Found != 2 {
		t.Errorf("counts: Expected=%d Found=%d, want 3 / 2", res.Expected, res.Found)
	}
	if len(res.Missing) != 1 || res.Missing[0] != 127 {
		t.Errorf("Missing = %v, want [127]", res.Missing)
	}
}

// TestCheck_RangeAlignment — non-checkpoint-aligned `from` / `to`
// values still produce the correct Expected count from the enclosed
// checkpoints.
func TestCheck_RangeAlignment(t *testing.T) {
	// Range [50, 200] should enclose checkpoints at 63, 127, 191
	// (191 is the last checkpoint <= 200).
	root := makeTestArchive(t, []uint32{63, 127, 191})
	c := archivecompleteness.NewCrossAnchorChecker(root)

	res, err := c.Check(50, 200)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Expected != 3 {
		t.Errorf("Expected = %d, want 3", res.Expected)
	}
	if res.Found != 3 {
		t.Errorf("Found = %d, want 3", res.Found)
	}
}

// TestCheck_RangeBelowFirstCheckpoint — a range entirely below
// seq=63 contains no checkpoint. Empty result, no error.
func TestCheck_RangeBelowFirstCheckpoint(t *testing.T) {
	root := makeTestArchive(t, nil)
	c := archivecompleteness.NewCrossAnchorChecker(root)

	res, err := c.Check(0, 50)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Expected != 0 || res.Found != 0 || len(res.Missing) != 0 {
		t.Errorf("range below first checkpoint should be empty, got %+v", res)
	}
}

// TestCheck_InvalidArchiveRoot — non-existent path returns an error.
func TestCheck_InvalidArchiveRoot(t *testing.T) {
	c := archivecompleteness.NewCrossAnchorChecker("/nonexistent/path/that/does/not/exist")
	_, err := c.Check(0, 191)
	if err == nil {
		t.Fatal("expected error for missing archiveRoot, got nil")
	}
}

// TestCheck_EmptyArchiveRoot — explicit empty string is rejected.
func TestCheck_EmptyArchiveRoot(t *testing.T) {
	c := archivecompleteness.NewCrossAnchorChecker("")
	_, err := c.Check(0, 191)
	if err == nil {
		t.Fatal("expected error for empty archiveRoot, got nil")
	}
}

// TestCheck_ToLessThanFrom — invalid range is an error.
func TestCheck_ToLessThanFrom(t *testing.T) {
	root := makeTestArchive(t, nil)
	c := archivecompleteness.NewCrossAnchorChecker(root)
	_, err := c.Check(200, 100)
	if err == nil {
		t.Fatal("expected error for to < from, got nil")
	}
}

// TestCheck_ArchiveRootIsFile — operator passes a regular file path
// instead of a directory; surface a clear error rather than walking
// nonsense.
func TestCheck_ArchiveRootIsFile(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(tmp, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	c := archivecompleteness.NewCrossAnchorChecker(tmp)
	_, err := c.Check(0, 191)
	if err == nil {
		t.Fatal("expected error for archiveRoot=file, got nil")
	}
}

// TestReport_RoundTripJSON — Report → JSON → struct should
// preserve every field. Wire shape lock for downstream tools.
func TestReport_RoundTripJSON(t *testing.T) {
	r := archivecompleteness.NewReport(2, 191)
	r.SetCrossAnchor("/tmp/test-archive", archivecompleteness.CrossAnchorResult{
		From:     2,
		To:       191,
		Expected: 3,
		Found:    2,
		Missing:  []uint32{127},
	})

	var buf bytes.Buffer
	if err := r.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var got archivecompleteness.Report
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Schema != "1" {
		t.Errorf("Schema = %q, want \"1\"", got.Schema)
	}
	if got.Range.From != 2 || got.Range.To != 191 {
		t.Errorf("Range = %+v", got.Range)
	}
	if got.CrossAnchor == nil {
		t.Fatal("CrossAnchor section missing")
	}
	if got.CrossAnchor.ArchiveRoot != "/tmp/test-archive" {
		t.Errorf("ArchiveRoot = %q", got.CrossAnchor.ArchiveRoot)
	}
	if got.CrossAnchor.MissingCount != 1 {
		t.Errorf("MissingCount = %d, want 1", got.CrossAnchor.MissingCount)
	}
	if len(got.CrossAnchor.Missing) != 1 || got.CrossAnchor.Missing[0] != 127 {
		t.Errorf("Missing = %v, want [127]", got.CrossAnchor.Missing)
	}
}

// TestReport_AnyMissing — convenience predicate fires when either
// section has gaps; clean when all sections clean or absent.
func TestReport_AnyMissing(t *testing.T) {
	r := archivecompleteness.NewReport(2, 191)
	if r.AnyMissing() {
		t.Error("empty report should not report missing")
	}
	r.SetCrossAnchor("/r", archivecompleteness.CrossAnchorResult{Expected: 3, Found: 3})
	if r.AnyMissing() {
		t.Error("clean cross-anchor: should not report missing")
	}
	r.SetCrossAnchor("/r", archivecompleteness.CrossAnchorResult{Expected: 3, Found: 2, Missing: []uint32{127}})
	if !r.AnyMissing() {
		t.Error("cross-anchor with gap: should report missing")
	}
}

// TestReport_TruncatedFlagSurvivesJSON — when a catastrophic gap
// trips the MaxMissingReported cap, Truncated must round-trip on
// the wire so consumers know the list is partial.
func TestReport_TruncatedFlagSurvivesJSON(t *testing.T) {
	r := archivecompleteness.NewReport(2, 1<<20)
	r.SetCrossAnchor("/r", archivecompleteness.CrossAnchorResult{
		Expected:  100000,
		Found:     0,
		Missing:   []uint32{63, 127, 191}, // partial — pretend the rest were truncated
		Truncated: true,
	})

	var buf bytes.Buffer
	if err := r.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var got archivecompleteness.Report
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !got.CrossAnchor.Truncated {
		t.Error("Truncated didn't survive the round-trip")
	}
}
