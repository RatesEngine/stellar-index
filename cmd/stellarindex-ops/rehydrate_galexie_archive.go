package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stellar/go-stellar-sdk/support/datastore"

	"github.com/StellarIndex/stellar-index/internal/config"
)

// ─── stellarindex-ops rehydrate-galexie-archive ──────────────────
//
// Per ADR-0027 §Step 2: a non-destructive operator that re-copies
// LCM files from the cold tier (aws-public-blockchain, the AWS
// Open Data Sponsorship bucket) back into the hot tier (local
// galexie-archive MinIO bucket) for a given ledger range.
//
// Use cases:
//   - Recovering from accidental trim — re-fetch a range an
//     operator trimmed too aggressively.
//   - Pre-warming hot before a planned backfill — pull the
//     historical range to local disk so the backfill avoids
//     per-LCM cross-Atlantic latency.
//   - Cold-tier integrity spot check — read every file the
//     trimmed range claims to hold upstream; surface any
//     unexpected gaps.
//
// Idempotent: uses PutFileIfNotExists, so re-running over a
// range that's already hydrated is a no-op (skipped files are
// counted but not refetched).
//
// Sees only schema-aligned ledger-file boundaries — the SDK's
// DataStoreSchema.GetObjectKeyFromSequenceNumber gives the path;
// we step by LedgersPerFile so each file is fetched once.

type rehydrateOpts struct {
	cfgPath string
	from    uint32
	to      uint32
	dryRun  bool
}

