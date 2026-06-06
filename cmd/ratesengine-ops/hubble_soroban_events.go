package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"
)

// ─── ratesengine-ops hubble-soroban-events ─────────────────────────
//
// Per-ledger event-count primitive against
// `crypto-stellar.crypto_stellar.history_contract_events` for a
// specified set of contract IDs, with an optional topic[0]/topic[1]
// filter.
//
// Why this exists. The SDEX hubble-check (#172, #183) queries
// `history_trades`, which doesn't cover Soroban DEXes — Hubble has
// no decoded view of Soroswap/Aquarius/Phoenix/Comet swaps; their
// events sit raw in `history_contract_events`. So decoder-coverage
// cross-checks for those sources need a different primitive: query
// Hubble for the events that *would* have produced a trade, then
// compare to our trades count for the same range outside this tool.
//
// Why this is NOT a built-in cross-check. Each Soroban source has
// a different (events ↔ trades) ratio:
//
//   - Soroswap: 2 events per trade (swap + sync) → filter topic[1]='swap' to count one event per trade
//   - Aquarius: 1 event per trade (topic[0]='trade')
//   - Phoenix:  8 events per trade (one per field) → filter topic[1]='offer_amount' for one-per-trade
//   - Comet:    1 event per swap (topic[0]='POOL', topic[1]='swap')
//   - Reflector: 1 event fans out to N OracleUpdate rows in our DB
//   - Redstone:  1 event per write_prices call → fans to N rows
//   - Band:      ZERO events; this tool can't detect Band coverage
//
// Bundling all those rules into the tool would make it a
// per-source-quirk amalgam. Instead, this tool emits the raw
// per-ledger Hubble counts; the operator runs it with the right
// filter for the source and compares to our DB row counts using
// the documented per-source ratio. See
// docs/operations/hubble-event-counts.md for the recipe.
//
// Cost. SELECT/COUNT/GROUP-BY against history_contract_events
// scans more than history_trades because the events table is
// per-event, not per-trade. Ballpark 20–40 GB per 1M-ledger range
// with a contract_id filter. -dry-run-bytes prints the byte
// estimate before executing.

