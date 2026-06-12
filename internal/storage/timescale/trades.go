package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/StellarAtlas/stellar-atlas/internal/aggregate"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/external"
)

// externalUSDVolumeDecimals is the off-chain quote-amount scale.
// Every CEX/FX source stamps amounts at the uniform 10^8 decimal
// convention per
// `internal/sources/external/<venue>::externalAmountDecimals`, so a
// quote amount of 4_250_000_000 means $42.50.
const externalUSDVolumeDecimals = 8

// USDVolumeFXResolver returns the asset's USD price as of `at` as
// a decimal string ("0.07127", "1.00", …) for use by
// [tradeUSDVolume]'s Phase 2 fallback path.
//
// Returns ("", false, nil) when no USD anchor is available for
// the asset (the resolver has no rate, the rate is too stale, the
// asset isn't on the operator's covered set). Real I/O errors
// propagate so the caller can surface them in metrics; the trade
// still inserts, just with `usd_volume` left NULL.
//
// Production wiring (F-1268 audit-2026-05-12 — landed):
// [VWAPUSDFXResolver] queries `prices_1m` for `<asset>/<peg>`
// per configured peg, caches per-(asset, 1-minute bucket), and
// expires entries past a freshness ceiling. Wired in
// `cmd/ratesengine-indexer/main.go` whenever the operator's
// `[trades].usd_pegged_classic_assets` list is non-empty.
//
// Concurrency: the resolver is invoked from the trade-insert
// hot path, possibly across many goroutines for high-fanout
// indexers. Implementations MUST be safe for concurrent
// USDPriceAt calls.
type USDVolumeFXResolver interface {
	USDPriceAt(ctx context.Context, asset canonical.Asset, at time.Time) (string, bool, error)
}

// tradeUSDVolume returns the per-trade USD-equivalent volume as a
// NUMERIC-compatible string, or nil when the trade can't be
// converted cleanly. Returning a *string lets the caller pass the
// value (or sql NULL) straight into the trades.usd_volume column.
//
// Three resolution tiers, tried in order:
//
//  1. Off-chain CEX/FX source AND quote is fiat:USD or a
//     USD-pegged stablecoin per `aggregate.FiatProxy`
//     (USDC/USDT/DAI/PYUSD/USDP). Decimals: 8 (uniform external
//     scale).
//
//  2. On-chain DEX source AND `quoteSpec` recognises the quote
//     asset as USD-pegged (operator-declared classic credits +
//     their SAC wrappers per [USDVolumeQuoteSpec]). Decimals: 7
//     (Stellar classic invariant).
//
//  3. **L2.2 Phase 2** — On-chain DEX source AND `fxResolver !=
//     nil` returns a USD rate for the quote asset at the trade's
//     timestamp. quote_amount × USDPrice / 10^classicDecimals
//     produces a non-NULL `usd_volume` for any operator-watched
//     quote with a recent VWAP. Off-chain trades that fell
//     through tier 1 also get a tier-3 attempt — covers a CEX
//     pair quoted in a non-stablecoin (e.g. binance:XLM/BTC).
//
// Tiers 1 + 2 trust their pegs at insert time — depeg events
// are observed separately via the divergence + anomaly paths and
// do NOT change the inserted usd_volume retroactively. Tier 3 is
// time-anchored to the trade's timestamp; the resolver MAY return
// (false) on stale data, in which case the column stays NULL.
//
// Everything else returns nil and the column stays NULL — neither
// over-claiming USD-equivalence on unknown quotes (would mislead
// downstream sums) nor silently dropping the trade itself (the row
// still inserts; only the USD column goes NULL).
func tradeUSDVolume(ctx context.Context, t canonical.Trade, quoteSpec *USDVolumeQuoteSpec, fxResolver USDVolumeFXResolver) *string {
	q := t.QuoteAmount.BigInt()
	if q == nil || q.Sign() <= 0 {
		return nil
	}
	md := external.Lookup(t.Source)
	if md.Class != external.ClassExchange {
		// Oracles and aggregators don't emit Trades — defensive nil
		// keeps the function honest if a misregistered source ever
		// sneaks one in.
		return nil
	}
	if decimals, ok := usdVolumeDecimals(t.Pair.Quote, md.Subclass, quoteSpec); ok {
		denom := scaleDenominator(decimals)
		// FloatString(8) gives a fixed-precision decimal — Postgres
		// NUMERIC accepts the form directly with no precision loss
		// for any value that fit in the original big.Int (NUMERIC is
		// arbitrary-precision; FloatString just chooses a render).
		rendered := new(big.Rat).SetFrac(q, denom).FloatString(8)
		return &rendered
	}
	// Phase 2 fallback — only when the operator wired an FX
	// resolver. Skip when nil to keep the no-config path on the
	// existing Phase 1 behaviour exactly.
	if fxResolver == nil {
		return nil
	}
	return tradeUSDVolumeViaFX(ctx, t, md.Subclass, fxResolver)
}

