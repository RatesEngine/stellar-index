package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/StellarIndex/stellar-index/internal/config"
	"github.com/StellarIndex/stellar-index/internal/metadata"
	"github.com/StellarIndex/stellar-index/internal/storage/timescale"
)

// sep1RefreshCmd resolves the SEP-1 stellar.toml for every issuer
// with a home_domain set and writes the parsed payload back to
// `issuers.sep1_payload` + bumps `sep1_resolved_at`.
//
// Run from cron at e.g. once an hour:
//
//	stellarindex-ops sep1-refresh -config /etc/stellarindex/api.toml \
//	    -limit 200 -older-than 24h
//
// Per-issuer fetch failures are logged + counted; they don't abort
// the run. The resolver respects its built-in 10s per-request
// timeout + SSRF guard so a slow/malicious operator domain can't
// stall the whole batch.
//
// Once a payload is written, /v1/issuers list responses surface
// `org_name` from `sep1_payload->>'OrgName'`.
//
//nolint:gocognit // linear refresh loop; per-issuer fetch + marshal + write reads better inline.
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
		// Bidirectional SEP-1 verification: the org is only "verified" if the
		// fetched toml's [[CURRENCIES]] lists THIS issuer back — i.e. the domain
		// owner attests to this account. Without it, anyone can set their
		// account's home_domain to a reputable domain and inherit its ORG_NAME
		// (spoofing). Callers MUST only merge/group issuers by org when this is
		// true; one-directional matches are "claimed, unverified".
		orgVerified := tomlListsIssuer(sep.Currencies, c.GStrkey)
		// Compact payload — OrgName/Documentation for /v1/issuers,
		// Currencies for the per-asset SEP-1 overlay on /v1/assets/{id}
		// (the latter used to live-fetch per request; this cron is now
		// the source of truth so the handler is a DB lookup). Raw +
		// NetworkPassphrase excluded — nothing reads them.
		currencies := make([]map[string]any, 0, len(sep.Currencies))
		for _, c := range sep.Currencies {
			currencies = append(currencies, map[string]any{
				"Code":            c.Code,
				"Issuer":          c.Issuer,
				"Decimals":        c.Decimals,
				"DisplayDecimals": c.DisplayDecimals,
				"Name":            c.Name,
				"Description":     c.Description,
				"Conditions":      c.Conditions,
				"Image":           c.Image,
				"FixedNumber":     c.FixedNumber,
				"MaxNumber":       c.MaxNumber,
				"IsUnlimited":     c.IsUnlimited,
				"AnchorAsset":     c.AnchorAsset,
				"AnchorAssetType": c.AnchorAssetType,
				"Status":          c.Status,
			})
		}
		payload, jerr := json.Marshal(map[string]any{
			"OrgName":       sep.OrgName,
			"OrgVerified":   orgVerified,
			"Version":       sep.Version,
			"Documentation": sep.Documentation,
			"Currencies":    currencies,
			"FetchedAt":     sep.FetchedAt.UTC().Format(time.RFC3339),
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

// tomlListsIssuer reports whether the fetched SEP-1 toml's [[CURRENCIES]] lists
// the given issuer back — the bidirectional half of org verification. Without
// this match, ORG_NAME from a self-declared home_domain is spoofable.
func tomlListsIssuer(currencies []metadata.Currency, issuer string) bool {
	for _, cur := range currencies {
		if cur.Issuer == issuer {
			return true
		}
	}
	return false
}
