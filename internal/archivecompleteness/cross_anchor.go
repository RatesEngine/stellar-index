package archivecompleteness

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// CrossAnchorChecker scans a Stellar history-archive layout under
// archiveRoot and reports missing per-checkpoint files.
//
// File path scheme matches what `verify-archive -tier checkpoint`
// reads:
//
//	<archiveRoot>/ledger/XX/YY/ZZ/ledger-XXYYZZWW.xdr.gz
//
// where XXYYZZWW is the 8-char hex-encoded checkpoint sequence
// (every 64 ledgers; seq % 64 == 63).
type CrossAnchorChecker struct {
	archiveRoot string
}

// NewCrossAnchorChecker returns a checker that scans the supplied
// archiveRoot. The directory must exist; missing directories /
// permission errors return on the first Check call.
func NewCrossAnchorChecker(archiveRoot string) *CrossAnchorChecker {
	return &CrossAnchorChecker{archiveRoot: archiveRoot}
}

// CrossAnchorResult is the output of [CrossAnchorChecker.Check].
type CrossAnchorResult struct {
	// Range that was scanned. For the typical case this is
	// `[2, network_head]`.
	From uint32
	To   uint32

	// Expected is the count of checkpoint positions in the range.
	// One per `seq % 64 == 63` slot, so `(To - From) / 64 + 1` for
	// a properly-aligned range.
	Expected int

	// Found is the count of checkpoint files actually present on
	// disk. Equal to Expected when the archive is complete.
	Found int

	// Missing lists the checkpoint ledger sequences whose file is
	// absent. Sorted ascending. Empty when the archive is complete.
	// Bounded by MaxMissingReported to keep payloads sane on
	// disaster scenarios — the count always reflects the true gap
	// even when the list is truncated (use Truncated to detect).
	Missing []uint32

	// Truncated is true when the actual missing-count exceeds
	// MaxMissingReported. The list above is partial in that case;
	// the Found / Expected counts are still authoritative.
	Truncated bool
}

// MaxMissingReported caps the size of CrossAnchorResult.Missing
// so a catastrophic gap doesn't produce a multi-megabyte report.
// 65,536 = one full partition's worth of checkpoints — large
// enough that any realistic gap fits, small enough that the JSON
// stays under a few MB.
const MaxMissingReported = 65536

// Check walks the expected checkpoint positions in [from, to] and
// returns a [CrossAnchorResult] describing what's present vs
// missing. Read-only — never mutates the archive.
//
// from and to are inclusive ledger sequences. The check enumerates
// `seq` such that `from <= seq <= to AND seq % 64 == 63`. If from
// or to don't align to a checkpoint boundary the check uses the
// nearest enclosing checkpoint pair (seq=63 is the first checkpoint;
// the last is `(to/64)*64 + 63` if that's still <= to, else the one
// before it).
//
// Returns an error only when archiveRoot itself is unreadable; a
// missing individual checkpoint file is recorded in Missing, not
// returned as an error.
func (c *CrossAnchorChecker) Check(from, to uint32) (CrossAnchorResult, error) {
	if c.archiveRoot == "" {
		return CrossAnchorResult{}, errors.New("archivecompleteness: archiveRoot is required")
	}
	if to < from {
		return CrossAnchorResult{}, fmt.Errorf("archivecompleteness: to (%d) < from (%d)", to, from)
	}

	// Confirm archiveRoot is readable before walking.
	st, err := os.Stat(c.archiveRoot)
	if err != nil {
		return CrossAnchorResult{}, fmt.Errorf("archivecompleteness: stat archiveRoot %q: %w", c.archiveRoot, err)
	}
	if !st.IsDir() {
		return CrossAnchorResult{}, fmt.Errorf("archivecompleteness: archiveRoot %q is not a directory", c.archiveRoot)
	}

	firstCP := alignFirstCheckpoint(from)
	lastCP := alignLastCheckpoint(to)

	if firstCP > lastCP {
		// The supplied range contains no checkpoint position. Empty
		// result, no error — the caller's range was just too narrow.
		return CrossAnchorResult{From: from, To: to}, nil
	}

	res := CrossAnchorResult{From: from, To: to}
	res.Expected = int((lastCP-firstCP)/64) + 1

	for seq := firstCP; seq <= lastCP; seq += 64 {
		path := checkpointPath(c.archiveRoot, seq)
		_, err := os.Stat(path)
		switch {
		case err == nil:
			res.Found++
		case errors.Is(err, fs.ErrNotExist):
			if len(res.Missing) < MaxMissingReported {
				res.Missing = append(res.Missing, seq)
			} else if !res.Truncated {
				res.Truncated = true
			}
		default:
			// Unexpected I/O error mid-walk — abort and surface;
			// we can't trust counts past this point.
			return res, fmt.Errorf("archivecompleteness: stat %q: %w", path, err)
		}
	}
	return res, nil
}

// alignFirstCheckpoint returns the smallest checkpoint sequence
// that is >= from. Checkpoints sit at seq = 63, 127, 191, ... (i.e.
// seq % 64 == 63).
func alignFirstCheckpoint(from uint32) uint32 {
	rem := from % 64
	if rem == 63 {
		return from
	}
	if rem < 63 {
		return from - rem + 63
	}
	// rem > 63 is impossible for uint32 % 64; included for clarity.
	return from + (64 - rem) + 63
}

// alignLastCheckpoint returns the largest checkpoint sequence that
// is <= to.
func alignLastCheckpoint(to uint32) uint32 {
	rem := to % 64
	if rem == 63 {
		return to
	}
	if rem >= 63 || rem < 63 {
		// rem < 63 → previous checkpoint is to - rem - 1
		// (or to - rem + 63 - 64 if you prefer)
		// Both forms equivalent; keep as a single branch.
		if to < 63 {
			// No checkpoint at or before `to` — return 0 as a
			// sentinel; the caller's firstCP > lastCP guard
			// handles this cleanly.
			return 0
		}
		return to - rem - 1
	}
	return 0
}

// checkpointPath returns the on-disk path for a checkpoint file.
func checkpointPath(archiveRoot string, seq uint32) string {
	hex := hexSeq8(seq)
	return filepath.Join(archiveRoot, "ledger", hex[0:2], hex[2:4], hex[4:6], "ledger-"+hex+".xdr.gz")
}

// hexSeq8 returns an 8-char lowercase hex string for the sequence,
// matching the format SDF uses in history-archive paths.
func hexSeq8(seq uint32) string {
	const digits = "0123456789abcdef"
	var b [8]byte
	for i := 7; i >= 0; i-- {
		b[i] = digits[seq&0xf]
		seq >>= 4
	}
	return string(b[:])
}