// tradeUSDVolumeViaFX is the L2.2 Phase 2 multiplication path.
// Picks the right scale per source class (CEX/FX = 8, DEX = 7),
// asks the resolver for the quote asset's USD price at the trade
// time, and renders quote_amount × usdRate / 10^decimals as a
// fixed-precision NUMERIC string.
//
// Errors in the resolver are silent here — the function returns
// nil so the trade still inserts with NULL `usd_volume`, and the
// caller (InsertTrade) doesn't fail the row. Operators read the
// fall-through rate via [TradeInsertsTotal]'s no-label series.
func tradeUSDVolumeViaFX(ctx context.Context, t canonical.Trade, subclass external.Subclass, r USDVolumeFXResolver) *string {
	if r == nil {
		return nil
	}
	usdRateStr, ok, err := r.USDPriceAt(ctx, t.Pair.Quote, t.Timestamp)
	if err != nil || !ok || usdRateStr == "" {
		return nil
	}
	usdRate, ok := new(big.Rat).SetString(usdRateStr)
	if !ok || usdRate.Sign() <= 0 {
		return nil
	}
	var decimals int
	switch subclass {
	case external.SubclassCEX, external.SubclassFX:
		decimals = externalUSDVolumeDecimals
	case external.SubclassDEX:
		decimals = stellarClassicDecimals
	default:
		return nil
	}
	q := new(big.Rat).SetFrac(t.QuoteAmount.BigInt(), scaleDenominator(decimals))
	usdAmount := new(big.Rat).Mul(q, usdRate)
	rendered := usdAmount.FloatString(8)
	return &rendered
}

// stellarClassicDecimals is the smallest-unit-to-display divisor
// power baked into Stellar classic — XLM, native, every classic
// credit. SEP-41 contracts publish their own value via decimals();
// the FX-fallback path here only fires for on-chain DEX trades
// whose Stellar-side quote assets all share this scale.
const stellarClassicDecimals = 7

// WouldPopulateUSDVolume reports whether [Store.InsertTrade] would
// stamp a non-null `usd_volume` for this trade given the store's
// currently-configured [USDVolumeQuoteSpec] AND
// [USDVolumeFXResolver]. Safe for callers (e.g. the pipeline sink
// emitting coverage metrics) to invoke before InsertTrade.
//
// The predicate runs the full three-tier resolution: Phase 1
// (off-chain CEX/FX with USD-pegged quote → tier 1; on-chain DEX
// with operator-allow-listed quote → tier 2) and Phase 2 (any
// remaining trade with an FX-resolver hit → tier 3).
//
// Note: a Phase 2 hit makes a synchronous call into the configured
// resolver. Production resolvers MUST be cheap enough for the
// trade-insert hot path (typically an in-memory cache lookup —
// see the package doc).
func (s *Store) WouldPopulateUSDVolume(ctx context.Context, t canonical.Trade) bool {
	return tradeUSDVolume(ctx, t, s.usdVolumeQuoteSpec, s.usdVolumeFXResolver) != nil
}

// usdVolumeDecimals picks the correct decimal scale for the trade's
// quote asset given the source's subclass + the operator's quote
// spec. Returns (0, false) when the trade isn't a USD-volume
// candidate.
func usdVolumeDecimals(quote canonical.Asset, subclass external.Subclass, quoteSpec *USDVolumeQuoteSpec) (int, bool) {
	switch subclass {
	case external.SubclassCEX, external.SubclassFX:
		// Off-chain — uniform externalAmountDecimals, peg via the
		// crypto-ticker FiatProxy.
		if !quoteIsUSDOrUSDPegged(quote) {
			return 0, false
		}
		return externalUSDVolumeDecimals, true
	case external.SubclassDEX:
		// On-chain (Stellar SDEX, Soroswap, Aquarius, Phoenix,
		// Comet) — peg + decimals come from the operator's
		// USDVolumeQuoteSpec. Phase 1 covers classic + SAC; pure
		// SEP-41 stablecoins are phase 2.
		return quoteSpec.QuoteUSDPegInfo(quote)
	default:
		return 0, false
	}
}

