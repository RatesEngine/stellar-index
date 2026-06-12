package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stellar/go-stellar-sdk/support/datastore"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
)

// ─── ratesengine-ops trim-galexie-archive ──────────────────────
//
// Per ADR-0027 §Step 2: the DESTRUCTIVE operator that deletes
// cold-eligible LCM files from the local hot tier (galexie-archive
// MinIO bucket on r1) once their presence in the cold tier has
// been verified. Reclaims pool capacity by tiering off the bulky
// historical mirror; the cold tier (aws-public-blockchain) serves
// reads for those ranges through the TieredDataStore fallback.
//
// Safety stack (each is independent):
//
//   1. --dry-run is the DEFAULT. Actual deletion requires the
//      explicit --commit flag — there is no "are you sure?" prompt
//      because the dry-run output IS the review step. Mismatched
//      flags (e.g. --dry-run --commit) report which dominates and
//      stop before any S3 call.
//   2. --verify-upstream is the DEFAULT. Every candidate is
//      HEAD'd against the cold tier before being marked for
//      deletion. If cold.Exists returns false, the candidate is
//      SKIPPED. Pass --no-verify-upstream only for an isolated
//      restore-from-backup workflow where you've already proven
//      the upstream copy by other means.
//   3. --max-files caps deletions per run. Default 100000 — a
//      typo can never delete the full archive in one shot.
//   4. --older-than-ledger is REQUIRED. No implicit "trim
//      everything below tip - N". Operator names a specific
//      sequence; the planner shows the exact span.
//   5. Cold-tier MUST be configured (cfg.Storage.S3ColdBucketArchive
//      non-empty). Refuses to run otherwise — without a cold tier
//      every "trim" is unrecoverable data loss.

type trimOpts struct {
	cfgPath        string
	olderThan      uint32 // ledger sequence boundary; files entirely below this are candidates
	verifyUpstream bool
	dryRun         bool
	commit         bool
	maxFiles       int
}

