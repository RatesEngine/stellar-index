package main

import (
	"strings"
	"testing"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"
)

// TestSplitRange_VariousSizes — pinning the split semantics across
// the corner cases the chunk orchestrator can hit. Final-chunk
// absorbs-remainder is the load-bearing property; tests check both
// the count and the boundary contiguity invariant
// (chunks[i].to + 1 == chunks[i+1].from).
func TestSplitRange_VariousSizes(t *testing.T) {
	tests := []struct {
		name      string
		from, to  uint32
		workers   int
		wantCount int
		wantFirst uint32
		wantLast  uint32
	}{
		{"workers=1 yields one chunk", 100, 200, 1, 1, 100, 200},
		{"workers=0 collapses to 1", 100, 200, 0, 1, 100, 200},
		{"even split", 100, 199, 4, 4, 100, 199},                  // 25 each
		{"uneven split — last absorbs", 100, 200, 4, 4, 100, 200}, // 25/25/25/26
		{"workers > span", 100, 102, 5, 1, 100, 102},
		{"single ledger range", 100, 100, 4, 1, 100, 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitRange(tc.from, tc.to, tc.workers)
			if len(got) != tc.wantCount {
				t.Fatalf("got %d chunks, want %d (chunks=%v)", len(got), tc.wantCount, got)
			}
			if got[0].from != tc.wantFirst {
				t.Errorf("got[0].from = %d, want %d", got[0].from, tc.wantFirst)
			}
			if got[len(got)-1].to != tc.wantLast {
				t.Errorf("got[last].to = %d, want %d", got[len(got)-1].to, tc.wantLast)
			}
			// Contiguity invariant.
			for i := 0; i < len(got)-1; i++ {
				if got[i].to+1 != got[i+1].from {
					t.Errorf("gap between chunk[%d].to=%d and chunk[%d].from=%d",
						i, got[i].to, i+1, got[i+1].from)
				}
			}
		})
	}
}

// hashFrom builds a deterministic Hash for tests so we can express
// "ledger N's hash" in a compact form.
func hashFrom(b byte) sdkxdr.Hash {
	var h sdkxdr.Hash
	h[0] = b
	return h
}

// TestStitchChunks_SingleChunkPasses — a single chunk has no
// boundary to check; stitch must succeed.
func TestStitchChunks_SingleChunkPasses(t *testing.T) {
	results := []chunkResult{
		{Idx: 0, FirstSeq: 100, LastSeq: 199, FirstPrevHash: hashFrom(0x99), LastHash: hashFrom(0xCC), Verified: 100},
	}
	if err := stitchChunks(results); err != nil {
		t.Errorf("single-chunk stitch should succeed; got %v", err)
	}
}

// TestStitchChunks_HappyPath — adjacent chunks where chunk[i].
// LastHash matches chunk[i+1].FirstPrevHash AND seqs are
// contiguous → no error.
func TestStitchChunks_HappyPath(t *testing.T) {
	results := []chunkResult{
		{Idx: 0, FirstSeq: 100, LastSeq: 199, LastHash: hashFrom(0xAA), Verified: 100},
		{Idx: 1, FirstSeq: 200, FirstPrevHash: hashFrom(0xAA), LastSeq: 299, LastHash: hashFrom(0xBB), Verified: 100},
		{Idx: 2, FirstSeq: 300, FirstPrevHash: hashFrom(0xBB), LastSeq: 399, LastHash: hashFrom(0xCC), Verified: 100},
	}
	if err := stitchChunks(results); err != nil {
		t.Errorf("happy-path stitch failed: %v", err)
	}
}