// scaleDenominator returns 10^decimals as a *big.Int. Decimals are
// always small (≤ 18) so the exponent is cheap.
func scaleDenominator(decimals int) *big.Int {
	out := big.NewInt(1)
	ten := big.NewInt(10)
	for range decimals {
		out.Mul(out, ten)
	}
	return out
}

// quoteIsUSDOrUSDPegged is true when the asset is fiat:USD or a
// stablecoin that aggregate.FiatProxy maps to USD. The peg is
// trusted at insert time — depeg events are observed separately
// via the divergence + anomaly paths and do NOT change the inserted
// usd_volume retroactively (a depegged USDT trade still carries
// its observed quote_amount, which is the right historical record).
func quoteIsUSDOrUSDPegged(a canonical.Asset) bool {
	if a.Type == canonical.AssetFiat && a.Code == "USD" {
		return true
	}
	proxy, ok := aggregate.FiatProxy(a)
	if !ok {
		return false
	}
	return proxy.Type == canonical.AssetFiat && proxy.Code == "USD"
}

// ErrNoFXQuote is returned by [Store.FXQuoteAtOrBefore] when no FX
// observation exists for the requested pair at-or-before the cutoff.
// Callers fall back to the cached VWAP path (degraded but functional)
// and surface the fallback via the AggregatorFXSnapFallbackTotal metric.
var ErrNoFXQuote = errors.New("timescale: no FX quote at or before cutoff")

