package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/StellarIndex/stellar-index/internal/config"
	"github.com/StellarIndex/stellar-index/internal/storage/clickhouse"
	"github.com/StellarIndex/stellar-index/internal/storage/timescale"
)

// issuerEnrich populates issuers.home_domain from the on-chain account state in
// the ClickHouse lake (DATA-TRUTH-PLAN G5 prerequisite). The issuers table only
// ever gets g_strkey written by the indexer; home_domain was never synced, so
// sep1-refresh (which selects issuers WITH a home_domain) had zero candidates
// and org_name stayed null. This reads each issuer's account home_domain from
// ledger_entries_current (complete after the G2 account backfill) in batches
// and writes it back, unblocking sep1-refresh → org_name.
//
// creation_ledger is NOT set here: it needs the create_account op from full
// history (operation_participants only covers ~1 day), which is a separate
// genesis op-scan — tracked, not done here.
func issuerEnrich(args []string) error {
	fs := flag.NewFlagSet("issuer-enrich", flag.ContinueOnError)
	cfgPath := fs.String("config", "/etc/stellarindex.toml", "config path")
	chAddr := fs.String("ch", "127.0.0.1:9300", "ClickHouse native address")
	batch := fs.Int("batch", 1000, "issuers per ClickHouse lookup batch")
	dryRun := fs.Bool("dry-run", false, "report counts without writing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ctx := context.Background()
	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer func() { _ = store.Close() }()
	er, err := clickhouse.NewExplorerReader(ctx, *chAddr)
	if err != nil {
		return fmt.Errorf("clickhouse: %w", err)
	}

	ids, err := loadIssuerGStrkeys(ctx, store)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "issuer-enrich: %d issuers; resolving home_domain from the lake (batch=%d, dry-run=%v)\n",
		len(ids), *batch, *dryRun)

	found, updated, start := 0, 0, time.Now()
	for lo := 0; lo < len(ids); lo += *batch {
		hi := lo + *batch
		if hi > len(ids) {
			hi = len(ids)
		}
		domains, derr := er.AccountHomeDomains(ctx, ids[lo:hi])
		if derr != nil {
			return fmt.Errorf("home_domain batch [%d,%d): %w", lo, hi, derr)
		}
		found += len(domains)
		if !*dryRun {
			n, uerr := updateIssuerHomeDomains(ctx, store, domains)
			if uerr != nil {
				return fmt.Errorf("update home_domains: %w", uerr)
			}
			updated += n
		}
		if (lo/(*batch))%20 == 0 {
			fmt.Fprintf(os.Stderr, "  ... %d/%d issuers scanned, %d with home_domain (%s)\n",
				hi, len(ids), found, time.Since(start).Round(time.Second))
		}
	}
	fmt.Printf("\n✅ issuer-enrich: %d issuers, %d have a home_domain, %d rows updated.\n", len(ids), found, updated)
	fmt.Printf("   Next: run `stellarindex-ops sep1-refresh` to fetch their tomls → org_name.\n")
	return nil
}

func loadIssuerGStrkeys(ctx context.Context, store *timescale.Store) ([]string, error) {
	rows, err := store.DB().QueryContext(ctx, `SELECT g_strkey FROM issuers`)
	if err != nil {
		return nil, fmt.Errorf("select issuers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, fmt.Errorf("scan g_strkey: %w", err)
		}
		ids = append(ids, g)
	}
	return ids, rows.Err()
}

// updateIssuerHomeDomains writes home_domain for each issuer that has one,
// only when the column is currently empty (never clobbers a resolver-set value).
func updateIssuerHomeDomains(ctx context.Context, store *timescale.Store, domains map[string]string) (int, error) {
	const q = `UPDATE issuers SET home_domain = $1
		WHERE g_strkey = $2 AND (home_domain IS NULL OR home_domain = '')`
	n := 0
	for g, domain := range domains {
		res, err := store.DB().ExecContext(ctx, q, domain, g)
		if err != nil {
			return n, err
		}
		if c, _ := res.RowsAffected(); c > 0 {
			n++
		}
	}
	return n, nil
}