func trimGalexieArchive(args []string) error { //nolint:gocognit,gocyclo,funlen // CLI plumbing + filter + delete loop; six helpers would scatter the safety guard chain
	opts, err := parseTrimFlags(args)
	if err != nil {
		return err
	}
	if opts.olderThan == 0 {
		return fmt.Errorf("--older-than-ledger is required (no implicit cutoff — name a specific ledger sequence)")
	}
	if opts.dryRun && opts.commit {
		return fmt.Errorf("--dry-run and --commit are mutually exclusive; pick one")
	}
	// Default to dry-run when neither is set. The explicit lack
	// of --commit is the safety primitive.
	if !opts.commit {
		opts.dryRun = true
	}
	if opts.maxFiles <= 0 {
		return fmt.Errorf("--max-files must be > 0; got %d", opts.maxFiles)
	}

	cfg, err := config.Load(opts.cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.Storage.ColdTieringEnabled() {
		return fmt.Errorf("cold tier not configured — trimming without a cold fallback is unrecoverable data loss. Set storage.s3_cold_bucket_archive first")
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

	var cold datastore.DataStore
	if opts.verifyUpstream {
		cold, err = datastore.NewDataStore(rootCtx, datastore.DataStoreConfig{
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
	}

	// Raw S3 client for DeleteObject — the SDK's datastore.DataStore
	// interface has no Delete method. We construct the same shape
	// the SDK's NewS3DataStore builds (path-style, optional anonymous
	// fallback for public buckets that don't need it here), then
	// call DeleteObject directly. Auth comes from the standard AWS
	// env vars (or the operator's ~/.aws/credentials if running
	// interactively). Hot is local MinIO; the env vars
	// RATESENGINE_S3_ACCESS_KEY + RATESENGINE_S3_SECRET_KEY map
	// to MinIO's root creds via the systemd EnvironmentFile.
	hotBucket, hotKeyPrefix, err := splitBucketPath(cfg.Storage.S3BucketArchive)
	if err != nil {
		return fmt.Errorf("parse archive bucket path: %w", err)
	}
	s3Client, err := buildS3Client(rootCtx, cfg.Storage.S3Endpoint, cfg.Storage.S3Region, cfg.Storage.S3AccessKeyEnv, cfg.Storage.S3SecretKeyEnv)
	if err != nil {
		return fmt.Errorf("build s3 client: %w", err)
	}

	logger.Info("trim plan",
		"hot_bucket", hotBucket,
		"hot_key_prefix", hotKeyPrefix,
		"older_than_ledger", opts.olderThan,
		"verify_upstream", opts.verifyUpstream,
		"max_files", opts.maxFiles,
		"dry_run", opts.dryRun,
	)

	// Enumerate hot files. SDK ListFilePaths returns key-relative
	// paths sorted ascending; we filter by parsed ledger range.
	paths, err := hot.ListFilePaths(rootCtx, datastore.ListFileOptions{})
	if err != nil {
		return fmt.Errorf("list hot files: %w", err)
	}
	logger.Info("hot file enumeration", "total_files", len(paths))

	var (
		candidates       []string
		skippedTooFresh  int
		skippedNotInCold int
		errs             int
	)
	for _, p := range paths {
		from, to, perr := datastore.ParseRangeFromObjectKey(p)
		if perr != nil {
			// Unparseable path — leave alone. This is the safe
			// posture; only files we can confidently bucket as
			// "fully below cutoff" are candidates.
			continue
		}
		_ = from
		if to >= opts.olderThan {
			skippedTooFresh++
			continue
		}
		if opts.verifyUpstream {
			ok, ferr := cold.Exists(rootCtx, p)
			if ferr != nil {
				logger.Warn("cold.Exists failed; treating as not-present (skip)", "path", p, "err", ferr)
				skippedNotInCold++
				errs++
				continue
			}
			if !ok {
				skippedNotInCold++
				continue
			}
		}
		candidates = append(candidates, p)
		if len(candidates) >= opts.maxFiles {
			logger.Warn("max-files cap reached; remaining paths not evaluated",
				"max_files", opts.maxFiles,
				"unprocessed_remaining", len(paths)-len(candidates)-skippedTooFresh-skippedNotInCold,
			)
			break
		}
	}

	logger.Info("trim plan ready",
		"candidates", len(candidates),
		"skipped_too_fresh", skippedTooFresh,
		"skipped_not_in_cold", skippedNotInCold,
		"verify_errors", errs,
		"dry_run", opts.dryRun,
	)

	if opts.dryRun {
		// Surface the first/last few candidates so an operator
		// reviewing dry-run output can sanity-check the boundary.
		if n := len(candidates); n > 0 {
			head := candidates[:minInt(3, n)]
			tail := candidates[maxInt(0, n-3):]
			logger.Info("dry-run sample",
				"first_3", head,
				"last_3", tail,
			)
		}
		return nil
	}

	// --commit: do the deletions. Per-object DeleteObject (vs
	// DeleteObjects bulk) so a partial failure leaves a clear
	// position cursor — operator can re-run --dry-run to see
	// what's left.
	start := time.Now()
	var deleted int
	for _, p := range candidates {
		if err := rootCtx.Err(); err != nil {
			return fmt.Errorf("trim aborted at %d/%d: %w", deleted, len(candidates), err)
		}
		fullKey := p
		if hotKeyPrefix != "" {
			fullKey = strings.TrimPrefix(hotKeyPrefix+"/", "/") + p
		}
		_, derr := s3Client.DeleteObject(rootCtx, &s3.DeleteObjectInput{
			Bucket: aws.String(hotBucket),
			Key:    aws.String(fullKey),
		})
		if derr != nil {
			logger.Warn("DeleteObject failed", "path", p, "err", derr)
			errs++
			continue
		}
		deleted++
	}
	logger.Info("trim complete",
		"deleted", deleted,
		"errors", errs,
		"elapsed", time.Since(start).String(),
	)
	if errs > 0 {
		return fmt.Errorf("trim finished with %d errors (deleted %d/%d)", errs, deleted, len(candidates))
	}
	return nil
}

func parseTrimFlags(args []string) (trimOpts, error) {
	fs := flag.NewFlagSet("trim-galexie-archive", flag.ContinueOnError)
	var (
		opts      trimOpts
		olderThan int64
		noVerify  bool
	)
	fs.StringVar(&opts.cfgPath, "config", "/etc/ratesengine.toml", "Path to ratesengine.toml")
	fs.Int64Var(&olderThan, "older-than-ledger", 0, "REQUIRED. Files whose entire ledger range is below this sequence become deletion candidates. No default to prevent unintentional trims.")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "List would-delete candidates without deleting. Default when neither --dry-run nor --commit is set.")
	fs.BoolVar(&opts.commit, "commit", false, "Actually delete. Requires explicit opt-in (default behaviour is dry-run).")
	fs.BoolVar(&noVerify, "no-verify-upstream", false, "Skip the HEAD-against-cold check. NOT RECOMMENDED — disables the primary safety primitive.")
	fs.IntVar(&opts.maxFiles, "max-files", 100000, "Hard cap on candidates per run. Default 100000 — a typo can never delete the full archive in one invocation.")
	if err := fs.Parse(args); err != nil {
		return trimOpts{}, err
	}
	if olderThan < 0 || olderThan > int64(^uint32(0)) {
		return trimOpts{}, fmt.Errorf("--older-than-ledger out of uint32 range: %d", olderThan)
	}
	opts.olderThan = uint32(olderThan)
	opts.verifyUpstream = !noVerify
	return opts, nil
}

// splitBucketPath parses the SDK-style "bucket/prefix/path" form
// into (bucket, prefix). Mirrors the SDK's `url.Parse("s3://" + ...)`
// logic from datastore/s3.go's FromS3Client.
func splitBucketPath(bucketPath string) (bucket, prefix string, err error) {
	parsed, err := url.Parse("s3://" + bucketPath)
	if err != nil {
		return "", "", err
	}
	return parsed.Host, strings.TrimPrefix(parsed.Path, "/"), nil
}

// buildS3Client builds an *s3.Client with path-style addressing
// (required by MinIO) and the operator-configured endpoint /
// region / credentials. accessKeyEnv + secretKeyEnv are env-var
// NAMES (not values) — they're resolved at call time so the
// binary inherits whatever the systemd EnvironmentFile sets.
func buildS3Client(ctx context.Context, endpoint, region, accessKeyEnv, secretKeyEnv string) (*s3.Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	// Prefer explicit creds from the env-var names cfg.Storage points
	// at. The SDK's default chain also picks them up if the env vars
	// match its canonical names (AWS_ACCESS_KEY_ID etc.), but we go
	// explicit to align with the rest of the codebase's env-var
	// naming convention.
	ak := os.Getenv(accessKeyEnv)
	sk := os.Getenv(secretKeyEnv)
	if ak != "" && sk != "" {
		awsCfg.Credentials = credentials.NewStaticCredentialsProvider(ak, sk, "")
	}
	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = true
		o.Region = region
	}), nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