// InsertTrade writes one trade. Returns nil for a successful insert
// OR a duplicate-key clash (idempotent by storage identity — the
// current conflict target is source+ledger+tx_hash+op_index+ts).
// Other errors propagate.
//
// The trade is validated via [canonical.Trade.Validate] before
// touching the DB; a Validate failure returns [canonical.ErrInvalidTrade].
//
// `usd_volume` is computed via [tradeUSDVolume] for both off-chain
// (CEX/FX) and on-chain (DEX) sources whose quote asset is recognised
// as USD-pegged. Off-chain coverage is built-in via the crypto-ticker
// `aggregate.FiatProxy` map; on-chain coverage requires the operator
// to install a [USDVolumeQuoteSpec] via [Store.SetUSDVolumeQuoteSpec]
// declaring which classic credits (and their SAC wrappers, transitive)
// they trust as USD-pegged. Everything else stores NULL — see the
// L2.2 caveat documented on `Volume24hUSDForAsset` and
// `internal/api/v1.VolumeReader`.
func (s *Store) InsertTrade(ctx context.Context, t canonical.Trade) error {
	if err := t.Validate(); err != nil {
		return err
	}

	// One statement, two effects, fully atomic:
	//   1. Insert the trade (idempotent on its PK).
	//   2. If — and only if — a row was actually inserted, bump the
	//      per-source entry tally (migration 0035). The `HAVING
	//      count(*) > 0` makes the counter upsert produce zero rows
	//      on a duplicate, so a backfill re-walk over already-stored
	//      ledgers never inflates the tally. Data-modifying CTEs
	//      always execute even though `bump` is unreferenced.
	// The trailing `SELECT count(*) FROM ins` returns 1 (new) or 0
	// (duplicate) — an explicit count, sturdier than the old
	// RowsAffected() path (no driver-quirk fail-open ambiguity).
	const q = `
        WITH ins AS (
            INSERT INTO trades (
                source, ledger, tx_hash, op_index, ts,
                base_asset, quote_asset,
                base_amount, quote_amount, usd_volume,
                maker, taker
            ) VALUES (
                $1, $2, $3, $4, $5,
                $6, $7,
                $8, $9, $10,
                NULLIF($11, ''), NULLIF($12, '')
            )
            ON CONFLICT (source, ledger, tx_hash, op_index, ts) DO NOTHING
            RETURNING 1
        ), bump AS (
            INSERT INTO source_entry_counts AS sec (source, entry_count, updated_at)
            SELECT $1, count(*), now() FROM ins
            HAVING count(*) > 0
            ON CONFLICT (source) DO UPDATE
              SET entry_count = sec.entry_count + EXCLUDED.entry_count,
                  updated_at  = EXCLUDED.updated_at
        )
        SELECT count(*) FROM ins
    `
	var usdVolume any // sql NULL when nil; pq accepts the *string form too
	if v := tradeUSDVolume(ctx, t, s.usdVolumeQuoteSpec, s.usdVolumeFXResolver); v != nil {
		usdVolume = *v
	}
	var rowsInserted int64
	if err := s.db.QueryRowContext(ctx, q,
		t.Source, t.Ledger, t.TxHash, t.OpIndex, t.Timestamp.UTC(),
		t.Pair.Base.String(), t.Pair.Quote.String(),
		t.BaseAmount, t.QuoteAmount, usdVolume,
		t.Maker, t.Taker,
	).Scan(&rowsInserted); err != nil {
		return fmt.Errorf("timescale: InsertTrade: %w", err)
	}

	// Emit per-source outcome metric (new vs duplicate) so operators
	// can detect a cursor-replay / stuck-tip pattern via
	// `rate(ratesengine_trade_insert_outcome_total{outcome="new"}[5m]) == 0`
	// while attempts (TradeInsertsTotal) keep climbing. See
	// obs.TradeInsertOutcomeTotal.
	outcome := "new"
	if rowsInserted == 0 {
		outcome = "duplicate"
	}
	obs.TradeInsertOutcomeTotal.WithLabelValues(t.Source, outcome).Inc()

	// F-1243 (codex audit-2026-05-13) second half: skip the
	// registry hook when the trade was a duplicate (rowsInserted
	// = 0 from the `ON CONFLICT DO NOTHING`). The wave-47 TTL fix
	// already addressed the freeze; this guard fixes the
	// observation_count drift on backfill replays / process
	// restarts that re-encounter already-stored trades.
	if rowsInserted == 0 {
		return nil
	}

	// Stamp wall-clock when a fresh row actually lands. Pairs with
	// obs.SourceLastEventUnix to detect the stuck-cursor /
	// duplicate-flood pattern: when last_event_unix keeps climbing
	// but last_insert_unix flat-lines, the cursor is processing
	// events that produce only duplicate inserts. See the metric
	// godoc + ratesengine_ingestion_duplicate_flood alert.
	obs.SourceLastInsertUnix.WithLabelValues(t.Source).Set(float64(time.Now().Unix()))

	// Phase 4 (per migration 0023's docblock): auto-register the
	// classic-asset registry from observed trades. Errors are
	// soft-failures — the trade row is committed, we just log+skip
	// the registry update so the hot path can't be sunk by a
	// registry-side problem. Dedupe-cached so this is a no-op for
	// every asset/issuer after the first touch in the process.
	for _, side := range [2]canonical.Asset{t.Pair.Base, t.Pair.Quote} {
		if regErr := s.registerClassicAssetSeen(ctx, side, t.Ledger, t.Timestamp); regErr != nil {
			// Soft-fail: the trade row is committed; a registry-side
			// problem must not sink the hot path. Stay quiet at info
			// level, but DON'T swallow silently (audit-2026-06-11
			// G11-08). registerClassicAssetSeen is dedupe-cached, so
			// this naturally fires at most once per (asset,issuer) per
			// process — already rate-limited. Debug level keeps the
			// steady state silent while leaving a breadcrumb when an
			// operator turns up logging to chase registry drift.
			slog.Default().Debug("timescale: classic-asset registry upsert failed (soft-skip)",
				"asset", side.String(),
				"ledger", t.Ledger,
				"err", regErr,
			)
		}
	}
	return nil
}

