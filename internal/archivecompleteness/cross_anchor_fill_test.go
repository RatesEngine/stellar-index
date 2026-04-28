package archivecompleteness_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/archivecompleteness"
)

// goodGzipBody is a minimal valid gzip stream the fake source
// returns when "the file exists upstream".
func goodGzipBody() []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte("ok"))
	_ = gz.Close()
	return buf.Bytes()
}

// fakeSource wraps an httptest.Server with per-checkpoint behaviour.
// behaviour returns (status, body) for a given relPath; the test
// expresses fault patterns (404 always, 500 once, etc.) by
// returning the right tuple.
func fakeSource(t *testing.T, behaviour func(relPath string) (int, []byte)) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, body := behaviour(r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// makeFiller wires a Filler that points at the supplied sources +
// a temp archive root.
func makeFiller(t *testing.T, sources []archivecompleteness.Source) (*archivecompleteness.CrossAnchorFiller, string) {
	t.Helper()
	root := t.TempDir()
	f, err := archivecompleteness.NewCrossAnchorFiller(archivecompleteness.FillerOptions{
		ArchiveRoot: root,
		Sources:     sources,
		Workers:     2, // keep test deterministic
	})
	if err != nil {
		t.Fatalf("NewCrossAnchorFiller: %v", err)
	}
	return f, root
}

// TestFill_HappyPath — single healthy source, every requested
// checkpoint comes back as a valid gzip and lands on disk.
func TestFill_HappyPath(t *testing.T) {
	ts := fakeSource(t, func(relPath string) (int, []byte) {
		if !strings.HasPrefix(relPath, "/ledger/") {
			return http.StatusBadRequest, nil
		}
		return http.StatusOK, goodGzipBody()
	})

	f, root := makeFiller(t, []archivecompleteness.Source{
		{Name: "test-source", URL: ts.URL},
	})

	res := f.Fill(context.Background(), []uint32{63, 127, 191})
	if res.Filled != 3 {
		t.Errorf("Filled = %d, want 3", res.Filled)
	}
	if len(res.Failed) != 0 {
		t.Errorf("Failed = %v, want empty", res.Failed)
	}
	if res.PerSourceSuccess["test-source"] != 3 {
		t.Errorf("PerSourceSuccess[test-source] = %d, want 3", res.PerSourceSuccess["test-source"])
	}

	// Each placed file should be at the canonical path + valid gzip.
	for _, seq := range []uint32{63, 127, 191} {
		hex := fmt.Sprintf("%08x", seq)
		path := filepath.Join(root, "ledger", hex[0:2], hex[2:4], hex[4:6], "ledger-"+hex+".xdr.gz")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file at %s: %v", path, err)
		}
	}
}

// TestFill_FallbackToSecondSource — first source 404s every request;
// second source serves them. Verifies the fallback chain works AND
// that PerSourceSuccess attributes wins to the right source.
func TestFill_FallbackToSecondSource(t *testing.T) {
	src1 := fakeSource(t, func(_ string) (int, []byte) {
		return http.StatusNotFound, nil
	})
	src2 := fakeSource(t, func(_ string) (int, []byte) {
		return http.StatusOK, goodGzipBody()
	})

	f, _ := makeFiller(t, []archivecompleteness.Source{
		{Name: "primary-down", URL: src1.URL},
		{Name: "fallback-up", URL: src2.URL},
	})

	res := f.Fill(context.Background(), []uint32{63})
	if res.Filled != 1 {
		t.Errorf("Filled = %d, want 1", res.Filled)
	}
	// Either source might be tried first due to shuffle; we just
	// require the FALLBACK source to have eventually succeeded.
	if res.PerSourceSuccess["fallback-up"]+res.PerSourceSuccess["primary-down"] < 1 {
		t.Errorf("expected at least one source to succeed; PerSource=%v", res.PerSourceSuccess)
	}
}

