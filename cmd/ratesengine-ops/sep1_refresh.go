package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/metadata"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// sep1RefreshCmd resolves the SEP-1 stellar.toml for every issuer
// with a home_domain set and writes the parsed payload back to
// `issuers.sep1_payload` + bumps `sep1_resolved_at`.
//
// Run from cron at e.g. once an hour:
//
//	ratesengine-ops sep1-refresh -config /etc/ratesengine/api.toml \
//	    -limit 200 -older-than 24h
//
// Per-issuer fetch failures are logged + counted; they don't abort
// the run. The resolver respects its built-in 10s per-request
// timeout + SSRF guard so a slow/malicious operator domain can't
// stall the whole batch.
//
// Once a payload is written, /v1/issuers list responses surface
// `org_name` from `sep1_payload->>'OrgName'`.
func sep1RefreshCmd(args []string) error {
	fs := flag.NewFlagSet("sep1-refresh", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	limit := fs.Int("limit", 100, "Max issuers to refresh per run (1-1000)")
	olderThan := fs.Duration("older-than", 24*time.Hour, "Skip issuers refreshed more recently than this")
	timeout := fs.Duration("timeout", 5*time.Minute, "Wall-clock timeout for the whole run")
	dryRun := fs.Bool("dry-run", false, "Fetch + print without writing to issuers.sep1_payload")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return errors.New("-config is required")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	candidates, err := store.IssuersNeedingSep1Refresh(ctx, *olderThan, *limit)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Println("No issuers need refresh.")
		return nil
	}
	fmt.Printf("Refreshing %d issuer(s) (older than %s)…\n", len(candidates), *olderThan)

	resolver := metadata.NewResolver(metadata.Options{Timeout: 10 * time.Second})

	var ok, failed int
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			fmt.Printf("\nAborted at %d/%d (deadline): %v\n", ok+failed, len(candidates), err)
			break
		}
		sep, err := resolver.Resolve(ctx, c.HomeDomain)
		if err != nil {
			fmt.Printf("FAIL  %s  %s  %v\n", c.GStrkey, c.HomeDomain, err)
			failed++
			continue
		}
		// Compact payload — store the fields we surface today plus
		// the documentation map so future code can read more without
		// re-fetching. Currencies + Raw are excluded; per-asset
		// SEP-1 surfaces via /v1/assets's own overlay path.
		payload, jerr := json.Marshal(map[string]any{
			"OrgName":           sep.OrgName,
			"Version":           sep.Version,
			"NetworkPassphrase": sep.NetworkPassphrase,
			"Documentation":     sep.Documentation,
			"FetchedAt":         sep.FetchedAt.UTC().Format(time.RFC3339),
		})
		if jerr != nil {
			fmt.Printf("FAIL  %s  marshal: %v\n", c.GStrkey, jerr)
			failed++
			continue
		}
		if !*dryRun {
			if err := store.SetIssuerSep1Payload(ctx, c.GStrkey, payload); err != nil {
				fmt.Printf("FAIL  %s  write: %v\n", c.GStrkey, err)
				failed++
				continue
			}
		}
		fmt.Printf("OK    %s  %s  org=%q\n", c.GStrkey, c.HomeDomain, sep.OrgName)
		ok++
	}
	fmt.Printf("\n%d succeeded, %d failed\n", ok, failed)
	if *dryRun {
		fmt.Println("(dry-run; no rows written)")
	}
	return nil
}