func hubbleSorobanEvents(args []string) error {
	fs := flag.NewFlagSet("hubble-soroban-events", flag.ContinueOnError)
	from := fs.Uint("from", 0, "starting ledger sequence, inclusive (required)")
	to := fs.Uint("to", 0, "ending ledger sequence, inclusive (required)")
	project := fs.String("bigquery-project", "",
		"GCP project ID to bill BigQuery queries against (required)")
	contractsCSV := fs.String("contracts", "",
		"comma-separated Soroban contract C-strkey IDs to filter on (required)")
	topic0 := fs.String("topic0", "",
		"optional ScVal::Symbol filter for topic[0]; matches contract event payload encoding")
	topic1 := fs.String("topic1", "",
		"optional ScVal::Symbol filter for topic[1]")
	output := fs.String("output", "json",
		"output format: json (per-ledger counts) | total (single count) | csv")
	dryRunBytes := fs.Bool("dry-run-bytes", false,
		"print BigQuery dry-run byte estimate, then exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *from == 0 {
		return errors.New("-from must be > 0")
	}
	if *to <= *from {
		return fmt.Errorf("-to (%d) must be > -from (%d)", *to, *from)
	}
	if *project == "" {
		return errors.New("-bigquery-project required")
	}
	if *contractsCSV == "" {
		return errors.New("-contracts required (one or more comma-separated C-strkeys)")
	}
	contracts := splitCSV(*contractsCSV)
	if len(contracts) == 0 {
		return errors.New("-contracts parsed to empty list")
	}
	switch *output {
	case "json", "total", "csv":
	default:
		return fmt.Errorf("-output must be one of json|total|csv; got %q", *output)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	bqClient, err := bigquery.NewClient(ctx, *project)
	if err != nil {
		return fmt.Errorf("bigquery client: %w", err)
	}
	defer func() { _ = bqClient.Close() }()

	if *dryRunBytes {
		bytes, err := hubbleSorobanEventsDryRun(ctx, bqClient,
			uint32(*from), uint32(*to), contracts, *topic0, *topic1)
		if err != nil {
			return fmt.Errorf("dry-run: %w", err)
		}
		_, _ = fmt.Fprintf(os.Stderr,
			"hubble-soroban-events dry-run: would scan ~%d bytes (~$%.4f at $5/TB on-demand)\n",
			bytes, float64(bytes)/1e12*5.0)
		return nil
	}

	counts, err := fetchHubbleSorobanEventCounts(ctx, bqClient,
		uint32(*from), uint32(*to), contracts, *topic0, *topic1)
	if err != nil {
		return fmt.Errorf("query Hubble: %w", err)
	}

	return emitHubbleSorobanCounts(counts, *output)
}

// ledgerCount is the per-ledger output record for the JSON / CSV
// emission paths.
type ledgerCount struct {
	Ledger uint32 `json:"ledger"`
	Count  int    `json:"count"`
}

// fetchHubbleSorobanEventCounts runs the GROUP-BY query and returns
// per-ledger counts for the matched events. Ledgers with zero
// matching events DON'T appear (sparse representation).
func fetchHubbleSorobanEventCounts(
	ctx context.Context,
	client *bigquery.Client,
	from, to uint32,
	contracts []string,
	topic0, topic1 string,
) (map[uint32]int, error) {
	query, params := buildSorobanEventsQuery(from, to, contracts, topic0, topic1)
	q := client.Query(query)
	q.Parameters = params
	it, err := q.Read(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[uint32]int, 1024)
	for {
		var row struct {
			Ledger int64 `bigquery:"ledger"`
			N      int64 `bigquery:"n"`
		}
		err := it.Next(&row)
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		out[uint32(row.Ledger)] = int(row.N)
	}
	return out, nil
}

// hubbleSorobanEventsDryRun returns the BigQuery dry-run byte
// estimate for the same query the live path would run. Useful so
// operators can preview cost before running for real (events table
// is much larger than trades table).
func hubbleSorobanEventsDryRun(
	ctx context.Context,
	client *bigquery.Client,
	from, to uint32,
	contracts []string,
	topic0, topic1 string,
) (int64, error) {
	query, params := buildSorobanEventsQuery(from, to, contracts, topic0, topic1)
	q := client.Query(query)
	q.Parameters = params
	q.DryRun = true
	job, err := q.Run(ctx)
	if err != nil {
		return 0, err
	}
	stat := job.LastStatus()
	if stat == nil || stat.Statistics == nil {
		return 0, errors.New("bigquery returned no statistics for dry-run")
	}
	return stat.Statistics.TotalBytesProcessed, nil
}

// buildSorobanEventsQuery returns the SQL + bound parameters for the
// per-ledger Hubble event-count query. Builds the optional topic
// filters into the WHERE clause; empty filter strings drop the
// corresponding AND. Pulled out of fetch + dry-run so both paths
// run literally the same SQL.
func buildSorobanEventsQuery(
	from, to uint32,
	contracts []string,
	topic0, topic1 string,
) (string, []bigquery.QueryParameter) {
	// COUNT(DISTINCT contract_event_xdr): Hubble's history_contract_events can
	// carry duplicate rows from overlapping batch loads (observed 2× on some
	// ranges), so a raw COUNT(*) over-reports. Dedup by the event's raw XDR —
	// its identity — to match our one-row-per-event ClickHouse lake.
	q := "SELECT closed_at, ledger_sequence AS ledger, COUNT(DISTINCT contract_event_xdr) AS n " +
		"FROM `crypto-stellar.crypto_stellar.history_contract_events` " +
		"WHERE ledger_sequence BETWEEN @from AND @to " +
		"  AND type_string = 'ContractEventTypeContract' " +
		"  AND contract_id IN UNNEST(@contracts) "

	params := []bigquery.QueryParameter{
		{Name: "from", Value: int64(from)},
		{Name: "to", Value: int64(to)},
		{Name: "contracts", Value: contracts},
	}
	if topic0 != "" {
		q += " AND topic_1 = @topic0 "
		params = append(params, bigquery.QueryParameter{Name: "topic0", Value: topic0})
	}
	if topic1 != "" {
		q += " AND topic_2 = @topic1 "
		params = append(params, bigquery.QueryParameter{Name: "topic1", Value: topic1})
	}
	q += " GROUP BY closed_at, ledger_sequence ORDER BY ledger"
	return q, params
}

// emitHubbleSorobanCounts writes the result in the requested format
// to stdout. JSON is an array of {ledger, count}; CSV mirrors the
// same. "total" emits one number — sum across the range — for
// piping into shell arithmetic.
func emitHubbleSorobanCounts(counts map[uint32]int, format string) error {
	ledgers := make([]uint32, 0, len(counts))
	for k := range counts {
		ledgers = append(ledgers, k)
	}
	sort.Slice(ledgers, func(i, j int) bool { return ledgers[i] < ledgers[j] })

	switch format {
	case "total":
		total := 0
		for _, l := range ledgers {
			total += counts[l]
		}
		_, err := fmt.Fprintf(os.Stdout, "%d\n", total)
		return err
	case "csv":
		_, _ = fmt.Fprintln(os.Stdout, "ledger,count")
		for _, l := range ledgers {
			if _, err := fmt.Fprintf(os.Stdout, "%d,%d\n", l, counts[l]); err != nil {
				return err
			}
		}
		return nil
	default: // json
		out := make([]ledgerCount, 0, len(ledgers))
		for _, l := range ledgers {
			out = append(out, ledgerCount{Ledger: l, Count: counts[l]})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
}
