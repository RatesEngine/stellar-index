package archivecompleteness

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// MetricsSnapshot is the per-run set of metrics we emit. Field
// names match the alert rules in
// `deploy/monitoring/rules/archive-completeness.yml`; renaming any
// field is a wire break against the alert rule file.
type MetricsSnapshot struct {
	// FilesMissing per archive (`cross-anchor` for now; PR D adds
	// `galexie-archive`). Caller fills the map; absent archives
	// produce no metric line.
	FilesMissing map[string]int

	// RunDurationSeconds is wall-clock for the whole verify run.
	RunDurationSeconds float64

	// LastSuccessTimestamp is set to the run's start time when
	// the run completed cleanly (no missing files left). Zero when
	// the run finished with residuals — the gauge stays at the
	// previous successful value, which is what alerts on
	// `_last_success_timestamp older than 26h` consume.
	LastSuccessTimestamp time.Time

	// RepairAttempts / RepairFailures per source. Cumulative
	// counters within this run; the textfile writer expects the
	// caller to track lifetime values across runs (or accepts
	// per-run snapshots and lets prometheus-rate handle it).
	RepairAttempts map[string]int
	RepairFailures map[string]int
}

// NewMetricsSnapshot returns an initialised snapshot ready to be
// populated by Run callers.
func NewMetricsSnapshot() *MetricsSnapshot {
	return &MetricsSnapshot{
		FilesMissing:   map[string]int{},
		RepairAttempts: map[string]int{},
		RepairFailures: map[string]int{},
	}
}

// WriteTextfile renders snapshot to w in Prometheus exposition
// format (the format node_exporter's textfile collector reads).
//
// Operator workflow per ADR-0017 §"Prometheus surface":
//
//   - systemd timer runs `archive-completeness verify -textfile-output PATH`
//   - PATH points at node_exporter's textfile_collector directory
//     (e.g. `/var/lib/node_exporter/textfile_collector/archive_completeness.prom`)
//   - node_exporter scrapes the directory and exposes the metrics
//     on its standard /metrics endpoint
//   - Prometheus scrapes node_exporter; alerts in
//     `deploy/monitoring/rules/archive-completeness.yml` fire on
//     the threshold conditions
//
// Atomic write protocol: the caller writes to a `<PATH>.tmp` file
// first, then renames into place. node_exporter's textfile
// collector treats partial writes as a parse error and skips them,
// so renaming-after-write avoids a race where the scrape sees a
// truncated metric block.
func WriteTextfile(w io.Writer, snapshot *MetricsSnapshot) error {
	if snapshot == nil {
		return nil
	}
	if err := writeFilesMissing(w, snapshot); err != nil {
		return err
	}
	if err := writeLastSuccess(w, snapshot); err != nil {
		return err
	}
	if err := writeRunDuration(w, snapshot); err != nil {
		return err
	}
	if err := writeCounter(w,
		"archive_completeness_repair_attempts_total",
		"Per-source repair attempts in this run.",
		"source", snapshot.RepairAttempts); err != nil {
		return err
	}
	return writeCounter(w,
		"archive_completeness_repair_failures_total",
		"Per-source repair failures in this run.",
		"source", snapshot.RepairFailures)
}

// writeFilesMissing emits the per-archive `archive_files_missing`
// gauge block. Help/Type emitted unconditionally so the file is a
// well-formed Prometheus textfile even with an empty map (the
// scrape sees the type declaration with no samples — valid).
func writeFilesMissing(w io.Writer, snapshot *MetricsSnapshot) error {
	if _, err := io.WriteString(w,
		"# HELP archive_files_missing Count of missing files in the named archive (ADR-0017).\n"+
			"# TYPE archive_files_missing gauge\n"); err != nil {
		return err
	}
	for _, archive := range sortedKeys(snapshot.FilesMissing) {
		if _, err := fmt.Fprintf(w,
			"archive_files_missing{archive=%q} %d\n",
			archive, snapshot.FilesMissing[archive]); err != nil {
			return err
		}
	}
	return nil
}

// writeLastSuccess emits the last_success_timestamp gauge when a
// successful run has occurred. Skip entirely otherwise so node_exporter
// surfaces the previous-scrape value (the staleness alert keys on
// time-since-this-gauge-was-set).
func writeLastSuccess(w io.Writer, snapshot *MetricsSnapshot) error {
	if snapshot.LastSuccessTimestamp.IsZero() {
		return nil
	}
	_, err := fmt.Fprintf(w,
		"# HELP archive_completeness_last_success_timestamp Unix timestamp of the most recent clean run.\n"+
			"# TYPE archive_completeness_last_success_timestamp gauge\n"+
			"archive_completeness_last_success_timestamp %d\n",
		snapshot.LastSuccessTimestamp.Unix())
	return err
}