// TestStitchChunks_HashMismatch — chunk[1].FirstPrevHash differs
// from chunk[0].LastHash → error names both chunks + ledger.
func TestStitchChunks_HashMismatch(t *testing.T) {
	results := []chunkResult{
		{Idx: 0, FirstSeq: 100, LastSeq: 199, LastHash: hashFrom(0xAA), Verified: 100},
		{Idx: 1, FirstSeq: 200, FirstPrevHash: hashFrom(0xDD), LastSeq: 299, LastHash: hashFrom(0xBB), Verified: 100},
	}
	err := stitchChunks(results)
	if err == nil {
		t.Fatal("expected boundary-mismatch error; got nil")
	}
	for _, want := range []string{"chunk[0→1]", "chain break", "ledger 199"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q: %v", want, err)
		}
	}
}

// TestStitchChunks_SeqGap — chunk[0].LastSeq + 1 != chunk[1].FirstSeq
// (a missing ledger between chunks) → error.
func TestStitchChunks_SeqGap(t *testing.T) {
	results := []chunkResult{
		{Idx: 0, FirstSeq: 100, LastSeq: 199, LastHash: hashFrom(0xAA), Verified: 100},
		{Idx: 1, FirstSeq: 250, FirstPrevHash: hashFrom(0xAA), LastSeq: 299, LastHash: hashFrom(0xBB), Verified: 50},
	}
	err := stitchChunks(results)
	if err == nil {
		t.Fatal("expected seq-gap error; got nil")
	}
	if !strings.Contains(err.Error(), "boundary gap") {
		t.Errorf("error message lacks 'boundary gap': %v", err)
	}
}

// TestCheckResumeFromHash_HappyPath — operator-supplied hex matches
// the observed FirstPrevHash → no error.
func TestCheckResumeFromHash_HappyPath(t *testing.T) {
	h := hashFrom(0xAA)
	hex := "aa" + strings.Repeat("00", 31)
	if err := checkResumeFromHash(hex, h, 100); err != nil {
		t.Errorf("happy path failed: %v", err)
	}
}

// TestCheckResumeFromHash_Mismatch — operator passed a hash that
// doesn't match the observed FirstPrevHash; error names the seam
// ledger + both hashes so the operator can audit.
func TestCheckResumeFromHash_Mismatch(t *testing.T) {
	h := hashFrom(0xAA)
	wrongHex := "bb" + strings.Repeat("00", 31)
	err := checkResumeFromHash(wrongHex, h, 100)
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	for _, want := range []string{"ledger 100", "boundary mismatch", "FirstPrevHash"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message lacks %q: %v", want, err)
		}
	}
}

// TestCheckResumeFromHash_BadHex — operator typo (non-hex chars,
// wrong length) surfaces as a parse error rather than silently
// passing.
func TestCheckResumeFromHash_BadHex(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"non-hex chars", "zzznotahex"},
		{"odd length", "aab"},
		{"too short", "aabb"},
		{"too long", strings.Repeat("aa", 33)}, // 33 bytes = 66 chars
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkResumeFromHash(tc.in, hashFrom(0xAA), 100); err == nil {
				t.Errorf("expected error for %q; got nil", tc.in)
			}
		})
	}
}

// TestStitchChunks_EmptyChunkSkipped — a chunk that processed zero
// ledgers (no LCMs in its range — uncommon but legal) is skipped
// in the boundary check; the chunks on either side are stitched
// as if the empty chunk weren't there. The stitch only validates
// PRESENT pairs, so this devolves to "no adjacent non-empty pair
// exists" → pass.
func TestStitchChunks_EmptyChunkSkipped(t *testing.T) {
	results := []chunkResult{
		{Idx: 0, FirstSeq: 100, LastSeq: 199, LastHash: hashFrom(0xAA), Verified: 100},
		{Idx: 1, Verified: 0}, // empty chunk
		{Idx: 2, FirstSeq: 300, FirstPrevHash: hashFrom(0xBB), LastSeq: 399, LastHash: hashFrom(0xCC), Verified: 100},
	}
	if err := stitchChunks(results); err != nil {
		t.Errorf("empty-chunk-in-the-middle should not stitch-error; got %v", err)
	}
}
