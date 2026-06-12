package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/stellar/go-stellar-sdk/ingest"
	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/ledgerstream"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/sdex"
)

// sdexClaimAudit walks a ledger range and runs every classic-DEX claim atom
// through the real SDEX decode path (sdex.AuditOp), tallying which claims the
// decoder DROPS and why. It exists to give an exact diagnosis of SDEX
// trade-count gaps against external anchors (Hubble): Hubble counts one trade
// per claim atom, we emit one per atom we DON'T drop, so the drops here are
// exactly the off-by-N. Reports drops bucketed by reason + per-ledger, with
// examples. Read-only.
func sdexClaimAudit(args []string) error { //nolint:gocognit,gocyclo,funlen // linear walk + tally; splitting reduces clarity.
	fs := flag.NewFlagSet("sdex-claim-audit", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	from := fs.Uint("from", 0, "first ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "last ledger sequence (inclusive, required)")
	bucket := fs.String("bucket", "", "override storage bucket (default cfg.Storage.S3BucketLive)")
	examples := fs.Int("examples", 20, "max example drops to print")
	dumpOps := fs.Bool("dump-ops", false, "dump every trade op (type, result codes, claim count) — diagnostic for non-extraction")
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

	fmt.Fprintf(os.Stderr, "sdex-claim-audit: walking ledgers %d..%d from %q\n", *from, *to, streamBucket)

	var totalClaims, totalDrops int
	dropsByReason := map[string]int{}
	ledgersWithDrops := map[uint32]int{}
	oneSideZeroByLedger := map[uint32]int{}
	var exampleLines []string

	walkErr := ledgerstream.Stream(ctx, lsCfg, uint32(*from), uint32(*to),
		func(lcm sdkxdr.LedgerCloseMeta) error {
			seq := lcm.LedgerSequence()
			reader, rerr := ingest.NewLedgerTransactionReaderFromLedgerCloseMeta(passphrase, lcm)
			if rerr != nil {
				fmt.Fprintf(os.Stderr, "sdex-claim-audit: reader ledger %d: %v\n", seq, rerr)
				return nil
			}
			defer func() { _ = reader.Close() }()
			for {
				tx, terr := reader.Read()
				if errors.Is(terr, io.EOF) {
					break
				}
				if terr != nil || !tx.Result.Successful() {
					continue
				}
				ops := tx.Envelope.Operations()
				opResults, ok := tx.Result.Result.OperationResults()
				if !ok {
					continue
				}
				for i := range ops {
					if i >= len(opResults) {
						break
					}
					claims, drops := sdex.AuditOp(ops[i], opResults[i])
					if *dumpOps && isTradeOpType(ops[i].Body.Type) {
						inner, hasInner := innerTradeCode(ops[i], opResults[i])
						fmt.Printf("ledger=%d tx=%d op=%d type=%s outerCode=%d innerCode=%d(%v) claims=%d emitted=%d\n",
							seq, tx.Index, i, ops[i].Body.Type.String(), opResults[i].Code, inner, hasInner, claims, claims-len(drops))
					}
					totalClaims += claims
					for _, d := range drops {
						totalDrops++
						ledgersWithDrops[seq]++
						reason := classifyDrop(d.Reason)
						if strings.HasPrefix(reason, "non-positive: one-side-zero") {
							oneSideZeroByLedger[seq]++
						}
						dropsByReason[reason]++
						if len(exampleLines) < *examples {
							exampleLines = append(exampleLines, fmt.Sprintf("ledger=%d op=%d atomType=%d: %s", seq, i, d.AtomType, d.Reason))
						}
					}
				}
			}
			return nil
		},
	)
	if walkErr != nil && !errors.Is(walkErr, context.Canceled) {
		return fmt.Errorf("sdex-claim-audit: stream: %w", walkErr)
	}

	fmt.Printf("\n=== sdex-claim-audit: %d..%d ===\n", *from, *to)
	fmt.Printf("total claim atoms (= Hubble trade count): %d\n", totalClaims)
	fmt.Printf("total dropped (NOT emitted as trades):    %d\n", totalDrops)
	fmt.Printf("trades we emit (claims - drops):          %d\n", totalClaims-totalDrops)
	fmt.Printf("ledgers with >=1 drop:                    %d\n", len(ledgersWithDrops))

	reasons := make([]string, 0, len(dropsByReason))
	for r := range dropsByReason {
		reasons = append(reasons, r)
	}
	sort.Slice(reasons, func(i, j int) bool { return dropsByReason[reasons[i]] > dropsByReason[reasons[j]] })
	fmt.Printf("\ndrops by reason:\n")
	for _, r := range reasons {
		fmt.Printf("  %-24s %d\n", r, dropsByReason[r])
	}
	if len(oneSideZeroByLedger) > 0 {
		osz := make([]uint32, 0, len(oneSideZeroByLedger))
		for l := range oneSideZeroByLedger {
			osz = append(osz, l)
		}
		sort.Slice(osz, func(i, j int) bool { return osz[i] < osz[j] })
		fmt.Printf("\none-side-zero ledgers (%d):\n", len(osz))
		for _, l := range osz {
			fmt.Printf("  ledger=%d count=%d\n", l, oneSideZeroByLedger[l])
		}
	}
	if len(exampleLines) > 0 {
		fmt.Printf("\nexamples:\n")
		for _, l := range exampleLines {
			fmt.Printf("  %s\n", l)
		}
	}
	return nil
}

// isTradeOpType reports whether an op type can emit ClaimAtoms.
func isTradeOpType(t sdkxdr.OperationType) bool {
	switch t {
	case sdkxdr.OperationTypeManageSellOffer, sdkxdr.OperationTypeManageBuyOffer,
		sdkxdr.OperationTypeCreatePassiveSellOffer, sdkxdr.OperationTypePathPaymentStrictReceive,
		sdkxdr.OperationTypePathPaymentStrictSend:
		return true
	}
	return false
}

// innerTradeCode returns the op's inner result code (the per-type result arm's
// Code) for diagnosing why claim extraction yields nothing.
func innerTradeCode(op sdkxdr.Operation, r sdkxdr.OperationResult) (int32, bool) {
	if r.Code != sdkxdr.OperationResultCodeOpInner {
		return 0, false
	}
	tr, ok := r.GetTr()
	if !ok {
		return 0, false
	}
	switch op.Body.Type {
	case sdkxdr.OperationTypeManageSellOffer:
		if x, ok := tr.GetManageSellOfferResult(); ok {
			return int32(x.Code), true
		}
	case sdkxdr.OperationTypeManageBuyOffer:
		if x, ok := tr.GetManageBuyOfferResult(); ok {
			return int32(x.Code), true
		}
	case sdkxdr.OperationTypeCreatePassiveSellOffer:
		if x, ok := tr.GetCreatePassiveSellOfferResult(); ok {
			return int32(x.Code), true
		}
	case sdkxdr.OperationTypePathPaymentStrictReceive:
		if x, ok := tr.GetPathPaymentStrictReceiveResult(); ok {
			return int32(x.Code), true
		}
	case sdkxdr.OperationTypePathPaymentStrictSend:
		if x, ok := tr.GetPathPaymentStrictSendResult(); ok {
			return int32(x.Code), true
		}
	}
	return 0, false
}

// classifyDrop buckets a decoder error string into a stable reason category.
func classifyDrop(reason string) string {
	switch {
	case strings.Contains(reason, "non-positive amounts"):
		// Split: both-zero claims are not real trades (Hubble drops them too,
		// no mismatch); one-side-zero claims ARE trades Hubble records but our
		// OR-guard rejects — the exact off-by-one vs Hubble.
		if strings.Contains(reason, "sold=0 bought=0") {
			return "non-positive: both-zero (Hubble also drops)"
		}
		return "non-positive: one-side-zero (Hubble counts -> off-by-one)"
	case strings.Contains(reason, "pair:"):
		return "invalid-pair (base==quote?)"
	case strings.Contains(reason, "sold asset"), strings.Contains(reason, "bought asset"):
		return "asset-conversion"
	case strings.Contains(reason, "type="):
		return "unknown-claim-atom-type"
	default:
		return "other"
	}
}
