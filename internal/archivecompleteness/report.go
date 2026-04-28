package archivecompleteness

import (
	"encoding/json"
	"io"
	"time"
)

// Report is the JSON wire shape produced by the `check` mode and
// consumed by the future `fix` mode. Per ADR-0017 PR A this carries
// the read-only enumeration; PR B's `fix` mode reads a Report and
// fetches the missing files via the multi-source fallback chain.
//
// Versioning: the Schema field locks the wire shape. PR B/C may
// add new optional fields; removing or repurposing a field bumps
// Schema and goes through an ADR amendment.
type Report struct {
	// Schema is the report's wire-format version. Stable per PR.
	// Pinned to "1" for PR A.
	Schema string `json:"schema"`

	// ScannedAt is when this report was produced. RFC 3339 UTC.
	ScannedAt time.Time `json:"scanned_at"`

	// Range that was checked across all archives, in ledger
	// sequence numbers.
	Range RangeSpec `json:"range"`

	// CrossAnchor holds the results of the cross-anchor archive
	// scan. Nil when the operator skipped that check via
	// `-checks` selection.
	CrossAnchor *CrossAnchorReportSection `json:"cross_anchor,omitempty"`

	// Primary holds the results of the primary (galexie-archive)
	// scan. Nil when skipped or not yet implemented (PR A leaves
	// this nil; PR B fills it in).
	Primary *PrimaryReportSection `json:"primary,omitempty"`
}

// RangeSpec is a `[from, to]` ledger range, both inclusive.
type RangeSpec struct {
	From uint32 `json:"from"`
	To   uint32 `json:"to"`
}

// CrossAnchorReportSection is the per-archive section for the
// cross-anchor scan.
type CrossAnchorReportSection struct {
	// ArchiveRoot is the absolute path that was scanned. Helps
	// downstream tooling know which mount this report describes.
	ArchiveRoot string `json:"archive_root"`

	// Expected / Found / MissingCount are the same fields from
	// [CrossAnchorResult] surfaced to the wire.
	Expected     int      `json:"expected"`
	Found        int      `json:"found"`
	MissingCount int      `json:"missing_count"`
	Missing      []uint32 `json:"missing,omitempty"`
	Truncated    bool     `json:"truncated,omitempty"`
}

// PrimaryReportSection is the per-archive section for the primary
// galexie-archive scan. PR A leaves this nil; PR B fills it.
type PrimaryReportSection struct {
	BucketName   string `json:"bucket_name"`
	Expected     int    `json:"expected"`
	Found        int    `json:"found"`
	MissingCount int    `json:"missing_count"`
	// MissingRanges holds (start, end) gap ranges as galexie
	// detect-gaps reports them. Inclusive on both ends.
	MissingRanges []GapRange `json:"missing_ranges,omitempty"`
}

// GapRange is one contiguous missing-ledger range.
type GapRange struct {
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

// NewReport returns an empty Report scaffold for the given range.
// Callers populate CrossAnchor / Primary sections as they run each
// scan, then call WriteJSON to emit.
func NewReport(from, to uint32) *Report {
	return &Report{
		Schema:    "1",
		ScannedAt: time.Now().UTC(),
		Range:     RangeSpec{From: from, To: to},
	}
}

// SetCrossAnchor populates the cross-anchor section from a
// [CrossAnchorResult].
func (r *Report) SetCrossAnchor(archiveRoot string, res CrossAnchorResult) {
	r.CrossAnchor = &CrossAnchorReportSection{
		ArchiveRoot:  archiveRoot,
		Expected:     res.Expected,
		Found:        res.Found,
		MissingCount: res.Expected - res.Found,
		Missing:      res.Missing,
		Truncated:    res.Truncated,
	}
}

// WriteJSON encodes the report to w as indented JSON. Returns any
// error from the encoder.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// AnyMissing reports whether either archive section shows missing
// files. Returns false on a clean report or one where every check
// section is nil.
func (r *Report) AnyMissing() bool {
	if r.CrossAnchor != nil && r.CrossAnchor.MissingCount > 0 {
		return true
	}
	if r.Primary != nil && r.Primary.MissingCount > 0 {
		return true
	}
	return false
}