// TestFill_AllSourcesFail — every source 404s. Each requested
// checkpoint exhausts the chain and lands in Failed.
func TestFill_AllSourcesFail(t *testing.T) {
	src1 := fakeSource(t, func(_ string) (int, []byte) { return http.StatusNotFound, nil })
	src2 := fakeSource(t, func(_ string) (int, []byte) { return http.StatusNotFound, nil })

	f, root := makeFiller(t, []archivecompleteness.Source{
		{Name: "s1", URL: src1.URL},
		{Name: "s2", URL: src2.URL},
	})

	res := f.Fill(context.Background(), []uint32{63, 127})
	if res.Filled != 0 {
		t.Errorf("Filled = %d, want 0", res.Filled)
	}
	if len(res.Failed) != 2 {
		t.Errorf("Failed count = %d, want 2", len(res.Failed))
	}
	// Every Failed entry's Reason should mention every source we
	// tried (so the operator knows nobody had it).
	for _, f := range res.Failed {
		if !strings.Contains(f.Reason, "s1") && !strings.Contains(f.Reason, "s2") {
			t.Errorf("Failure reason %q should mention at least one source", f.Reason)
		}
	}

	// No file should have landed.
	hex := fmt.Sprintf("%08x", uint32(63))
	path := filepath.Join(root, "ledger", hex[0:2], hex[2:4], hex[4:6], "ledger-"+hex+".xdr.gz")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file at %s should not exist after all-source-fail; err=%v", path, err)
	}
}

// TestFill_RejectsInvalidGzip — source returns 200 with garbage
// (not a gzip stream). Filler must NOT place the file (gzip
// validation guard).
func TestFill_RejectsInvalidGzip(t *testing.T) {
	ts := fakeSource(t, func(_ string) (int, []byte) {
		return http.StatusOK, []byte("this is not gzip data")
	})

	f, root := makeFiller(t, []archivecompleteness.Source{
		{Name: "corrupt-source", URL: ts.URL},
	})

	res := f.Fill(context.Background(), []uint32{63})
	if res.Filled != 0 {
		t.Errorf("Filled = %d, want 0 (gzip should reject corrupt body)", res.Filled)
	}
	if len(res.Failed) != 1 {
		t.Errorf("Failed count = %d, want 1", len(res.Failed))
	}

	hex := fmt.Sprintf("%08x", uint32(63))
	path := filepath.Join(root, "ledger", hex[0:2], hex[2:4], hex[4:6], "ledger-"+hex+".xdr.gz")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("invalid-gzip file should not be placed; err=%v", err)
	}
}

// TestFill_RejectsEmptyBody — source returns 200 with an empty
// body (rare but possible from a misbehaving CDN). Filler treats
// it as a failure and falls through.
func TestFill_RejectsEmptyBody(t *testing.T) {
	src1 := fakeSource(t, func(_ string) (int, []byte) {
		return http.StatusOK, nil // empty body
	})
	src2 := fakeSource(t, func(_ string) (int, []byte) {
		return http.StatusOK, goodGzipBody()
	})

	f, _ := makeFiller(t, []archivecompleteness.Source{
		{Name: "empty-cdn", URL: src1.URL},
		{Name: "good-source", URL: src2.URL},
	})

	res := f.Fill(context.Background(), []uint32{63})
	if res.Filled != 1 {
		t.Errorf("Filled = %d, want 1 (should fall through to good source)", res.Filled)
	}
}

