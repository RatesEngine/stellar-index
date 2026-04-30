package supply

import (
	"fmt"
	"io"
	"math/big"
	"os"
	"time"
)

// WriteSnapshotTextfile renders a [Supply] snapshot to path in the
// Prometheus textfile-collector format used by node_exporter.
//
// pass=true → emits the success metric set including the
// `last_success_timestamp` gauge that the staleness alert keys on.
// pass=false → caller should use [WriteSnapshotFailureTextfile]
// instead so a failed run leaves the success-timestamp untouched.
//
// Atomic write protocol matches the established pattern in
// internal/archivecompleteness/metrics.go::WriteTextfileAtomic and
// cmd/ratesengine-sla-probe/textfile.go::writeTextfileAtomic —
// `<path>.tmp` first, rename into place. node_exporter skips
// `.tmp` files, so a partial write never appears in a scrape.
func WriteSnapshotTextfile(path string, snap Supply, durationSec float64, pass bool) error {
	return writeAtomic(path, func(w io.Writer) error {
		return writeSnapshotMetrics(w, snap, durationSec, pass)
	})
}

// WriteSnapshotFailureTextfile is the failure-path emit. Writes
// `unit_failed=1` and the run-duration gauge but OMITS the
// `last_success_timestamp` so node_exporter surfaces the
// previous-scrape value (which is what the staleness alert
// consumes when the current run failed).
//
// `assetRaw` is the operator-supplied -asset flag value (e.g.
// "native" / "USDC-G…"); it goes on the `unit_failed` label so an
// operator running multiple assets sees per-asset failure status.
func WriteSnapshotFailureTextfile(path, assetRaw string, durationSec float64) error {
	return writeAtomic(path, func(w io.Writer) error {
		return writeFailureMetrics(w, assetRaw, durationSec)
	})
}

// writeSnapshotMetrics emits the full success-path metric set:
//
//	ratesengine_supply_snapshot_total_xlm{asset_key=}
//	ratesengine_supply_snapshot_circulating_xlm{asset_key=}
//	ratesengine_supply_snapshot_max_xlm{asset_key=}              (only when set)
//	ratesengine_supply_snapshot_ledger{asset_key=}
//	ratesengine_supply_snapshot_observed_at_seconds{asset_key=}
//	ratesengine_supply_snapshot_run_duration_seconds
//	ratesengine_supply_snapshot_unit_failed{asset_key=}          0
//	ratesengine_supply_snapshot_last_success_timestamp{asset_key=}
//
// XLM units (not stroops) for human-readable Grafana panels —
// stroops × 10^-7. NUMERIC stroop precision is preserved in
// asset_supply_history; the textfile loses sub-stroop precision in
// the float64 conversion, which is fine for monitoring (the alerts
// don't read sub-XLM precision).
func writeSnapshotMetrics(w io.Writer, snap Supply, durationSec float64, pass bool) error {
	asset := snap.AssetKey

	if err := writeGauge(w,
		"ratesengine_supply_snapshot_total_xlm",
		"Total supply in XLM units (stroops × 10^-7).",
		asset, stroopsToXLM(snap.TotalSupply)); err != nil {
		return err
	}
	if err := writeGauge(w,
		"ratesengine_supply_snapshot_circulating_xlm",
		"Circulating supply in XLM units.",
		asset, stroopsToXLM(snap.CirculatingSupply)); err != nil {
		return err
	}
	if snap.MaxSupply != nil {
		if err := writeGauge(w,
			"ratesengine_supply_snapshot_max_xlm",
			"Max supply in XLM units (omitted for uncapped assets).",
			asset, stroopsToXLM(snap.MaxSupply)); err != nil {
			return err
		}
	}
	if err := writeGaugeInt(w,
		"ratesengine_supply_snapshot_ledger",
		"Ledger sequence the snapshot was attributed to.",
		asset, int64(snap.LedgerSequence)); err != nil {
		return err
	}
	if err := writeGaugeInt(w,
		"ratesengine_supply_snapshot_observed_at_seconds",
		"Unix timestamp of the snapshot's observed_at.",
		asset, snap.ObservedAt.Unix()); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w,
		"# HELP ratesengine_supply_snapshot_run_duration_seconds Wall-clock duration of the most recent snapshot run.\n"+
			"# TYPE ratesengine_supply_snapshot_run_duration_seconds gauge\n"+
			"ratesengine_supply_snapshot_run_duration_seconds %.3f\n",
		durationSec); err != nil {
		return err
	}
	failed := 0
	if !pass {
		failed = 1
	}
	if err := writeGaugeInt(w,
		"ratesengine_supply_snapshot_unit_failed",
		"1 when the most recent run failed, 0 on success.",
		asset, int64(failed)); err != nil {
		return err
	}
	if pass {
		if err := writeGaugeInt(w,
			"ratesengine_supply_snapshot_last_success_timestamp",
			"Unix timestamp of the most recent successful snapshot.",
			asset, time.Now().Unix()); err != nil {
			return err
		}
	}
	return nil
}

// writeFailureMetrics emits a minimal failure-path block. Only
// `unit_failed` and the run-duration gauge — no value gauges
// (we don't have a Supply to report) and no `last_success_timestamp`
// (so the staleness alert keys on the previous-scrape value).
func writeFailureMetrics(w io.Writer, assetRaw string, durationSec float64) error {
	if err := writeGaugeInt(w,
		"ratesengine_supply_snapshot_unit_failed",
		"1 when the most recent run failed, 0 on success.",
		assetRaw, 1); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w,
		"# HELP ratesengine_supply_snapshot_run_duration_seconds Wall-clock duration of the most recent snapshot run.\n"+
			"# TYPE ratesengine_supply_snapshot_run_duration_seconds gauge\n"+
			"ratesengine_supply_snapshot_run_duration_seconds %.3f\n",
		durationSec)
	return err
}

func writeGauge(w io.Writer, name, help, asset string, value float64) error {
	_, err := fmt.Fprintf(w,
		"# HELP %s %s\n# TYPE %s gauge\n%s{asset_key=%q} %.3f\n",
		name, help, name, name, asset, value)
	return err
}

func writeGaugeInt(w io.Writer, name, help, asset string, value int64) error {
	_, err := fmt.Fprintf(w,
		"# HELP %s %s\n# TYPE %s gauge\n%s{asset_key=%q} %d\n",
		name, help, name, name, asset, value)
	return err
}

// stroopsToXLM divides a stroops *big.Int by 10^7 and returns the
// XLM value as float64. Loses sub-stroop precision but the textfile
// is monitoring data, not the source of truth (asset_supply_history
// retains full NUMERIC precision).
func stroopsToXLM(stroops *big.Int) float64 {
	if stroops == nil {
		return 0
	}
	rat := new(big.Rat).SetFrac(stroops, big.NewInt(10_000_000))
	f, _ := rat.Float64()
	return f
}

// writeAtomic runs `body` against a `<path>.tmp` file then renames
// into place. Mirrors the pattern in internal/archivecompleteness
// and the SLA probe.
func writeAtomic(path string, body func(io.Writer) error) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644) //nolint:gosec // operator-supplied path; collector reads world-readable files
	if err != nil {
		return fmt.Errorf("create textfile %q: %w", tmp, err)
	}
	if err := body(f); err != nil {
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
		return fmt.Errorf("rename textfile %q -> %q: %w", tmp, path, err)
	}
	return nil
}