// BatchInsertTrades writes up to a few hundred trades in a single
// SQL roundtrip. Live-r1 incident 2026-06-01: per-INSERT roundtrip
// latency capped indexer throughput at ~5 inserts/sec despite
// postgres-side capacity > 9000/sec (verified by raw psql loop).
// Batching collapses N roundtrips into 1, lifting throughput by
// roughly the batch factor.
//
// Same idempotency semantics as [Store.InsertTrade]:
// `ON CONFLICT DO NOTHING` on the trade PK, and the
// `source_entry_counts` UPSERT only bumps the per-source tally by
// the number of rows actually inserted (so re-runs over already-
// stored ledgers don't inflate the count).
//
// Caller-side filtering: rows are NOT pre-validated by this
// function — callers MUST `Validate` each trade before queueing it
// into a batch (the sink layer already does). USD-volume is computed
// per row from the store's USD-volume resolver, same as the single
// row path.
//
// Returns nil on success; on any DB error the whole batch fails and
// the caller's outcome metric should reflect that. The error is
// best-effort wrapped with `timescale: BatchInsertTrades: %w`. There
// is no partial-success semantic — either every row is attempted (and
// individual rows may be duplicate-absorbed), or the whole batch
// fails.
func (s *Store) BatchInsertTrades(ctx context.Context, trades []canonical.Trade) error {
	if len(trades) == 0 {
		return nil
	}

	// Build VALUES placeholders + args slice. Each row has 12 params
	// (source, ledger, tx_hash, op_index, ts, base_asset, quote_asset,
	// base_amount, quote_amount, usd_volume, maker, taker).
	const colsPerRow = 12
	args := make([]any, 0, len(trades)*colsPerRow)
	valuesParts := make([]string, 0, len(trades))
	for i, t := range trades {
		base := i*colsPerRow + 1
		valuesParts = append(valuesParts, fmt.Sprintf(
			"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, NULLIF($%d, ''), NULLIF($%d, ''))",
			base, base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11,
		))
		var usdVolume any
		if v := tradeUSDVolume(ctx, t, s.usdVolumeQuoteSpec, s.usdVolumeFXResolver); v != nil {
			usdVolume = *v
		}
		args = append(args,
			t.Source, t.Ledger, t.TxHash, t.OpIndex, t.Timestamp.UTC(),
			t.Pair.Base.String(), t.Pair.Quote.String(),
			t.BaseAmount, t.QuoteAmount, usdVolume,
			t.Maker, t.Taker,
		)
	}

	// CTE shape:
	//   ins → multi-row INSERT, RETURNING source for each row that
	//         actually landed (i.e. wasn't a duplicate).
	//   bump → aggregate landed rows per source, UPSERT into
	//          source_entry_counts.
	// The bump's SELECT … GROUP BY pattern is the multi-source twin
	// of the single-row variant in InsertTrade.
	//nolint:gosec // G201: VALUES placeholders constructed only from compile-time format string.
	query := fmt.Sprintf(`
        WITH ins AS (
            INSERT INTO trades (
                source, ledger, tx_hash, op_index, ts,
                base_asset, quote_asset,
                base_amount, quote_amount, usd_volume,
                maker, taker
            ) VALUES %s
            ON CONFLICT (source, ledger, tx_hash, op_index, ts) DO NOTHING
            RETURNING source
        ), bump AS (
            INSERT INTO source_entry_counts AS sec (source, entry_count, updated_at)
            SELECT source, count(*), now() FROM ins GROUP BY source
            ON CONFLICT (source) DO UPDATE
              SET entry_count = sec.entry_count + EXCLUDED.entry_count,
                  updated_at  = EXCLUDED.updated_at
        )
        SELECT source, count(*) FROM ins GROUP BY source
    `, strings.Join(valuesParts, ", "))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("timescale: BatchInsertTrades: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Counts per source for outcome metrics: how many landed (new) +
	// how many were absorbed by ON CONFLICT (duplicate).
	perSourceNew := make(map[string]int, 4)
	for rows.Next() {
		var source string
		var n int
		if err := rows.Scan(&source, &n); err != nil {
			return fmt.Errorf("timescale: BatchInsertTrades scan: %w", err)
		}
		perSourceNew[source] = n
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("timescale: BatchInsertTrades rows.Err: %w", err)
	}

	// Per-source totals (we know how many we sent per source from
	// the input slice). Duplicates = sent - landed.
	perSourceSent := make(map[string]int, len(perSourceNew))
	for _, t := range trades {
		perSourceSent[t.Source]++
	}
	now := float64(time.Now().Unix())
	for source, sent := range perSourceSent {
		landed := perSourceNew[source]
		duplicate := sent - landed
		if landed > 0 {
			obs.TradeInsertOutcomeTotal.WithLabelValues(source, "new").Add(float64(landed))
			obs.SourceLastInsertUnix.WithLabelValues(source).Set(now)
		}
		if duplicate > 0 {
			obs.TradeInsertOutcomeTotal.WithLabelValues(source, "duplicate").Add(float64(duplicate))
		}
	}

	// Asset-registry side effects: skip in the batch path. The
	// registry is dedupe-cached per process, and a missing
	// registration is soft-fail — it'll get picked up on the next
	// single-row InsertTrade for the same asset (e.g. backfill
	// subcommand). The hot batch path stays narrow.
	return nil
}

// LatestTradesForPair returns up to `limit` most-recent trades for
// the given ordered pair. Returns an empty slice + nil error if the
// pair has no trades.
func (s *Store) LatestTradesForPair(ctx context.Context, p canonical.Pair, limit int) ([]canonical.Trade, error) {
	if limit <= 0 {
		limit = 100
	}
	const q = `
        SELECT source, ledger, tx_hash, op_index, ts,
               base_asset, quote_asset,
               base_amount, quote_amount,
               COALESCE(maker, ''), COALESCE(taker, '')
          FROM trades
         WHERE base_asset  = $1
           AND quote_asset = $2
         ORDER BY ts DESC, ledger DESC
         LIMIT $3
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: LatestTradesForPair: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []canonical.Trade
	for rows.Next() {
		var t canonical.Trade
		var baseAsset, quoteAsset string
		if err := rows.Scan(
			&t.Source, &t.Ledger, &t.TxHash, &t.OpIndex, &t.Timestamp,
			&baseAsset, &quoteAsset,
			&t.BaseAmount, &t.QuoteAmount,
			&t.Maker, &t.Taker,
		); err != nil {
			return nil, fmt.Errorf("timescale: LatestTradesForPair scan: %w", err)
		}
		// Reconstruct Pair via the canonical parse path — this also
		// enforces shape invariants on read.
		base, err := canonical.ParseAsset(baseAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestTradesForPair base %q: %w", baseAsset, err)
		}
		quote, err := canonical.ParseAsset(quoteAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestTradesForPair quote %q: %w", quoteAsset, err)
		}
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestTradesForPair pair: %w", err)
		}
		t.Pair = pair
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: LatestTradesForPair rows: %w", err)
	}
	return out, nil
}

// LatestTradePerSource returns the most-recent trade from each source
// that has ever traded `pair`. Empty slice + nil error when the pair
// has no trades.
//
// sourceFilter "" returns all sources; a non-empty value restricts to
// that single source (0- or 1-element slice). Filtering at the SQL
// layer means a single-source query is just an index point lookup.
//
// Implementation: DISTINCT ON (source) ordered by ts DESC, ledger DESC
// — cheap because trades_pair_source_ts_idx (migration 0037) covers
// the (base_asset, quote_asset, source, ts DESC, ledger DESC) order
// exactly. The cost is ~O(num_sources) per pair rather than
// O(rows_in_pair).
func (s *Store) LatestTradePerSource(ctx context.Context, p canonical.Pair, sourceFilter string) ([]canonical.Trade, error) {
	const q = `
        SELECT DISTINCT ON (source)
               source, ledger, tx_hash, op_index, ts,
               base_asset, quote_asset,
               base_amount, quote_amount,
               COALESCE(maker, ''), COALESCE(taker, '')
          FROM trades
         WHERE base_asset  = $1
           AND quote_asset = $2
           AND ($3 = '' OR source = $3)
         ORDER BY source, ts DESC, ledger DESC
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(), sourceFilter,
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: LatestTradePerSource: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []canonical.Trade
	for rows.Next() {
		var t canonical.Trade
		var baseAsset, quoteAsset string
		if err := rows.Scan(
			&t.Source, &t.Ledger, &t.TxHash, &t.OpIndex, &t.Timestamp,
			&baseAsset, &quoteAsset,
			&t.BaseAmount, &t.QuoteAmount,
			&t.Maker, &t.Taker,
		); err != nil {
			return nil, fmt.Errorf("timescale: LatestTradePerSource scan: %w", err)
		}
		base, err := canonical.ParseAsset(baseAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestTradePerSource base %q: %w", baseAsset, err)
		}
		quote, err := canonical.ParseAsset(quoteAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestTradePerSource quote %q: %w", quoteAsset, err)
		}
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestTradePerSource pair: %w", err)
		}
		t.Pair = pair
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: LatestTradePerSource rows: %w", err)
	}
	return out, nil
}

