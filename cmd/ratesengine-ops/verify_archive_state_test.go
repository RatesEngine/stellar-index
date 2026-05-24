package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"
)

// TestReadVerifyArchiveState_missingFileReturnsZero confirms the
// first-run path: no state file yet means we get an empty state
// without error, so the caller can compute -from from explicit
// flags only.
func TestReadVerifyArchiveState_missingFileReturnsZero(t *testing.T) {
	t.Parallel()
	got, err := readVerifyArchiveState(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("err = %v, want nil for missing file", err)
	}
	if got.Tiers == nil {
		t.Errorf("Tiers nil — want empty map for caller's safety")
	}
	if len(got.Tiers) != 0 {
		t.Errorf("Tiers should be empty, got %d entries", len(got.Tiers))
	}
}

// TestReadVerifyArchiveState_emptyPathReturnsZero covers the
// state-file-disabled path: empty path means the operator opted out
// of incremental, so we return empty state without error.
func TestReadVerifyArchiveState_emptyPathReturnsZero(t *testing.T) {
	t.Parallel()
	got, err := readVerifyArchiveState("")
	if err != nil {
		t.Fatalf("err = %v, want nil for empty path", err)
	}
	if got.Tiers == nil || len(got.Tiers) != 0 {
		t.Errorf("Tiers = %v, want empty map", got.Tiers)
	}
}

// TestWriteRead_roundTrip exercises the atomic-rename write path
// + read parse. Uses t.TempDir so cleanup is automatic.
func TestWriteRead_roundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	want := VerifyArchiveState{
		Tiers: map[string]VerifyArchiveTierState{
			"chain": {
				LastVerifiedLedger: 60_000_000,
				LastVerifiedAt:     time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC),
				LastVerifiedHash:   "abc123",
			},
		},
	}
	if err := writeVerifyArchiveState(path, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Verify the .tmp file is gone (atomic rename happened).
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp still present, atomic rename didn't fire")
	}
	got, err := readVerifyArchiveState(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Tiers["chain"].LastVerifiedLedger != want.Tiers["chain"].LastVerifiedLedger {
		t.Errorf("LastVerifiedLedger = %d, want %d",
			got.Tiers["chain"].LastVerifiedLedger,
			want.Tiers["chain"].LastVerifiedLedger)
	}
	if got.Tiers["chain"].LastVerifiedHash != "abc123" {
		t.Errorf("LastVerifiedHash = %q, want abc123", got.Tiers["chain"].LastVerifiedHash)
	}
}