// writeRunDuration emits the run_duration gauge when a run has
// occurred (seconds > 0). Skip when zero so a stub call doesn't
// emit a meaningless 0.000.
func writeRunDuration(w io.Writer, snapshot *MetricsSnapshot) error {
	if snapshot.RunDurationSeconds <= 0 {
		return nil
	}
	_, err := fmt.Fprintf(w,
		"# HELP archive_completeness_run_duration_seconds Wall-clock duration of the most recent run.\n"+
			"# TYPE archive_completeness_run_duration_seconds gauge\n"+
			"archive_completeness_run_duration_seconds %.3f\n",
		snapshot.RunDurationSeconds)
	return err
}

// writeCounter emits a labelled counter block. Skip entirely on an
// empty map so the textfile doesn't carry orphan TYPE lines for a
// counter no run has produced yet.
func writeCounter(w io.Writer, name, help, labelKey string, samples map[string]int) error {
	if len(samples) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w,
		"# HELP %s %s\n# TYPE %s counter\n",
		name, help, name); err != nil {
		return err
	}
	for _, label := range sortedKeys(samples) {
		if _, err := fmt.Fprintf(w,
			"%s{%s=%q} %d\n",
			name, labelKey, label, samples[label]); err != nil {
			return err
		}
	}
	return nil
}

// WriteTextfileAtomic writes snapshot to path via the standard
// node_exporter textfile-collector atomic-write protocol:
//
//  1. Write to `<path>.tmp` with restrictive permissions
//  2. Rename to <path> (POSIX atomic on the same filesystem)
//
// node_exporter's textfile collector skips files whose name ends
// in `.tmp`, so a partial write never appears in a scrape.
func WriteTextfileAtomic(path string, snapshot *MetricsSnapshot) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644) //nolint:gosec // operator-supplied path; collector reads world-readable files
	if err != nil {
		return fmt.Errorf("create textfile %q: %w", tmp, err)
	}
	if err := WriteTextfile(f, snapshot); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename textfile %q → %q: %w", tmp, path, err)
	}
	return nil
}

// sortedKeys returns m's keys in ascending order. Used so the
// emitted metric blocks are stable across runs (ops are happier
// when the same scrape produces byte-identical output for an
// unchanged state).
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PopulateFromReport fills snapshot.FilesMissing from a Report's
// per-archive missing-counts. Convenience for the verify-mode
// caller; lets the metrics layer stay decoupled from Report's
// internal field names.
func (s *MetricsSnapshot) PopulateFromReport(r *Report) {
	if r == nil {
		return
	}
	if r.CrossAnchor != nil {
		s.FilesMissing["cross-anchor"] = r.CrossAnchor.MissingCount
	}
	if r.Primary != nil {
		s.FilesMissing["galexie-archive"] = r.Primary.MissingCount
	}
}

// PopulateFromFillResult merges a FillResult's per-source counts
// into the snapshot. Successful fetches contribute to attempts;
// failures contribute to BOTH attempts and failures (one attempt
// per failed source try). The snapshot's caller is responsible for
// tracking lifetime totals across runs if it wants — this method
// only emits per-run counts.
func (s *MetricsSnapshot) PopulateFromFillResult(res FillResult) {
	for source, count := range res.PerSourceSuccess {
		s.RepairAttempts[source] += count
	}
	// Each failure attempted every source in turn; we don't have
	// per-source failure counts on FillResult currently. For PR C
	// we just record the total failure count under a synthetic
	// `multi-source-exhausted` label; PR D will track per-source
	// failures explicitly when it adds the primary archive.
	if len(res.Failed) > 0 {
		s.RepairFailures["multi-source-exhausted"] += len(res.Failed)
	}
}

// SerialiseHeader returns a stable comment-only header that
// callers can prepend to a textfile dump for human readability —
// who emitted it, when, with what tool version. node_exporter
// ignores comment lines so this is wire-safe.
func SerialiseHeader(producedBy string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Produced by %s at %s\n", producedBy, time.Now().UTC().Format(time.RFC3339))
	return b.String()
}
