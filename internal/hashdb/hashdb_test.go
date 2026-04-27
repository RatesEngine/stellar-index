package hashdb

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mkPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "drift.db")
}

// TestRoundTrip is the happy path: Create → Append → Close → Open →
// Get returns the same hash bytes. Confirms the on-disk layout
// survives a close-and-reopen cycle.
func TestRoundTrip(t *testing.T) {
	path := mkPath(t)

	db, err := Create(path, 1000)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	hashA := Hash([]byte("ledger-1000-bytes"))
	hashB := Hash([]byte("ledger-1001-bytes"))
	if err := db.Append(1000, hashA); err != nil {
		t.Fatalf("Append 1000: %v", err)
	}
	if err := db.Append(1001, hashB); err != nil {
		t.Fatalf("Append 1001: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if got := db.StartLedger(); got != 1000 {
		t.Errorf("StartLedger after reopen = %d, want 1000", got)
	}
	got, err := db.Get(1000)
	if err != nil {
		t.Fatalf("Get 1000: %v", err)
	}
	if got != hashA {
		t.Errorf("Get(1000) returned different hash than Append wrote")
	}
	got, err = db.Get(1001)
	if err != nil {
		t.Fatalf("Get 1001: %v", err)
	}
	if got != hashB {
		t.Errorf("Get(1001) returned different hash than Append wrote")
	}
}

// TestVerify_OK is the steady-state case: re-reading the same bytes
// later (e.g. periodic verifier) hashes to the same value, Verify
// returns nil. This is the *most-common* code path in production —
// drift is rare; agreement is the norm.
func TestVerify_OK(t *testing.T) {
	db, err := Create(mkPath(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bytesAtSeq42 := []byte("the-canonical-LCM-bytes-for-42")
	h := Hash(bytesAtSeq42)
	if err := db.Append(42, h); err != nil {
		t.Fatal(err)
	}

	// Re-hash the same bytes; Verify must say nil.
	if err := db.Verify(42, Hash(bytesAtSeq42)); err != nil {
		t.Errorf("Verify on identical bytes returned error: %v", err)
	}
}

// TestVerify_DriftDetected is the alert path: same ledger seq, but
// the bytes have changed. Verify must return ErrDrift so the caller
// can escalate. This is the exact scenario the package exists to
// catch — AWS public bucket retroactively rewrote a previously-fetched
// ledger, or local corruption silently flipped bits.
func TestVerify_DriftDetected(t *testing.T) {
	db, err := Create(mkPath(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	original := []byte("ledger-7-original-bytes")
	rewritten := []byte("ledger-7-DRIFT-bytes")

	if err := db.Append(7, Hash(original)); err != nil {
		t.Fatal(err)
	}

	err = db.Verify(7, Hash(rewritten))
	if !errors.Is(err, ErrDrift) {
		t.Fatalf("Verify on drift returned %v, want ErrDrift", err)
	}
	// Error message must include the seq number so ops can find it.
	if !bytes.Contains([]byte(err.Error()), []byte("seq=7")) {
		t.Errorf("ErrDrift message should include 'seq=7', got: %v", err)
	}
}

// TestGet_Missing covers the cold-cache path: a Verify against a
// ledger that has never been recorded must return ErrMissing — NOT
// ErrDrift. The caller uses ErrMissing as the signal "record this
// hash for next time" rather than "the world is on fire".
func TestGet_Missing(t *testing.T) {
	db, err := Create(mkPath(t), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Append nothing, then ask for something inside the file's range.
	_, err = db.Get(150)
	if !errors.Is(err, ErrMissing) {
		t.Errorf("Get on never-written seq returned %v, want ErrMissing", err)
	}

	// Same for Verify — must surface ErrMissing, not ErrDrift.
	err = db.Verify(150, Hash([]byte("anything")))
	if !errors.Is(err, ErrMissing) {
		t.Errorf("Verify on never-written seq returned %v, want ErrMissing", err)
	}
}

// TestGet_Sparse covers the post-skip case: appending at seq=200
// when start=100 leaves slots 100..199 unwritten. Reads in the gap
// must return ErrMissing (the all-zeros sentinel). Reads at 200
// return the real hash.
func TestGet_Sparse(t *testing.T) {
	db, err := Create(mkPath(t), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	h := Hash([]byte("seq-200"))
	if err := db.Append(200, h); err != nil {
		t.Fatal(err)
	}

	// Slot 150 is in the file's data range but unwritten.
	_, err = db.Get(150)
	if !errors.Is(err, ErrMissing) {
		t.Errorf("Get on unwritten interior slot returned %v, want ErrMissing", err)
	}

	// Slot 200 is the real record.
	got, err := db.Get(200)
	if err != nil {
		t.Fatalf("Get on written slot: %v", err)
	}
	if got != h {
		t.Errorf("Get(200) returned different hash than Append wrote")
	}
}

// TestOutOfRange covers the "operator passed a wrong from" guard.
// startLedger=1000, then asking about seq=999 returns ErrOutOfRange
// rather than silently aliasing into the header bytes.
func TestOutOfRange(t *testing.T) {
	db, err := Create(mkPath(t), 1000)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Append(999, Hash([]byte("x"))); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("Append below startLedger returned %v, want ErrOutOfRange", err)
	}
	if _, err := db.Get(999); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("Get below startLedger returned %v, want ErrOutOfRange", err)
	}
}

// TestOpen_BadMagic catches the "operator pointed at the wrong file"
// case. Even a same-size file with garbage bytes must fail Open
// rather than silently treat the garbage as a header + records.
func TestOpen_BadMagic(t *testing.T) {
	path := mkPath(t)
	// Write 16 bytes of garbage as a fake header.
	junk := bytes.Repeat([]byte{0xab}, headerSize)
	if err := writeFile(path, junk); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if !errors.Is(err, ErrBadMagic) {
		t.Errorf("Open on bad magic returned %v, want ErrBadMagic", err)
	}
}

// TestOpen_BadVersion ensures forward-compat fails loud. A future
// hashdb v2 file opened by a v1 build must surface ErrBadVersion
// rather than be misread as v1.
func TestOpen_BadVersion(t *testing.T) {
	path := mkPath(t)
	hdr := make([]byte, headerSize)
	copy(hdr[:8], magic)
	// Version 99 — far enough in the future to be definitively wrong.
	hdr[8], hdr[9], hdr[10], hdr[11] = 0, 0, 0, 99
	if err := writeFile(path, hdr); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if !errors.Is(err, ErrBadVersion) {
		t.Errorf("Open on bad version returned %v, want ErrBadVersion", err)
	}
}

// TestCreate_ExclusiveFails locks down the fail-on-exists semantics
// of Create — operators must not silently truncate an existing
// hashdb just because they re-ran a populate command. They have to
// delete the file deliberately first.
func TestCreate_ExclusiveFails(t *testing.T) {
	path := mkPath(t)
	db, err := Create(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	if _, err := Create(path, 0); err == nil {
		t.Error("Create on existing path returned nil — must fail-exclusive")
	}
}

// writeFile is a tiny helper used by the bad-header tests.
func writeFile(path string, b []byte) error {
	return os.WriteFile(path, b, 0o644)
}
