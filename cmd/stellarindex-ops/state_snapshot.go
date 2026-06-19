package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/stellar/go-stellar-sdk/historyarchive"
	"github.com/stellar/go-stellar-sdk/ingest"
	"github.com/stellar/go-stellar-sdk/support/storage"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarIndex/stellar-index/internal/config"
)

const (
	defaultPubnetArchive    = "https://history.stellar.org/prd/core-live/core_live_001"
	defaultPubnetPassphrase = "Public Global Stellar Network ; September 2015"
)

// snapTally is the per-entry-type rollup of a checkpoint state read.
type snapTally struct {
	byType        map[xdr.LedgerEntryType]uint64
	total         uint64
	contractCode  uint64
	wasmInstances uint64
	sacInstances  uint64
	elapsed       time.Duration
	partial       bool
}

// stateSnapshot reads a history-archive checkpoint's full current ledger-entry
// state (the bucket list) and tallies it by entry type. This is the read-only
// foundation of the data-truth backfill (docs/archive/page-audit-2026-06-19/
// DATA-TRUTH-PLAN.md, gaps G1–G3): the served current-state projection
// (ledger_entries_current) only holds entries that CHANGED since ledger ~62M,
// so a checkpoint snapshot is the source of truth for the dormant-pre-62M tail
// — contract code/instances (→ WASM), accounts/trustlines (→ account state +
// circulating supply). The CheckpointChangeReader streams the bucket list and
// emits the CURRENT entry for every live key in one pass (no genesis replay).
//
// Read-only: it never writes. -limit caps entries processed so the bucket
// download stays bounded for a quick proof; -limit 0 reads the whole snapshot.
func stateSnapshot(args []string) error {
	fs := flag.NewFlagSet("state-snapshot", flag.ContinueOnError)
	cfgPath := fs.String("config", "/etc/stellarindex.toml", "config path (optional for a public-archive read)")
	archiveURL := fs.String("archive", "", "history archive URL (default: cfg.Stellar.HistoryArchiveURL)")
	checkpoint := fs.Uint("checkpoint", 0, "checkpoint ledger (default: latest checkpoint)")
	limit := fs.Uint64("limit", 2_000_000, "max entries to read (0 = full snapshot)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	url, passphrase := resolveArchiveTarget(*cfgPath, *archiveURL)
	ctx := context.Background()
	arch, err := historyarchive.Connect(url, historyarchive.ArchiveOptions{
		NetworkPassphrase: passphrase,
		ConnectOptions:    storage.ConnectOptions{Context: ctx, UserAgent: "stellarindex-ops/state-snapshot"},
	})
	if err != nil {
		return fmt.Errorf("connect history archive %q: %w", url, err)
	}

	seq, err := resolveCheckpoint(arch, uint32(*checkpoint)) //nolint:gosec // operator-supplied ledger
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "state-snapshot: reading checkpoint %d from %s (limit=%d)\n", seq, url, *limit)

	t, err := tallyCheckpoint(ctx, arch, seq, *limit)
	if err != nil {
		return err
	}
	printTally(seq, t)
	return nil
}

// resolveArchiveTarget picks the archive URL + network passphrase, preferring
// the config but falling back to the public pubnet archive so a read works
// even without a config file.
func resolveArchiveTarget(cfgPath, override string) (url, passphrase string) {
	url, passphrase = override, defaultPubnetPassphrase
	if cfg, err := config.LoadWithEnv(cfgPath); err == nil {
		if url == "" {
			url = cfg.Stellar.HistoryArchiveURL
		}
		if p := cfg.Stellar.Passphrase(); p != "" {
			passphrase = p
		}
	} else {
		fmt.Fprintf(os.Stderr, "state-snapshot: config load failed (%v) — using public-archive defaults\n", err)
	}
	if url == "" {
		url = defaultPubnetArchive
	}
	return url, passphrase
}

// resolveCheckpoint returns the requested checkpoint ledger, or the latest
// checkpoint when 0.
func resolveCheckpoint(arch *historyarchive.Archive, want uint32) (uint32, error) {
	if want != 0 {
		return want, nil
	}
	latest, err := arch.GetLatestLedgerSequence()
	if err != nil {
		return 0, fmt.Errorf("latest ledger: %w", err)
	}
	return arch.GetCheckpointManager().PrevCheckpoint(latest), nil
}

// tallyCheckpoint streams the checkpoint's bucket list and rolls it up by entry
// type (read-only). limit>0 stops early for a bounded proof.
func tallyCheckpoint(ctx context.Context, arch *historyarchive.Archive, seq uint32, limit uint64) (*snapTally, error) {
	reader, err := ingest.NewCheckpointChangeReader(ctx, arch, seq)
	if err != nil {
		return nil, fmt.Errorf("checkpoint change reader @ %d: %w", seq, err)
	}
	defer func() { _ = reader.Close() }()

	t := &snapTally{byType: map[xdr.LedgerEntryType]uint64{}}
	start := time.Now()
	for {
		ch, rerr := reader.Read()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return nil, fmt.Errorf("read entry %d: %w", t.total, rerr)
		}
		if ch.Post == nil { // a snapshot is all live entries → Post-only
			continue
		}
		t.observe(ch.Type, ch.Post)
		if t.total%1_000_000 == 0 {
			fmt.Fprintf(os.Stderr, "  ... %d entries (%s)\n", t.total, time.Since(start).Round(time.Second))
		}
		if limit > 0 && t.total >= limit {
			t.partial = true
			fmt.Fprintf(os.Stderr, "  (limit reached — partial tally)\n")
			break
		}
	}
	t.elapsed = time.Since(start)
	return t, nil
}

// observe folds one live entry into the tally, classifying contract instances
// as WASM vs SAC (the G1 signal — SACs have no WASM, WASM instances point at a
// contract_code blob we need for the "see the code" view).
func (t *snapTally) observe(typ xdr.LedgerEntryType, post *xdr.LedgerEntry) {
	t.byType[typ]++
	t.total++
	switch typ {
	case xdr.LedgerEntryTypeContractCode:
		t.contractCode++
	case xdr.LedgerEntryTypeContractData:
		cd, ok := post.Data.GetContractData()
		if !ok || cd.Key.Type != xdr.ScValTypeScvLedgerKeyContractInstance {
			return
		}
		inst, iok := cd.Val.GetInstance()
		if !iok {
			return
		}
		switch inst.Executable.Type {
		case xdr.ContractExecutableTypeContractExecutableStellarAsset:
			t.sacInstances++
		case xdr.ContractExecutableTypeContractExecutableWasm:
			t.wasmInstances++
		}
	}
}

func printTally(seq uint32, t *snapTally) {
	note := " (full snapshot)"
	if t.partial {
		note = " (PARTIAL — limit hit; pass -limit 0 for the full snapshot)"
	}
	fmt.Printf("\n=== checkpoint %d state tally — %d entries in %s%s ===\n",
		seq, t.total, t.elapsed.Round(time.Second), note)
	for typ, c := range t.byType {
		fmt.Printf("  %-24s %d\n", typ.String(), c)
	}
	fmt.Printf("\ncontract_code blobs:      %d   (ledger_entries_current has ~257)\n", t.contractCode)
	fmt.Printf("contract WASM instances:  %d\n", t.wasmInstances)
	fmt.Printf("contract SAC instances:   %d\n", t.sacInstances)
	fmt.Printf("\nNext (DATA-TRUTH-PLAN G1–G3): re-run with a writer to stage these into\n")
	fmt.Printf("a shadow table, reconcile vs ledger_entries_current, merge.\n")
}