// TestWriteVerifyArchiveState_createsParentDir covers the
// mkdir -p semantics. Operator should never have to pre-create
// /var/lib/ratesengine for the write to succeed.
func TestWriteVerifyArchiveState_createsParentDir(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()
	nested := filepath.Join(tmpdir, "deeply", "nested", "dir", "state.json")
	if err := writeVerifyArchiveState(nested, VerifyArchiveState{}); err != nil {
		t.Fatalf("write to nested path: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

// TestResolveIncrementalFrom covers the four interesting branches:
// no prior state, fresh prior state, prior > overlap (normal case),
// prior < overlap (would underflow without floor).
func TestResolveIncrementalFrom(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		state         VerifyArchiveState
		tier          string
		explicitFrom  uint32
		safetyOverlap uint32
		want          uint32
	}{
		{
			name:          "no prior state → use explicit -from",
			state:         VerifyArchiveState{Tiers: map[string]VerifyArchiveTierState{}},
			tier:          "chain",
			explicitFrom:  2,
			safetyOverlap: 5000,
			want:          2,
		},
		{
			name: "prior < overlap → floor to ledger 2",
			state: VerifyArchiveState{Tiers: map[string]VerifyArchiveTierState{
				"chain": {LastVerifiedLedger: 1000},
			}},
			tier:          "chain",
			explicitFrom:  2,
			safetyOverlap: 5000,
			want:          2,
		},
		{
			name: "prior - overlap > explicit → use computed",
			state: VerifyArchiveState{Tiers: map[string]VerifyArchiveTierState{
				"chain": {LastVerifiedLedger: 60_000_000},
			}},
			tier:          "chain",
			explicitFrom:  2,
			safetyOverlap: 5000,
			want:          59_995_000,
		},
		{
			name: "explicit -from greater than computed → operator wins",
			state: VerifyArchiveState{Tiers: map[string]VerifyArchiveTierState{
				"chain": {LastVerifiedLedger: 60_000_000},
			}},
			tier:          "chain",
			explicitFrom:  60_000_000, // operator wants only the most recent
			safetyOverlap: 5000,
			want:          60_000_000,
		},
		{
			name: "different tier → falls through to explicit",
			state: VerifyArchiveState{Tiers: map[string]VerifyArchiveTierState{
				"chain": {LastVerifiedLedger: 60_000_000},
			}},
			tier:          "checkpoint",
			explicitFrom:  100,
			safetyOverlap: 5000,
			want:          100,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveIncrementalFrom(tc.state, tc.tier, tc.explicitFrom, tc.safetyOverlap)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestUpdateTierState confirms the only-advances-forward semantics:
// a new run that covers a lower ledger range than prior state DOES
// NOT regress the high-water mark.
func TestUpdateTierState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC)
	prior := VerifyArchiveState{Tiers: map[string]VerifyArchiveTierState{
		"chain": {LastVerifiedLedger: 60_000_000, LastVerifiedHash: "old-hash"},
	}}

	// Advance: new high beats prior.
	advanced := updateTierState(prior, "chain", 62_000_000, "new-hash", now)
	if advanced.Tiers["chain"].LastVerifiedLedger != 62_000_000 {
		t.Errorf("after advance: LastVerifiedLedger = %d, want 62M",
			advanced.Tiers["chain"].LastVerifiedLedger)
	}
	if advanced.Tiers["chain"].LastVerifiedHash != "new-hash" {
		t.Errorf("hash not updated")
	}

	// Regression: new < prior is a no-op.
	noregress := updateTierState(prior, "chain", 50_000_000, "regression-hash", now)
	if noregress.Tiers["chain"].LastVerifiedLedger != 60_000_000 {
		t.Errorf("after regression attempt: LastVerifiedLedger = %d, want 60M (no regression)",
			noregress.Tiers["chain"].LastVerifiedLedger)
	}
	if noregress.Tiers["chain"].LastVerifiedHash != "old-hash" {
		t.Errorf("hash got overwritten on regression attempt")
	}

	// New tier on empty prior.
	freshTier := updateTierState(VerifyArchiveState{}, "checkpoint", 1000, "", now)
	if freshTier.Tiers["checkpoint"].LastVerifiedLedger != 1000 {
		t.Errorf("new tier insert: LastVerifiedLedger = %d, want 1000",
			freshTier.Tiers["checkpoint"].LastVerifiedLedger)
	}

	// updateTierState clears InProgress unconditionally — the
	// post-run end-of-walk signal. Even a no-advance update should
	// wipe in-flight scratch.
	withInProgress := VerifyArchiveState{Tiers: map[string]VerifyArchiveTierState{
		"chain": {
			LastVerifiedLedger: 60_000_000,
			InProgress: &RunProgress{
				From: 1, To: 100, Workers: 2,
				Chunks: []ChunkProgress{{Idx: 0, Done: true}, {Idx: 1, Done: false}},
			},
		},
	}}
	cleared := updateTierState(withInProgress, "chain", 60_000_000, "", now)
	if cleared.Tiers["chain"].InProgress != nil {
		t.Errorf("updateTierState should clear InProgress; got %+v",
			cleared.Tiers["chain"].InProgress)
	}
	if withInProgress.Tiers["chain"].InProgress == nil {
		t.Errorf("updateTierState mutated the input's InProgress")
	}
}

// TestStartTierProgress seeds the per-chunk tracking the resume path
// reads on the next run.
func TestStartTierProgress(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	chunks := []rangeChunk{
		{from: 2, to: 1_000_000},
		{from: 1_000_001, to: 2_000_000},
		{from: 2_000_001, to: 3_000_000},
	}
	st := startTierProgress(VerifyArchiveState{}, "chain", 2, 3_000_000, 3, chunks, now)

	rp := st.Tiers["chain"].InProgress
	if rp == nil {
		t.Fatalf("InProgress not set")
	}
	if rp.From != 2 || rp.To != 3_000_000 || rp.Workers != 3 {
		t.Errorf("plan = (%d,%d,%d), want (2, 3000000, 3)", rp.From, rp.To, rp.Workers)
	}
	if len(rp.Chunks) != 3 {
		t.Fatalf("got %d chunk progresses, want 3", len(rp.Chunks))
	}
	for i, cp := range rp.Chunks {
		if cp.Idx != i || cp.From != chunks[i].from || cp.To != chunks[i].to {
			t.Errorf("chunk %d = %+v, want from=%d to=%d", i, cp, chunks[i].from, chunks[i].to)
		}
		if cp.Done {
			t.Errorf("chunk %d Done=true at seed time", i)
		}
	}
}

// TestMarkChunkDone flips the right chunk + records its terminal
// hash. Non-target chunks stay untouched.
func TestMarkChunkDone(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	chunks := []rangeChunk{{from: 2, to: 1_000_000}, {from: 1_000_001, to: 2_000_000}}
	st := startTierProgress(VerifyArchiveState{}, "chain", 2, 2_000_000, 2, chunks, now)

	var h sdkxdr.Hash
	for i := range h {
		h[i] = byte(i)
	}
	st = markChunkDone(st, "chain", 1, h, now.Add(time.Hour))

	rp := st.Tiers["chain"].InProgress
	if rp.Chunks[0].Done {
		t.Errorf("chunk 0 should still be Done=false")
	}
	if !rp.Chunks[1].Done {
		t.Errorf("chunk 1 should be Done=true")
	}
	wantHex := hashToHex(h)
	if rp.Chunks[1].LastVerifiedHash != wantHex {
		t.Errorf("chunk 1 LastVerifiedHash = %q, want %q", rp.Chunks[1].LastVerifiedHash, wantHex)
	}

	// Out-of-range idx is a no-op (defensive — caller shouldn't but
	// we don't want a crash on a stale callback).
	stOOB := markChunkDone(st, "chain", 99, h, now)
	if stOOB.Tiers["chain"].InProgress.Chunks[1].Done != true {
		t.Errorf("out-of-range markChunkDone disturbed unrelated state")
	}
}

// TestResumeChunks_planMatch returns just the not-Done chunks when
// the prior run's plan matches the current.
func TestResumeChunks_planMatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	chunks := []rangeChunk{
		{from: 2, to: 1_000_000},
		{from: 1_000_001, to: 2_000_000},
		{from: 2_000_001, to: 3_000_000},
	}
	st := startTierProgress(VerifyArchiveState{}, "chain", 2, 3_000_000, 3, chunks, now)
	var h sdkxdr.Hash
	st = markChunkDone(st, "chain", 0, h, now)
	st = markChunkDone(st, "chain", 1, h, now)
	// chunk 2 remains undone

	got, idxs, reason := resumeChunks(st, "chain", 2, 3_000_000, 3, chunks)
	if len(got) != 1 {
		t.Fatalf("got %d chunks to resume, want 1 (only chunk 2 is undone)", len(got))
	}
	if got[0] != chunks[2] {
		t.Errorf("resumed chunk = %+v, want %+v", got[0], chunks[2])
	}
	if len(idxs) != 1 || idxs[0] != 2 {
		t.Errorf("idxs = %v, want [2]", idxs)
	}
	if !strings.Contains(reason, "resumed 1 of 3 chunks") || !strings.Contains(reason, "2 already Done") {
		t.Errorf("reason = %q, expected 'resumed 1 of 3 chunks, 2 already Done'", reason)
	}
}

// TestResumeChunks_allDone collapses to a no-op signal when every
// chunk in the prior plan is Done.
func TestResumeChunks_allDone(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	chunks := []rangeChunk{{from: 2, to: 1_000_000}, {from: 1_000_001, to: 2_000_000}}
	st := startTierProgress(VerifyArchiveState{}, "chain", 2, 2_000_000, 2, chunks, now)
	var h sdkxdr.Hash
	st = markChunkDone(st, "chain", 0, h, now)
	st = markChunkDone(st, "chain", 1, h, now)

	got, _, reason := resumeChunks(st, "chain", 2, 2_000_000, 2, chunks)
	if got != nil {
		t.Errorf("got %d chunks, want nil (all Done)", len(got))
	}
	if !strings.Contains(reason, "all 2 chunks already Done") {
		t.Errorf("reason = %q, expected 'all 2 chunks already Done…'", reason)
	}
}

// TestResumeChunks_planDiffers ignores prior progress when from /
// to / workers / chunk-count don't match exactly (operator changed
// flags between runs, or live ingest moved tip).
func TestResumeChunks_planDiffers(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	priorChunks := []rangeChunk{{from: 2, to: 1_000_000}, {from: 1_000_001, to: 2_000_000}}
	st := startTierProgress(VerifyArchiveState{}, "chain", 2, 2_000_000, 2, priorChunks, now)
	var h sdkxdr.Hash
	st = markChunkDone(st, "chain", 0, h, now)

	// Same chunks, but workers different — plan mismatch.
	got, idxs, reason := resumeChunks(st, "chain", 2, 2_000_000, 4, priorChunks)
	if len(got) != len(priorChunks) {
		t.Errorf("plan mismatch should ignore prior; got %d chunks, want %d", len(got), len(priorChunks))
	}
	if len(idxs) != len(priorChunks) {
		t.Errorf("idxs = %v, want identity over %d", idxs, len(priorChunks))
	}
	if !strings.Contains(reason, "plan differs") {
		t.Errorf("reason = %q, expected to mention plan-differs", reason)
	}
}

// TestResumeChunks_noPriorState is the first-ever run case.
func TestResumeChunks_noPriorState(t *testing.T) {
	t.Parallel()
	chunks := []rangeChunk{{from: 2, to: 1_000_000}, {from: 1_000_001, to: 2_000_000}}
	got, idxs, reason := resumeChunks(VerifyArchiveState{Tiers: map[string]VerifyArchiveTierState{}}, "chain", 2, 2_000_000, 2, chunks)
	if len(got) != 2 || len(idxs) != 2 || idxs[0] != 0 || idxs[1] != 1 {
		t.Errorf("first run should return all chunks with identity idxs; got chunks=%d idxs=%v", len(got), idxs)
	}
	if !strings.Contains(reason, "no prior in-progress") {
		t.Errorf("reason = %q, expected 'no prior in-progress…'", reason)
	}
}

// TestState_jsonRoundTripWithInProgress confirms the new InProgress
// field survives marshal+unmarshal, and that absence (legacy state
// files) parses cleanly as nil.
func TestState_jsonRoundTripWithInProgress(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	chunks := []rangeChunk{{from: 2, to: 1_000_000}, {from: 1_000_001, to: 2_000_000}}
	st := startTierProgress(VerifyArchiveState{}, "chain", 2, 2_000_000, 2, chunks, now)
	var h sdkxdr.Hash
	for i := range h {
		h[i] = byte(0xAB)
	}
	st = markChunkDone(st, "chain", 0, h, now)

	tmp := t.TempDir() + "/state.json"
	if err := writeVerifyArchiveState(tmp, st); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readVerifyArchiveState(tmp)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	rp := got.Tiers["chain"].InProgress
	if rp == nil {
		t.Fatalf("round-trip lost InProgress")
	}
	if rp.From != 2 || rp.To != 2_000_000 || rp.Workers != 2 || len(rp.Chunks) != 2 {
		t.Errorf("round-trip plan drift: %+v", rp)
	}
	if !rp.Chunks[0].Done || rp.Chunks[1].Done {
		t.Errorf("round-trip Done flags drifted: %+v", rp.Chunks)
	}

	// Legacy state file (no in_progress key) parses with InProgress = nil.
	legacyJSON := `{"tiers":{"chain":{"last_verified_ledger":42,"last_verified_at":"2026-05-24T12:00:00Z","last_verified_hash":"abc"}}}`
	legacyPath := t.TempDir() + "/legacy.json"
	if err := os.WriteFile(legacyPath, []byte(legacyJSON), 0o600); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	legacy, err := readVerifyArchiveState(legacyPath)
	if err != nil {
		t.Fatalf("read legacy: %v", err)
	}
	if legacy.Tiers["chain"].InProgress != nil {
		t.Errorf("legacy file should yield nil InProgress; got %+v", legacy.Tiers["chain"].InProgress)
	}
	if legacy.Tiers["chain"].LastVerifiedLedger != 42 {
		t.Errorf("legacy file LastVerifiedLedger = %d, want 42", legacy.Tiers["chain"].LastVerifiedLedger)
	}
}
