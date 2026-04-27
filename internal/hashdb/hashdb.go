// Package hashdb is a tiny on-disk record of (ledger_seq → sha256
// of LCM bytes) tuples, used as a drift detector against retroactive
// rewrites of upstream galexie objects.
//
// Motivation. Per ADR-0016 §"Trust model", regions that read galexie
// data from a non-local bucket (R2 from AWS public bucket; R3 from
// Vultr Object Storage) are exposed to a failure mode that R1's
// full-mirror shape isn't: upstream may rewrite a previously-fetched
// ledger's bytes. The bytes can still be internally consistent
// (chain-link hash holds) and can still match SDF's signed history
// (Tier B holds), yet differ from what the region first observed.
// In other words: a Tier A + Tier D pass can succeed against rewritten
// bytes; only a fingerprint of what we *originally* saw catches it.
//
// hashdb is that fingerprint. As the indexer reads each LCM, it
// computes sha256 over the canonical XDR bytes and appends a record.
// A periodic verifier later re-reads the same bucket, recomputes
// sha256, and compares against the recorded value — drift triggers
// alert.
//
// Format. Header (16 bytes) followed by a packed array of fixed-size
// records:
//
//	magic       [8]byte    // "rxhashdb"
//	version     uint32     // file format version (1)
//	startLedger uint32     // ledger of record 0 (records are dense)
//
//	then N records of:
//	  hash      [32]byte   // sha256 of the LCM XDR bytes
//
// The ledger sequence is implicit from the record offset:
// `seq = startLedger + index`. Records are dense — no gaps allowed —
// because galexie buckets cover contiguous ranges. A record of all
// zero bytes is sentinel for "not yet written" and Verify() rejects
// it as ErrMissing rather than treating it as a hash. (sha256(empty)
// is a real value but not realistic for an LCM, so we accept the
// false-zero edge case.)
//
// At ~32 bytes/ledger and ~62 M ledgers on pubnet, a full hashdb is
// ~2 GB — small enough to live alongside the postgres dataset, large
// enough that we don't keep it in process memory. All reads/writes
// are O(1) seeks into the mmap-able file.
//
// Concurrency. A *DB owns the underlying *os.File; concurrent calls
// must be serialised by the caller. The intended usage is one writer
// (the indexer) and one reader (the verify cron) — never both at the
// same time. The file is left in a consistent state across crashes
// because every record is a fixed-size atomic-write candidate
// (sub-page) and the dense-array layout has no inter-record metadata
// to corrupt.
package hashdb

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// File-format constants.
const (
	magic         = "rxhashdb"
	formatVersion = uint32(1)
	headerSize    = 16 // 8 magic + 4 version + 4 startLedger
	recordSize    = 32 // sha256 length
)

// Sentinel errors.
var (
	// ErrOutOfRange is returned when a (Get/Verify/Append) addresses
	// a ledger sequence below the file's startLedger. We refuse
	// rather than silently treat it as record 0.
	ErrOutOfRange = errors.New("hashdb: ledger sequence outside file range")
	// ErrMissing is returned when a ledger has no record yet (the
	// file is sparse beyond the last Append, or the slot is unwritten
	// zeros).
	ErrMissing = errors.New("hashdb: no record for ledger")
	// ErrDrift is returned by Verify when the supplied hash differs
	// from the stored hash. The caller should escalate to ops; this
	// is the alert condition the package exists to detect.
	ErrDrift = errors.New("hashdb: hash drift — recorded != observed")
	// ErrBadMagic is returned when the file header's magic bytes
	// don't match — the file was probably written by something else,
	// or it's truncated.
	ErrBadMagic = errors.New("hashdb: bad magic — not a hashdb file")
	// ErrBadVersion is returned when the file format version is
	// unknown to this build.
	ErrBadVersion = errors.New("hashdb: unsupported file format version")
)

// DB is an open hashdb file. Not safe for concurrent use.
type DB struct {
	f           *os.File
	startLedger uint32
}