// TradesInRange returns trades for the given pair whose close-time
// falls in [from, to). Ordered by (ts ASC, ledger ASC) — chronological,
// which is what OHLC / VWAP callers want.
//
// limit clamps the returned count to avoid runaway queries; pass 0
// or negative for the default of 1000. The hard ceiling is 10000.
//
// An empty slice + nil error means the pair has no trades in the
// window — not an error. Callers distinguish "empty" from "error"
// by testing len(rows).
func (s *Store) TradesInRange(ctx context.Context, p canonical.Pair, from, to time.Time, limit int) ([]canonical.Trade, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}
	if to.Before(from) {
		return nil, fmt.Errorf("timescale: TradesInRange: to %v < from %v", to, from)
	}
	// Order DESC so the LIMIT keeps the NEWEST rows when the window has
	// more than `limit` trades, then reverse to ascending below. The
	// previous `ORDER BY ts ASC LIMIT` kept the OLDEST `limit` rows, so a
	// busy 1h/24h VWAP was computed from a stale slice that began at the
	// window start and stopped ~limit trades later, never reaching the
	// present (F-1319). Callers still receive ascending order; only which
	// rows survive truncation changed (newest, not oldest).
	const q = `
        SELECT source, ledger, tx_hash, op_index, ts,
               base_asset, quote_asset,
               base_amount, quote_amount,
               COALESCE(maker, ''), COALESCE(taker, '')
          FROM trades
         WHERE base_asset  = $1
           AND quote_asset = $2
           AND ts         >= $3
           AND ts          < $4
         ORDER BY ts DESC, ledger DESC
         LIMIT $5
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(),
		from.UTC(), to.UTC(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: TradesInRange: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []canonical.Trade
	for rows.Next() {
		var t canonical.Trade
		var baseAsset, quoteAsset string
		if err := rows.Scan(
			&t.Source, &t.Ledger, &t.TxHash, &t.OpIndex, &t.Timestamp,
			&baseAsset, &quoteAsset,
			&t.BaseAmount, &t.QuoteAmount,
			&t.Maker, &t.Taker,
		); err != nil {
			return nil, fmt.Errorf("timescale: TradesInRange scan: %w", err)
		}
		base, err := canonical.ParseAsset(baseAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: TradesInRange base %q: %w", baseAsset, err)
		}
		quote, err := canonical.ParseAsset(quoteAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: TradesInRange quote %q: %w", quoteAsset, err)
		}
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			return nil, fmt.Errorf("timescale: TradesInRange pair: %w", err)
		}
		t.Pair = pair
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: TradesInRange rows: %w", err)
	}
	// Scanned newest-first (see the DESC query above); reverse to the
	// ascending order this method's contract promises its callers.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// TradesInRangeAfter is TradesInRange with a full-PK cursor. Rows
// are returned iff their (ts, ledger, tx_hash, op_index, source)
// tuple is strictly greater than the corresponding `after*` values.
//
// Widening from (ts, ledger) to the full PK closes a pagination
// edge case: multiple trades can share (ts, ledger), and the naive
// tuple `(ts, ledger) > (X, Y)` would skip any same-(ts, ledger)
// row that didn't happen to be the last one on the previous page.
// The primary key is unique so the full-PK tuple gives total order.
//
// afterTs = zero time disables the cursor; use TradesInRange for
// that case (shorter form).
func (s *Store) TradesInRangeAfter(
	ctx context.Context,
	p canonical.Pair,
	from, to, afterTs time.Time,
	afterLedger uint32,
	afterTxHash, afterSource string,
	afterOpIndex uint32,
	limit int,
) ([]canonical.Trade, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}
	if to.Before(from) {
		return nil, fmt.Errorf("timescale: TradesInRangeAfter: to %v < from %v", to, from)
	}
	// Full-PK tuple comparison. ORDER BY + WHERE must agree on the
	// column order so the comparison is monotonic with the sort.
	// Source sorts last so the common case (single-source trades)
	// doesn't pay an unnecessary string compare cost on the index.
	//
	// NOTE: the Go function signature declares afterSource BEFORE
	// afterOpIndex (by-type grouping of the two strings), but the
	// SQL tuple expects them in PK order (tx_hash, op_index, source).
	// The parameter BINDING below — not the signature — is what
	// matters; it hands values to the placeholders in PK order.
	// If you reorder the signature, reorder the binding too.
	const q = `
        SELECT source, ledger, tx_hash, op_index, ts,
               base_asset, quote_asset,
               base_amount, quote_amount,
               COALESCE(maker, ''), COALESCE(taker, '')
          FROM trades
         WHERE base_asset  = $1
           AND quote_asset = $2
           AND ts         >= $3
           AND ts          < $4
           AND (ts, ledger, tx_hash, op_index, source) > ($5, $6, $7, $8, $9)
         ORDER BY ts ASC, ledger ASC, tx_hash ASC, op_index ASC, source ASC
         LIMIT $10
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(), // $1, $2
		from.UTC(), to.UTC(), // $3, $4
		// $5..$9 — must match the PK tuple order in the SQL above,
		// NOT the function-signature order.
		afterTs.UTC(), afterLedger, afterTxHash, afterOpIndex, afterSource,
		limit, // $10
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: TradesInRangeAfter: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []canonical.Trade
	for rows.Next() {
		var t canonical.Trade
		var baseAsset, quoteAsset string
		if err := rows.Scan(
			&t.Source, &t.Ledger, &t.TxHash, &t.OpIndex, &t.Timestamp,
			&baseAsset, &quoteAsset,
			&t.BaseAmount, &t.QuoteAmount,
			&t.Maker, &t.Taker,
		); err != nil {
			return nil, fmt.Errorf("timescale: TradesInRangeAfter scan: %w", err)
		}
		base, err := canonical.ParseAsset(baseAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: TradesInRangeAfter base %q: %w", baseAsset, err)
		}
		quote, err := canonical.ParseAsset(quoteAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: TradesInRangeAfter quote %q: %w", quoteAsset, err)
		}
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			return nil, fmt.Errorf("timescale: TradesInRangeAfter pair: %w", err)
		}
		t.Pair = pair
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: TradesInRangeAfter rows: %w", err)
	}
	return out, nil
}

