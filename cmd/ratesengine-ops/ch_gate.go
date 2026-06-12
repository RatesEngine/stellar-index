package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
	"github.com/StellarAtlas/stellar-atlas/internal/ledgerstream"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/clickhouse"
)

// chGate runs the ADR-0034 Phase-2 §6 gates over a backfilled ledger range:
//
//   - Gate 2 (completeness): for every ledger it recomputes the
//     decoder-independent census (dispatcher.CensusLedger) AND the
//     structural extract (clickhouse.ExtractLedger) straight from galexie,
//     asserts the extractor matches the census, then reads the range back
//     out of ClickHouse and asserts the STORED per-ledger counts and the
//     ACTUAL child-table row counts both equal the census. Any divergence
//     is a dropped/miscounted row — the gate fails (non-zero exit).
//   - Gate 1 (footprint): reports compressed bytes/ledger from system.parts
//     and projects full-history size; reports the census-walk throughput.
//
// It writes nothing — it only reads galexie + ClickHouse — so it is safe to
// re-run, and it is the same comparison Phase 5 completeness will reuse.
func chGate(args []string) error { //nolint:gocognit,gocyclo,funlen // linear walk + compare + report; splitting reduces clarity.
	fs := flag.NewFlagSet("ch-gate", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	from := fs.Uint("from", 0, "first ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "last ledger sequence (inclusive, required)")
	bucket := fs.String("bucket", "", "override storage bucket (default cfg.Storage.S3BucketLive)")
	chAddr := fs.String("ch-addr", "127.0.0.1:9300", "ClickHouse native address")
	examples := fs.Int("examples", 20, "max per-ledger mismatch examples to print")
	totalHistory := fs.Uint("project-to", 0, "tip ledger to project full-history footprint against (0 = skip projection)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *from == 0 || *to == 0 || *to < *from {
		return fmt.Errorf("-config, -from, -to are required; -to must be >= -from")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}

	ctx, cancel := signalContext()
	defer cancel()

	streamBucket := cfg.Storage.S3BucketLive
	if *bucket != "" {
		streamBucket = *bucket
	}
	lsCfg := newBoundedLedgerStreamConfig(cfg, streamBucket)
	passphrase := cfg.Stellar.Passphrase()

	fmt.Fprintf(os.Stderr, "ch-gate: census-walking ledgers %d..%d from %q, comparing to ClickHouse %s\n",
		*from, *to, streamBucket, *chAddr)

	var (
		walked                                         int
		extractMismatches                              int
		censusTx, censusOp, censusEvents, censusTrades uint64
		start                                          = time.Now()
		lastLog                                        = time.Now()
	)

	walkErr := ledgerstream.Stream(ctx, lsCfg, uint32(*from), uint32(*to),
		func(lcm sdkxdr.LedgerCloseMeta) error {
			walked++
			seq := lcm.LedgerSequence()

			census, cerr := dispatcher.CensusLedger(lcm, passphrase)
			if cerr != nil {
				fmt.Fprintf(os.Stderr, "ch-gate: ledger %d census: %v\n", seq, cerr)
				return nil
			}
			ext, eerr := clickhouse.ExtractLedger(lcm, passphrase)
			if eerr != nil {
				fmt.Fprintf(os.Stderr, "ch-gate: ledger %d extract: %v\n", seq, eerr)
				return nil
			}

			// Cross-check: the structural extractor must agree with the
			// independent census oracle on the protocol-meaningful counts.
			if uint32(census.SorobanEventCount) != ext.Ledger.SorobanEventCount ||
				uint32(census.ClassicTradeEffectCount) != ext.Ledger.ClassicTradeEffectCount {
				extractMismatches++
				if extractMismatches <= *examples {
					fmt.Fprintf(os.Stderr, "ch-gate: EXTRACT≠CENSUS ledger %d: events census=%d extract=%d | trades census=%d extract=%d\n",
						seq, census.SorobanEventCount, ext.Ledger.SorobanEventCount,
						census.ClassicTradeEffectCount, ext.Ledger.ClassicTradeEffectCount)
				}
			}

			censusEvents += uint64(census.SorobanEventCount)
			censusTrades += uint64(census.ClassicTradeEffectCount)
			censusTx += uint64(ext.Ledger.TxCount) // tx/op are pure LCM counts; census doesn't track them
			censusOp += uint64(ext.Ledger.OpCount)

			if time.Since(lastLog) >= 15*time.Second {
				rate := float64(walked) / time.Since(start).Seconds()
				fmt.Fprintf(os.Stderr, "ch-gate: walked %d (at %d, %.1f ledgers/s)\n", walked, seq, rate)
				lastLog = time.Now()
			}
			return nil
		},
	)
	if walkErr != nil && !errors.Is(walkErr, context.Canceled) {
		return fmt.Errorf("ch-gate: stream (walked %d): %w", walked, walkErr)
	}
	walkRate := float64(walked) / time.Since(start).Seconds()

	// ─── read the same range back out of ClickHouse ──────────────
	ch, err := clickhouse.ReadGateCounts(ctx, *chAddr, uint32(*from), uint32(*to))
	if err != nil {
		return err
	}
	fp, err := clickhouse.ReadFootprint(ctx, *chAddr)
	if err != nil {
		return err
	}

	// ─── report ──────────────────────────────────────────────────
	fmt.Printf("\n=== ch-gate: ADR-0034 Phase-2 §6 ===\n")
	fmt.Printf("range:           %d..%d (%d ledgers requested, %d walked)\n", *from, *to, *to-*from+1, walked)
	fmt.Printf("CH ledger rows:  %d (min=%d max=%d)\n", ch.LedgerRows, ch.MinLedger, ch.MaxLedger)

	type check struct {
		name           string
		census, stored uint64
		row            uint64 // 0 + hasRow=false when no row table mirrors this count
		hasRow         bool
	}
	checks := []check{
		{"transactions", censusTx, ch.StoredTx, ch.RowTx, true},
		{"operations", censusOp, ch.StoredOp, ch.RowOp, true},
		{"contract_events", censusEvents, ch.StoredEvents, ch.RowEvents, true},
		{"classic_trades", censusTrades, ch.StoredTrades, 0, false}, // no row table; stored count only
	}

	gateFail := false
	fmt.Printf("\n%-16s %14s %14s %14s  %s\n", "count", "census", "CH-stored", "CH-rows", "verdict")
	for _, c := range checks {
		ok := c.census == c.stored && (!c.hasRow || c.stored == c.row)
		verdict := "OK"
		if !ok {
			verdict = "MISMATCH"
			gateFail = true
		}
		rowStr := "-"
		if c.hasRow {
			rowStr = fmt.Sprintf("%d", c.row)
		}
		fmt.Printf("%-16s %14d %14d %14s  %s\n", c.name, c.census, c.stored, rowStr, verdict)
	}

	// Ledger coverage: every walked ledger must be present in CH exactly once.
	if ch.LedgerRows != uint64(walked) {
		fmt.Printf("ledger coverage: MISMATCH (walked %d, CH ledgers %d)\n", walked, ch.LedgerRows)
		gateFail = true
	}
	if extractMismatches > 0 {
		fmt.Printf("extractor vs census: MISMATCH on %d ledger(s)\n", extractMismatches)
		gateFail = true
	} else {
		fmt.Printf("extractor vs census: OK (all %d ledgers)\n", walked)
	}

	// ─── footprint (gate 1) ──────────────────────────────────────
	var totalBytes, totalRows uint64
	fmt.Printf("\n%-22s %16s %16s\n", "table", "bytes", "rows")
	for _, f := range fp {
		fmt.Printf("%-22s %16d %16d\n", f.Table, f.Bytes, f.Rows)
		totalBytes += f.Bytes
		totalRows += f.Rows
	}
	fmt.Printf("%-22s %16d %16d\n", "TOTAL", totalBytes, totalRows)
	// system.parts is whole-DB, so divide by ALL ledgers in CH, not the gate
	// range. If the gate range == the whole DB (e.g. the 100k sample is all
	// that's loaded), this is the sample's true bytes/ledger.
	if ch.TotalLedgers > 0 {
		bpl := float64(totalBytes) / float64(ch.TotalLedgers)
		fmt.Printf("\nfootprint: %.0f bytes/ledger (%.2f GiB total over %d CH ledgers)\n",
			bpl, float64(totalBytes)/(1<<30), ch.TotalLedgers)
		if *totalHistory > 0 {
			projected := bpl * float64(*totalHistory)
			fmt.Printf("projected full history (to ledger %d, linear from this sample): %.1f TiB\n",
				*totalHistory, projected/(1<<40))
		}
	}
	fmt.Printf("census-walk throughput: %.1f ledgers/s\n", walkRate)

	if gateFail {
		return fmt.Errorf("ch-gate: COMPLETENESS GATE FAILED")
	}
	fmt.Printf("\n✅ ch-gate: completeness gate PASSED\n")
	return nil
}