// Create opens a fresh hashdb file at path covering ledgers
// [startLedger, +∞). Fails if the file already exists. Creates
// header bytes; no records.
func Create(path string, startLedger uint32) (*DB, error) {
	// #nosec G304 — `path` is operator-supplied (CLI flag /
	// ops-config), not request-derived. Path-traversal protection
	// belongs at the caller's input-validation layer, not here.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("hashdb: create %s: %w", path, err)
	}
	hdr := make([]byte, headerSize)
	copy(hdr[:8], magic)
	binary.BigEndian.PutUint32(hdr[8:12], formatVersion)
	binary.BigEndian.PutUint32(hdr[12:16], startLedger)
	if _, err := f.WriteAt(hdr, 0); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("hashdb: write header: %w", err)
	}
	return &DB{f: f, startLedger: startLedger}, nil
}

// Open opens an existing hashdb file. Returns ErrBadMagic /
// ErrBadVersion on header mismatch.
func Open(path string) (*DB, error) {
	// #nosec G304 — operator-supplied path; see Create() comment.
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("hashdb: open %s: %w", path, err)
	}
	hdr := make([]byte, headerSize)
	if _, err := io.ReadFull(f, hdr); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("hashdb: read header: %w", err)
	}
	if string(hdr[:8]) != magic {
		_ = f.Close()
		return nil, ErrBadMagic
	}
	if v := binary.BigEndian.Uint32(hdr[8:12]); v != formatVersion {
		_ = f.Close()
		return nil, fmt.Errorf("%w: file is v%d, build supports v%d", ErrBadVersion, v, formatVersion)
	}
	start := binary.BigEndian.Uint32(hdr[12:16])
	return &DB{f: f, startLedger: start}, nil
}

// Close releases the underlying file handle. Idempotent.
func (db *DB) Close() error {
	if db.f == nil {
		return nil
	}
	err := db.f.Close()
	db.f = nil
	return err
}

// StartLedger returns the ledger sequence of record 0.
func (db *DB) StartLedger() uint32 { return db.startLedger }

// Hash returns the sha256 of the supplied LCM bytes. Helper so
// callers don't have to import crypto/sha256 just to construct a
// fixed-size value.
func Hash(lcmBytes []byte) [recordSize]byte {
	return sha256.Sum256(lcmBytes)
}

// recordOffset returns the byte offset of the record for `seq`. It
// does not bounds-check against the file's length — that's the
// caller's job.
func (db *DB) recordOffset(seq uint32) (int64, error) {
	if seq < db.startLedger {
		return 0, fmt.Errorf("%w: seq=%d < startLedger=%d", ErrOutOfRange, seq, db.startLedger)
	}
	return int64(headerSize) + int64(seq-db.startLedger)*int64(recordSize), nil
}

// Append writes the hash for ledger seq. The file is extended (with
// zero-padding for any skipped slots) when seq is past the current
// EOF — but that's an antipattern: callers should write contiguously.
// If you must skip, the gap reads back as ErrMissing.
func (db *DB) Append(seq uint32, h [recordSize]byte) error {
	off, err := db.recordOffset(seq)
	if err != nil {
		return err
	}
	if _, err := db.f.WriteAt(h[:], off); err != nil {
		return fmt.Errorf("hashdb: write record seq=%d: %w", seq, err)
	}
	return nil
}

// Get returns the recorded hash for ledger seq. Returns ErrMissing
// when the slot is past EOF or all-zero.
func (db *DB) Get(seq uint32) ([recordSize]byte, error) {
	var out [recordSize]byte
	off, err := db.recordOffset(seq)
	if err != nil {
		return out, err
	}
	n, err := db.f.ReadAt(out[:], off)
	if errors.Is(err, io.EOF) || (err == nil && n < recordSize) {
		return out, fmt.Errorf("%w: seq=%d (file truncated or never appended)", ErrMissing, seq)
	}
	if err != nil {
		return out, fmt.Errorf("hashdb: read record seq=%d: %w", seq, err)
	}
	if out == ([recordSize]byte{}) {
		return out, fmt.Errorf("%w: seq=%d (slot unwritten)", ErrMissing, seq)
	}
	return out, nil
}

// Verify compares observed against the stored hash for seq. Returns
// nil on match, ErrDrift on mismatch, ErrMissing when no record
// exists yet. Callers that want "record on first sight, verify
// thereafter" semantics should fall back to Append on ErrMissing.
func (db *DB) Verify(seq uint32, observed [recordSize]byte) error {
	stored, err := db.Get(seq)
	if err != nil {
		return err
	}
	if stored != observed {
		return fmt.Errorf("%w: seq=%d stored=%x observed=%x", ErrDrift, seq, stored[:8], observed[:8])
	}
	return nil
}