func rehydrateGalexieArchive(args []string) error { //nolint:gocognit,gocyclo,funlen // CLI plumbing + per-file loop + reporting; one continuous procedure is easier to follow than five helpers
	opts, err := parseRehydrateFlags(args)
	if err != nil {
		return err
	}
	if opts.from == 0 || opts.to == 0 || opts.from > opts.to {
		return fmt.Errorf("invalid -from / -to: from=%d to=%d (both required; from <= to)", opts.from, opts.to)
	}

	cfg, err := config.Load(opts.cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.Storage.ColdTieringEnabled() {
		return fmt.Errorf("cold tier not configured — set storage.s3_cold_bucket_archive in %s before rehydrating", opts.cfgPath)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	hot, err := datastore.NewDataStore(rootCtx, datastore.DataStoreConfig{
		Type: "S3",
		Params: map[string]string{
			"destination_bucket_path": cfg.Storage.S3BucketArchive,
			"region":                  cfg.Storage.S3Region,
			"endpoint_url":            cfg.Storage.S3Endpoint,
		},
		NetworkPassphrase: cfg.Stellar.Passphrase(),
		Compression:       "zstd",
	})
	if err != nil {
		return fmt.Errorf("hot datastore: %w", err)
	}
	defer func() { _ = hot.Close() }()

	cold, err := datastore.NewDataStore(rootCtx, datastore.DataStoreConfig{
		Type: "S3",
		Params: map[string]string{
			"destination_bucket_path": cfg.Storage.S3ColdBucketArchive,
			"region":                  cfg.Storage.S3ColdRegion,
			"endpoint_url":            cfg.Storage.S3ColdEndpoint,
		},
		NetworkPassphrase: cfg.Stellar.Passphrase(),
		Compression:       "zstd",
	})
	if err != nil {
		return fmt.Errorf("cold datastore: %w", err)
	}
	defer func() { _ = cold.Close() }()

	// Schema discovery via hot — both tiers must publish compatible
	// schemas (Galexie's manifest is bucket-local but the LedgersPerFile
	// + FilesPerPartition + extension match across mirrors of the same
	// network). Using hot is the safer side: hot is where we write.
	schema, err := datastore.LoadSchema(rootCtx, hot, datastore.DataStoreConfig{
		Type: "S3",
		Params: map[string]string{
			"destination_bucket_path": cfg.Storage.S3BucketArchive,
			"region":                  cfg.Storage.S3Region,
			"endpoint_url":            cfg.Storage.S3Endpoint,
		},
	})
	if err != nil {
		return fmt.Errorf("load schema (hot): %w", err)
	}

	paths := rehydratePaths(schema, opts.from, opts.to)
	logger.Info("rehydrate plan",
		"from", opts.from,
		"to", opts.to,
		"files", len(paths),
		"ledgers_per_file", schema.LedgersPerFile,
		"dry_run", opts.dryRun,
	)

	var (
		skipped int // already present in hot
		copied  int // dry-run: would-copy; commit: actually copied
		missing int // not present in cold either — the genuinely-missing bucket
		errs    int
	)
	start := time.Now()
	for i, path := range paths {
		if err := rootCtx.Err(); err != nil {
			return fmt.Errorf("rehydrate aborted at %d/%d: %w", i, len(paths), err)
		}
		exists, err := hot.Exists(rootCtx, path)
		if err != nil {
			logger.Warn("hot.Exists failed", "path", path, "err", err)
			errs++
			continue
		}
		if exists {
			skipped++
			continue
		}
		if opts.dryRun {
			copied++
			continue
		}
		// Read from cold, write to hot. Stream through a buffer so
		// PutFile's WriterTo contract is satisfied; the LCM file
		// sizes are bounded (~tens of KB per file at LedgersPerFile=1
		// — Galexie's default) so in-memory buffering is fine.
		rc, _, err := cold.GetFile(rootCtx, path)
		if err != nil {
			// Cold-side absent — log + count, don't abort. Operators
			// inspect missing.count to discover real archive holes
			// (e.g. an LCM that genuinely never landed upstream).
			logger.Warn("cold.GetFile failed", "path", path, "err", err)
			missing++
			continue
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			logger.Warn("cold body drain failed", "path", path, "err", err)
			errs++
			continue
		}
		ok, err := hot.PutFileIfNotExists(rootCtx, path, byteWriterTo(body), nil)
		if err != nil {
			logger.Warn("hot.PutFileIfNotExists failed", "path", path, "err", err)
			errs++
			continue
		}
		if ok {
			copied++
		} else {
			// Race condition: another process hydrated this file
			// between our Exists check and our Put. Count as skipped.
			skipped++
		}
	}

	logger.Info("rehydrate complete",
		"files", len(paths),
		"skipped_already_present", skipped,
		"copied", copied,
		"missing_in_cold", missing,
		"errors", errs,
		"elapsed", time.Since(start).String(),
		"dry_run", opts.dryRun,
	)
	if errs > 0 {
		return fmt.Errorf("rehydrate finished with %d errors", errs)
	}
	return nil
}

// rehydratePaths enumerates the schema-aligned file paths covering
// [from, to]. Each file holds LedgersPerFile ledgers; we step by
// that amount to visit each path once. Paths are sorted ascending.
func rehydratePaths(schema datastore.DataStoreSchema, from, to uint32) []string {
	if schema.LedgersPerFile == 0 {
		// Defensive: LoadSchema should populate this. Falling back to
		// 1 ledger per file matches Galexie's default and prevents an
		// infinite loop if the schema is malformed.
		schema.LedgersPerFile = 1
	}
	// Align `from` down to its file's start boundary so a -from in
	// mid-file still rehydrates the file containing it.
	start := schema.GetSequenceNumberStartBoundary(from)
	estFiles := int((to-start)/schema.LedgersPerFile) + 1
	seen := make(map[string]struct{}, estFiles)
	out := make([]string, 0, estFiles)
	for seq := start; seq <= to; seq += schema.LedgersPerFile {
		path := schema.GetObjectKeyFromSequenceNumber(seq)
		if _, dup := seen[path]; dup {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func parseRehydrateFlags(args []string) (rehydrateOpts, error) {
	fs := flag.NewFlagSet("rehydrate-galexie-archive", flag.ContinueOnError)
	var (
		opts rehydrateOpts
		from int64
		to   int64
	)
	fs.StringVar(&opts.cfgPath, "config", "/etc/stellarindex.toml", "Path to stellarindex.toml")
	fs.Int64Var(&from, "from", 0, "First ledger sequence to rehydrate (inclusive)")
	fs.Int64Var(&to, "to", 0, "Last ledger sequence to rehydrate (inclusive)")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "List would-copy files without writing to hot")
	if err := fs.Parse(args); err != nil {
		return rehydrateOpts{}, err
	}
	if from < 0 || from > int64(^uint32(0)) {
		return rehydrateOpts{}, fmt.Errorf("-from out of uint32 range: %d", from)
	}
	if to < 0 || to > int64(^uint32(0)) {
		return rehydrateOpts{}, fmt.Errorf("-to out of uint32 range: %d", to)
	}
	opts.from = uint32(from)
	opts.to = uint32(to)
	return opts, nil
}

// byteWriterTo adapts a []byte to the io.WriterTo interface required
// by datastore.DataStore.PutFile. The SDK's S3 backend buffers the
// full body anyway; passing a WriterTo is the interface shape, not
// a streaming-vs-buffered choice.
type byteWriterTo []byte

func (b byteWriterTo) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(b)
	return int64(n), err
}