// FXQuoteAtOrBefore returns the most recent FX-source observation
// for `pair` whose `ts <= cutoff`, restricted to sources passed in
// `fxSources` (typically the result of external.FXSources()). When
// multiple FX sources have a quote at-or-before cutoff, the one with
// the largest ts wins; ties are broken by source-name DESC ordering
// (deterministic across regions because every region's source registry
// is identical).
//
// Returns (price, observedAt, source, nil) on hit;
// (nil, time.Time{}, "", [ErrNoFXQuote]) when no FX quote exists at
// or before cutoff. Other DB errors propagate.
//
// `price` is the per-trade ratio QuoteAmount/BaseAmount expressed as a
// *big.Rat (no precision loss — both sides come from NUMERIC columns).
// FX-source trades use a uniform 1e8 scale on each side so the ratio
// is dimensionally clean (the scale cancels). Empty `fxSources`
// returns ErrNoFXQuote without touching the DB.
//
// Implementation notes:
//   - The hypertable index `(base_asset, quote_asset, ts DESC)` makes
//     this a constant-cost descending range scan. Pushing the source
//     filter to SQL keeps the scan bounded to FX rows.
//   - cutoff is rounded to UTC to match the InsertTrade convention.
func (s *Store) FXQuoteAtOrBefore(
	ctx context.Context,
	pair canonical.Pair,
	cutoff time.Time,
	fxSources []string,
) (price *big.Rat, observedAt time.Time, source string, err error) {
	if len(fxSources) == 0 {
		return nil, time.Time{}, "", ErrNoFXQuote
	}

	const q = `
        SELECT source, ts, base_amount, quote_amount
          FROM trades
         WHERE base_asset  = $1
           AND quote_asset = $2
           AND ts         <= $3
           AND source      = ANY($4)
         ORDER BY ts DESC, source DESC
         LIMIT 1
    `
	var (
		gotSource         string
		gotTS             time.Time
		baseAmt, quoteAmt string
	)
	row := s.db.QueryRowContext(ctx, q,
		pair.Base.String(), pair.Quote.String(),
		cutoff.UTC(), pq.Array(fxSources),
	)
	if err := row.Scan(&gotSource, &gotTS, &baseAmt, &quoteAmt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, time.Time{}, "", ErrNoFXQuote
		}
		return nil, time.Time{}, "", fmt.Errorf("timescale: FXQuoteAtOrBefore: %w", err)
	}

	baseInt, ok := new(big.Int).SetString(baseAmt, 10)
	if !ok || baseInt.Sign() == 0 {
		return nil, time.Time{}, "", fmt.Errorf("timescale: FXQuoteAtOrBefore: invalid base_amount %q", baseAmt)
	}
	quoteInt, ok := new(big.Int).SetString(quoteAmt, 10)
	if !ok {
		return nil, time.Time{}, "", fmt.Errorf("timescale: FXQuoteAtOrBefore: invalid quote_amount %q", quoteAmt)
	}
	r := new(big.Rat).SetFrac(quoteInt, baseInt)
	return r, gotTS, gotSource, nil
}

// CountTrades returns the total number of rows in the trades table.
// O(hypertable scan) on TimescaleDB; use sparingly (diagnostics + tests).
func (s *Store) CountTrades(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM trades`).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("timescale: CountTrades: %w", err)
	}
	return n, nil
}