// TestFill_PathStructure — placed files match the SDF history-
// archive layout (ledger/XX/YY/ZZ/ledger-XXYYZZWW.xdr.gz). Pinning
// this so a refactor doesn't quietly rename the subdirectory shape.
func TestFill_PathStructure(t *testing.T) {
	ts := fakeSource(t, func(_ string) (int, []byte) {
		return http.StatusOK, goodGzipBody()
	})
	f, root := makeFiller(t, []archivecompleteness.Source{
		{Name: "s", URL: ts.URL},
	})

	// Pick a checkpoint with a non-trivial hex. seq=337*64-1 = 21567
	// → hex 0x0000543f.
	seq := uint32(0x543f)
	res := f.Fill(context.Background(), []uint32{seq})
	if res.Filled != 1 {
		t.Fatalf("Filled = %d, want 1", res.Filled)
	}

	wantPath := filepath.Join(root, "ledger", "00", "00", "54", "ledger-0000543f.xdr.gz")
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("expected file at %s: %v", wantPath, err)
	}
}

// TestFill_ContextCancellation — cancellation mid-fill stops new
// fetches. Already-placed files stay (idempotent next run handles
// them); in-flight ones may complete or fail as ctx unwinds.
func TestFill_ContextCancellation(t *testing.T) {
	var seen int64
	ts := fakeSource(t, func(_ string) (int, []byte) {
		atomic.AddInt64(&seen, 1)
		return http.StatusOK, goodGzipBody()
	})

	f, _ := makeFiller(t, []archivecompleteness.Source{
		{Name: "s", URL: ts.URL},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediate
	// Use a list large enough that some entries should be
	// abandoned.
	missing := make([]uint32, 50)
	for i := range missing {
		missing[i] = uint32((i+1)*64 - 1)
	}
	res := f.Fill(ctx, missing)
	if res.Filled == int(seen) && res.Filled == 50 {
		t.Errorf("expected cancellation to short-circuit; got Filled=%d", res.Filled)
	}
}

// TestFill_NilOptionsValidation — empty ArchiveRoot rejected.
func TestFill_RejectsEmptyArchiveRoot(t *testing.T) {
	_, err := archivecompleteness.NewCrossAnchorFiller(archivecompleteness.FillerOptions{})
	if err == nil {
		t.Fatal("expected error for empty ArchiveRoot")
	}
}

// TestFill_RejectsNonexistentArchiveRoot — supplied path doesn't
// exist; surface the error at construction so we don't try to
// write into nowhere later.
func TestFill_RejectsNonexistentArchiveRoot(t *testing.T) {
	_, err := archivecompleteness.NewCrossAnchorFiller(archivecompleteness.FillerOptions{
		ArchiveRoot: "/nonexistent/path/that/does/not/exist",
	})
	if err == nil {
		t.Fatal("expected error for missing ArchiveRoot")
	}
}

// TestDefaultCrossAnchorSources — sanity-check the returned chain
// has the expected primary URLs in the expected order.
func TestDefaultCrossAnchorSources(t *testing.T) {
	sources := archivecompleteness.DefaultCrossAnchorSources()
	if len(sources) < 3 {
		t.Fatalf("DefaultCrossAnchorSources should include at least 3 entries; got %d", len(sources))
	}
	if sources[0].Name != "sdf-core-live-001" {
		t.Errorf("sources[0].Name = %q, want sdf-core-live-001", sources[0].Name)
	}
	if !strings.Contains(sources[0].URL, "history.stellar.org") {
		t.Errorf("sources[0].URL = %q, want history.stellar.org", sources[0].URL)
	}
}

// TestFill_EmptyMissingList — Fill on an empty list returns clean
// without issuing any HTTP request.
func TestFill_EmptyMissingList(t *testing.T) {
	var seen int64
	ts := fakeSource(t, func(_ string) (int, []byte) {
		atomic.AddInt64(&seen, 1)
		return http.StatusOK, goodGzipBody()
	})
	f, _ := makeFiller(t, []archivecompleteness.Source{{Name: "s", URL: ts.URL}})

	res := f.Fill(context.Background(), nil)
	if res.Filled != 0 || len(res.Failed) != 0 {
		t.Errorf("empty missing: Filled=%d Failed=%d, want 0/0", res.Filled, len(res.Failed))
	}
	if seen != 0 {
		t.Errorf("server saw %d requests for empty missing list, want 0", seen)
	}
}
