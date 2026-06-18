package timescale

import (
	"context"
	"fmt"
)

// BespokeBlock is the served-tier, per-category bespoke analytics for a
// protocol page (the v1 API maps it 1:1 to api/v1.ProtocolBespoke — timescale
// can't import v1). A generic container (headline KPIs + named time-series +
// named top-N tables) filled with content tailored to the protocol's category,
// so the UI renders the three shapes generically while the DATA is bespoke.
//
// All numeric values are PRE-FORMATTED strings here: the store owns formatting
// so amounts that exceed 2^53 stay exact (ADR-0003) and percentages/counts read
// the way the page shows them.
type BespokeBlock struct {
	Category string
	KPIs     []BespokeKPI
	Series   []BespokeSeries
	Tables   []BespokeTable
	Notes    []string
}

// BespokeKPI is one headline metric card.
type BespokeKPI struct {
	Label string
	Value string
	Unit  string
	Hint  string
}

// BespokeSeries is a named time-series for a chart.
type BespokeSeries struct {
	Name   string
	Unit   string
	Points []BespokeSeriesPt
}

// BespokeSeriesPt is one (date, value) point; Value is a numeric string.
type BespokeSeriesPt struct {
	Date  string
	Value string
}

// BespokeTable is a named top-N table — column headers + string rows.
type BespokeTable struct {
	Title   string
	Columns []string
	Rows    [][]string
}

// BuildProtocolBespoke assembles the bespoke block for source (the protocol
// name) given its category, over a trailing windowDays. Returns nil (not an
// error) for a category with no bespoke metrics yet, so the page degrades to
// its generic analytics. windowDays bounds every query to the ts-indexed recent
// window so the projected-table scans stay cheap.
func (s *Store) BuildProtocolBespoke(ctx context.Context, source, category string, windowDays int) (*BespokeBlock, error) {
	if windowDays <= 0 {
		windowDays = 90
	}
	switch category {
	case "bridge":
		return s.bespokeBridge(ctx, source, windowDays)
	case "dex", "amm":
		return s.bespokeDEX(ctx, source, windowDays)
	case "lending":
		return s.bespokeLending(ctx, source, windowDays)
	case "yield":
		return s.bespokeYield(ctx, source, windowDays)
	case "oracle":
		return s.bespokeOracle(ctx, source, windowDays)
	}
	// Categories with no bespoke block yet land here.
	return nil, nil
}

// bespokeBridge builds the bridge (CCTP / Rozo) bespoke block from cctp_events:
// total + daily cross-chain transfer volume and a by-destination-domain table.
// Reference implementation for the per-category pattern.
func (s *Store) bespokeBridge(ctx context.Context, source string, windowDays int) (*BespokeBlock, error) {
	// Only cctp_events carries the domain + amount shape today; rozo_events is
	// empty. Keep this scoped to cctp; other bridges omit the block.
	if source != "cctp" {
		return nil, nil
	}
	since := fmt.Sprintf("%d days", windowDays)
	blk := &BespokeBlock{
		Category: "bridge",
		Notes: []string{
			"Volume is the summed CCTP event amount (USDC, 6-decimal units) over the window; deposit_for_burn + mint_and_withdraw legs both count.",
		},
	}

	// KPIs: total volume + transfer count over the window.
	var totalVol, txCount string
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(sum(amount),0)::text, count(*)::text
		FROM cctp_events WHERE ts > now() - $1::interval AND amount IS NOT NULL`, since).
		Scan(&totalVol, &txCount)
	if err != nil {
		return nil, fmt.Errorf("timescale: bespokeBridge KPIs: %w", err)
	}
	blk.KPIs = append(blk.KPIs,
		BespokeKPI{Label: fmt.Sprintf("Transfer volume (%dd)", windowDays), Value: totalVol, Unit: "USDC-units", Hint: "summed event amount in 6-decimal USDC units"},
		BespokeKPI{Label: fmt.Sprintf("Transfers (%dd)", windowDays), Value: txCount},
	)

	// Daily volume series.
	series, err := s.scanDailySeries(ctx, `
		SELECT to_char(date_trunc('day', ts), 'YYYY-MM-DD'), COALESCE(sum(amount),0)::text
		FROM cctp_events WHERE ts > now() - $1::interval AND amount IS NOT NULL
		GROUP BY 1 ORDER BY 1 ASC`, since)
	if err != nil {
		return nil, err
	}
	if len(series) > 0 {
		blk.Series = append(blk.Series, BespokeSeries{Name: "Daily transfer volume", Unit: "USDC-units", Points: series})
	}

	// By counterparty domain.
	tbl, err := s.scanTable(ctx,
		BespokeTable{Title: "By counterparty domain", Columns: []string{"Domain", "Transfers", "Volume (USDC-units)"}},
		`SELECT COALESCE(counterparty_domain::text,'—'), count(*)::text, COALESCE(sum(amount),0)::text
		   FROM cctp_events WHERE ts > now() - $1::interval
		  GROUP BY counterparty_domain ORDER BY count(*) DESC LIMIT 25`, since)
	if err != nil {
		return nil, err
	}
	if len(tbl.Rows) > 0 {
		blk.Tables = append(blk.Tables, tbl)
	}
	return blk, nil
}

// scanDailySeries runs a (date_text, value_text) query and returns the points.
func (s *Store) scanDailySeries(ctx context.Context, query string, args ...any) ([]BespokeSeriesPt, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("timescale: bespoke series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []BespokeSeriesPt
	for rows.Next() {
		var p BespokeSeriesPt
		if err := rows.Scan(&p.Date, &p.Value); err != nil {
			return nil, fmt.Errorf("timescale: bespoke series scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// scanTable runs a query whose columns match base.Columns and fills base.Rows
// (every value scanned as text). The header is taken from base.
func (s *Store) scanTable(ctx context.Context, base BespokeTable, query string, args ...any) (BespokeTable, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return base, fmt.Errorf("timescale: bespoke table %q: %w", base.Title, err)
	}
	defer func() { _ = rows.Close() }()
	n := len(base.Columns)
	for rows.Next() {
		cells := make([]string, n)
		ptrs := make([]any, n)
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return base, fmt.Errorf("timescale: bespoke table %q scan: %w", base.Title, err)
		}
		base.Rows = append(base.Rows, cells)
	}
	return base, rows.Err()
}

// bespokeDEX builds the DEX/AMM bespoke block (soroswap / phoenix / aquarius
// / comet / sdex) from the dex_volume_by_pair_1d continuous aggregate
// (migration 0064): windowed USD volume + trade count + unique pairs KPIs, a
// daily USD-volume series, and a top-pairs-by-volume table. Queries the CAGG,
// never raw `trades` (a direct 90d GROUP BY over the 313M-row hypertable
// measured ~15.7s).
func (s *Store) bespokeDEX(ctx context.Context, source string, windowDays int) (*BespokeBlock, error) {
	since := fmt.Sprintf("%d days", windowDays)
	blk := &BespokeBlock{
		Category: "dex",
		Notes: []string{
			"Volume is summed USD volume (vol) from the dex_volume_by_pair_1d daily continuous aggregate over the window; trades whose quote never resolved to a USD price contribute 0 to volume but still count toward trade and pair totals.",
			"Base turnover is in base-asset base units (per-asset decimals), not USD.",
		},
	}

	// KPIs: window USD volume, trade count, unique pairs.
	var vol, txCount, pairs string
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(sum(vol),0)::text,
		       COALESCE(sum(trades),0)::text,
		       count(DISTINCT (base_asset, quote_asset))::text
		FROM dex_volume_by_pair_1d
		WHERE source = $1 AND bucket > now() - $2::interval`, source, since).
		Scan(&vol, &txCount, &pairs)
	if err != nil {
		return nil, fmt.Errorf("timescale: bespokeDEX KPIs: %w", err)
	}
	if txCount == "0" {
		// No trades in the window — e.g. soroswap-router, whose swaps are
		// ContractCall-derived and never land in `trades`, or a dormant DEX.
		// Omit the block rather than render an all-zero DEX panel.
		return nil, nil
	}
	blk.KPIs = append(blk.KPIs,
		BespokeKPI{Label: fmt.Sprintf("USD volume (%dd)", windowDays), Value: vol, Unit: "USD", Hint: "summed usd_volume of priced trades over the window"},
		BespokeKPI{Label: fmt.Sprintf("Trades (%dd)", windowDays), Value: txCount},
		BespokeKPI{Label: fmt.Sprintf("Active pairs (%dd)", windowDays), Value: pairs, Hint: "distinct base/quote pairs traded in the window"},
	)

	// Daily USD-volume series.
	series, err := s.scanDailySeries(ctx, `
		SELECT to_char(bucket, 'YYYY-MM-DD'), COALESCE(sum(vol),0)::text
		FROM dex_volume_by_pair_1d
		WHERE source = $1 AND bucket > now() - $2::interval
		GROUP BY 1 ORDER BY 1 ASC`, source, since)
	if err != nil {
		return nil, err
	}
	if len(series) > 0 {
		blk.Series = append(blk.Series, BespokeSeries{Name: "Daily USD volume", Unit: "USD", Points: series})
	}

	// Top pairs by USD volume.
	tbl, err := s.scanTable(ctx,
		BespokeTable{Title: "Top pairs by USD volume", Columns: []string{"Base", "Quote", "Trades", "USD volume", "Base turnover"}},
		`SELECT base_asset, quote_asset,
		        COALESCE(sum(trades),0)::text,
		        COALESCE(sum(vol),0)::text,
		        COALESCE(sum(base_vol),0)::text
		   FROM dex_volume_by_pair_1d
		  WHERE source = $1 AND bucket > now() - $2::interval
		  GROUP BY base_asset, quote_asset
		  ORDER BY sum(vol) DESC NULLS LAST, sum(trades) DESC LIMIT 25`, source, since)
	if err != nil {
		return nil, err
	}
	if len(tbl.Rows) > 0 {
		blk.Tables = append(blk.Tables, tbl)
	}
	return blk, nil
}

// bespokeLending builds the Blend lending bespoke block: per-asset net
// supplied (supply + supply_collateral − withdraw − withdraw_collateral) and
// net borrowed (borrow − repay) from blend_positions, plus auction and
// backstop activity. blend_positions amounts are unsigned magnitudes — the
// sign is the event_kind, not the value — so net positions are signed sums of
// the gross magnitudes. Confirmed on r1: event_kind ∈ {supply, withdraw,
// supply_collateral, withdraw_collateral, borrow, repay, flash_loan};
// token_amount + b_or_d_amount are both ≥ 0.
func (s *Store) bespokeLending(ctx context.Context, source string, windowDays int) (*BespokeBlock, error) {
	if source != "blend" {
		return nil, nil
	}
	since := fmt.Sprintf("%d days", windowDays)
	blk := &BespokeBlock{
		Category: "lending",
		Notes: []string{
			"Net supplied / net borrowed are signed running sums of unsigned blend_positions.token_amount over the window — supply/supply_collateral add, withdraw/withdraw_collateral subtract for supplied; borrow adds, repay subtracts for borrowed. They are WINDOW deltas, not all-time TVL (the served tier is retention-scoped); flash_loan is excluded.",
			"Asset is a Soroban token contract id, shown shortened; amounts are in the token's base units (per-asset decimals).",
			"Per-pool 'Util %' is the window borrow/supply ratio — a coarse proxy, not on-chain utilisation (which is current reserve borrowed/supplied). Real current-state TVL + supply/borrow APYs need the Soroban pool-storage reader (reserve b_rate/d_rate + totals from contract storage); this block is event-derived and window-scoped until that ships.",
		},
	}

	if err := s.lendingPositionBlocks(ctx, blk, since, windowDays); err != nil {
		return nil, err
	}
	if err := s.lendingAuctionBlocks(ctx, blk, since, windowDays); err != nil {
		return nil, err
	}
	if err := s.lendingBackstopKPIs(ctx, blk, since, windowDays); err != nil {
		return nil, err
	}
	return blk, nil
}

// lendingPositionBlocks fills the net-supplied / net-borrowed KPIs, the
// per-asset net-position table, and the daily position-event series.
func (s *Store) lendingPositionBlocks(ctx context.Context, blk *BespokeBlock, since string, windowDays int) error {
	var netSupplied, netBorrowed, users, flashLoans string
	err := s.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(sum(CASE
		    WHEN event_kind IN ('supply','supply_collateral')    THEN token_amount
		    WHEN event_kind IN ('withdraw','withdraw_collateral') THEN -token_amount
		    ELSE 0 END),0)::text,
		  COALESCE(sum(CASE
		    WHEN event_kind = 'borrow' THEN token_amount
		    WHEN event_kind = 'repay'  THEN -token_amount
		    ELSE 0 END),0)::text,
		  count(DISTINCT user_address)::text,
		  count(*) FILTER (WHERE event_kind = 'flash_loan')::text
		FROM blend_positions
		WHERE ledger_close_time > now() - $1::interval`, since).
		Scan(&netSupplied, &netBorrowed, &users, &flashLoans)
	if err != nil {
		return fmt.Errorf("timescale: bespokeLending position KPIs: %w", err)
	}
	blk.KPIs = append(blk.KPIs,
		BespokeKPI{Label: fmt.Sprintf("Net supplied (%dd)", windowDays), Value: netSupplied, Unit: "token-units", Hint: "supply+collateral minus withdrawals, summed across assets (base units)"},
		BespokeKPI{Label: fmt.Sprintf("Net borrowed (%dd)", windowDays), Value: netBorrowed, Unit: "token-units", Hint: "borrow minus repay, summed across assets (base units)"},
		BespokeKPI{Label: fmt.Sprintf("Active users (%dd)", windowDays), Value: users},
		BespokeKPI{Label: fmt.Sprintf("Flash loans (%dd)", windowDays), Value: flashLoans},
	)

	tbl, err := s.scanTable(ctx,
		BespokeTable{Title: "Net position by asset", Columns: []string{"Asset", "Net supplied", "Net borrowed", "Events"}},
		`SELECT asset,
		   COALESCE(sum(CASE
		     WHEN event_kind IN ('supply','supply_collateral')    THEN token_amount
		     WHEN event_kind IN ('withdraw','withdraw_collateral') THEN -token_amount
		     ELSE 0 END),0)::text,
		   COALESCE(sum(CASE
		     WHEN event_kind = 'borrow' THEN token_amount
		     WHEN event_kind = 'repay'  THEN -token_amount
		     ELSE 0 END),0)::text,
		   count(*)::text
		 FROM blend_positions
		 WHERE ledger_close_time > now() - $1::interval
		 GROUP BY asset
		 ORDER BY count(*) DESC LIMIT 25`, since)
	if err != nil {
		return err
	}
	if len(tbl.Rows) > 0 {
		blk.Tables = append(blk.Tables, tbl)
	}

	poolTbl, err := s.scanTable(ctx,
		BespokeTable{Title: "Net position by pool", Columns: []string{"Pool", "Net supplied", "Net borrowed", "Util %", "Users", "Events"}},
		`SELECT pool,
		   COALESCE(sum(CASE
		     WHEN event_kind IN ('supply','supply_collateral')    THEN token_amount
		     WHEN event_kind IN ('withdraw','withdraw_collateral') THEN -token_amount
		     ELSE 0 END),0)::text,
		   COALESCE(sum(CASE
		     WHEN event_kind = 'borrow' THEN token_amount
		     WHEN event_kind = 'repay'  THEN -token_amount
		     ELSE 0 END),0)::text,
		   CASE WHEN COALESCE(sum(CASE
		         WHEN event_kind IN ('supply','supply_collateral')    THEN token_amount
		         WHEN event_kind IN ('withdraw','withdraw_collateral') THEN -token_amount
		         ELSE 0 END),0) > 0
		     THEN round(100.0 * COALESCE(sum(CASE
		         WHEN event_kind = 'borrow' THEN token_amount
		         WHEN event_kind = 'repay'  THEN -token_amount
		         ELSE 0 END),0) / sum(CASE
		         WHEN event_kind IN ('supply','supply_collateral')    THEN token_amount
		         WHEN event_kind IN ('withdraw','withdraw_collateral') THEN -token_amount
		         ELSE 0 END), 2)::text
		     ELSE '—' END,
		   count(DISTINCT user_address)::text,
		   count(*)::text
		 FROM blend_positions
		 WHERE ledger_close_time > now() - $1::interval
		 GROUP BY pool
		 ORDER BY count(*) DESC LIMIT 25`, since)
	if err != nil {
		return err
	}
	if len(poolTbl.Rows) > 0 {
		blk.Tables = append(blk.Tables, poolTbl)
	}

	series, err := s.scanDailySeries(ctx, `
		SELECT to_char(date_trunc('day', ledger_close_time), 'YYYY-MM-DD'), count(*)::text
		FROM blend_positions WHERE ledger_close_time > now() - $1::interval
		GROUP BY 1 ORDER BY 1 ASC`, since)
	if err != nil {
		return err
	}
	if len(series) > 0 {
		blk.Series = append(blk.Series, BespokeSeries{Name: "Daily position events", Unit: "events", Points: series})
	}
	return nil
}

// lendingAuctionBlocks fills the auction-count KPIs and the recent-auctions
// table from blend_auctions.
func (s *Store) lendingAuctionBlocks(ctx context.Context, blk *BespokeBlock, since string, windowDays int) error {
	var newAuctions, fills string
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE event_kind = 'new')::text,
		       count(*) FILTER (WHERE event_kind = 'fill')::text
		FROM blend_auctions WHERE ts > now() - $1::interval`, since).
		Scan(&newAuctions, &fills); err != nil {
		return fmt.Errorf("timescale: bespokeLending auction KPIs: %w", err)
	}
	blk.KPIs = append(blk.KPIs,
		BespokeKPI{Label: fmt.Sprintf("Auctions started (%dd)", windowDays), Value: newAuctions, Hint: "blend_auctions new events (type 0 user-liquidation, 1 bad-debt, 2 interest)"},
		BespokeKPI{Label: fmt.Sprintf("Auctions filled (%dd)", windowDays), Value: fills},
	)

	atbl, err := s.scanTable(ctx,
		BespokeTable{Title: "Recent auctions", Columns: []string{"When", "Type", "Kind", "User", "Fill %"}},
		`SELECT to_char(ts, 'YYYY-MM-DD HH24:MI'),
		        CASE auction_type WHEN 0 THEN 'user-liquidation' WHEN 1 THEN 'bad-debt' WHEN 2 THEN 'interest' ELSE auction_type::text END,
		        event_kind,
		        user_address,
		        COALESCE(fill_percent::text,'—')
		   FROM blend_auctions WHERE ts > now() - $1::interval
		  ORDER BY ts DESC LIMIT 25`, since)
	if err != nil {
		return err
	}
	if len(atbl.Rows) > 0 {
		blk.Tables = append(blk.Tables, atbl)
	}
	return nil
}

// lendingBackstopKPIs fills the backstop deposit/withdraw volume KPIs (the
// table is sparse — degrades to 0).
func (s *Store) lendingBackstopKPIs(ctx context.Context, blk *BespokeBlock, since string, windowDays int) error {
	var backstopVol, backstopCount string
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(sum(amount),0)::text, count(*)::text
		FROM blend_backstop_events WHERE ledger_close_time > now() - $1::interval`, since).
		Scan(&backstopVol, &backstopCount); err != nil {
		return fmt.Errorf("timescale: bespokeLending backstop KPIs: %w", err)
	}
	blk.KPIs = append(blk.KPIs,
		BespokeKPI{Label: fmt.Sprintf("Backstop volume (%dd)", windowDays), Value: backstopVol, Unit: "token-units", Hint: "summed blend_backstop_events amount (base units)"},
		BespokeKPI{Label: fmt.Sprintf("Backstop events (%dd)", windowDays), Value: backstopCount},
	)
	return nil
}

// bespokeYield builds the DeFindex vault bespoke block from defindex_flows:
// windowed gross deposit / withdraw flow volume (by direction), per-vault net
// flow (deposit − withdraw, an AUM proxy), and unique actors. Confirmed on r1:
// direction ∈ {deposit, withdraw}; layer ∈ {strategy, vault}. The series and
// per-vault net scope to the vault layer to avoid double-counting a deposit
// that fans out into strategies.
func (s *Store) bespokeYield(ctx context.Context, source string, windowDays int) (*BespokeBlock, error) {
	if source != "defindex" {
		return nil, nil
	}
	since := fmt.Sprintf("%d days", windowDays)
	blk := &BespokeBlock{
		Category: "yield",
		Notes: []string{
			"Flow volume is gross summed defindex_flows.amount by direction over the window from the STRATEGY layer — the vault layer records who/when but carries the amount as a per-strategy vector (amounts_vec), so its scalar amount is NULL; the strategy-layer scalars are the actual capital deployed.",
			"Net flow (deposit − withdraw) is a window AUM proxy, not all-time TVL (the served tier is retention-scoped). Contract is a Soroban strategy contract id; amounts are summed token base units (per-asset decimals) across strategies.",
		},
	}

	var depositVol, withdrawVol, actors string
	err := s.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(sum(amount) FILTER (WHERE direction = 'deposit'  AND layer = 'strategy'),0)::text,
		  COALESCE(sum(amount) FILTER (WHERE direction = 'withdraw' AND layer = 'strategy'),0)::text,
		  count(DISTINCT actor)::text
		FROM defindex_flows WHERE ledger_close_time > now() - $1::interval`, since).
		Scan(&depositVol, &withdrawVol, &actors)
	if err != nil {
		return nil, fmt.Errorf("timescale: bespokeYield KPIs: %w", err)
	}
	blk.KPIs = append(blk.KPIs,
		BespokeKPI{Label: fmt.Sprintf("Deposit volume (%dd)", windowDays), Value: depositVol, Unit: "token-units", Hint: "gross strategy-layer deposit amount (base units)"},
		BespokeKPI{Label: fmt.Sprintf("Withdraw volume (%dd)", windowDays), Value: withdrawVol, Unit: "token-units", Hint: "gross strategy-layer withdraw amount (base units)"},
		BespokeKPI{Label: fmt.Sprintf("Unique actors (%dd)", windowDays), Value: actors},
	)

	series, err := s.scanDailySeries(ctx, `
		SELECT to_char(date_trunc('day', ledger_close_time), 'YYYY-MM-DD'), COALESCE(sum(amount),0)::text
		FROM defindex_flows
		WHERE ledger_close_time > now() - $1::interval AND layer = 'strategy'
		GROUP BY 1 ORDER BY 1 ASC`, since)
	if err != nil {
		return nil, err
	}
	if len(series) > 0 {
		blk.Series = append(blk.Series, BespokeSeries{Name: "Daily strategy flow volume", Unit: "token-units", Points: series})
	}

	tbl, err := s.scanTable(ctx,
		BespokeTable{Title: "Net flow by strategy", Columns: []string{"Strategy", "Deposits", "Withdrawals", "Net flow", "Flows"}},
		`SELECT contract_id,
		   COALESCE(sum(amount) FILTER (WHERE direction = 'deposit'  AND layer = 'strategy'),0)::text,
		   COALESCE(sum(amount) FILTER (WHERE direction = 'withdraw' AND layer = 'strategy'),0)::text,
		   COALESCE(sum(CASE WHEN layer = 'strategy' AND direction = 'deposit'  THEN amount
		                     WHEN layer = 'strategy' AND direction = 'withdraw' THEN -amount
		                     ELSE 0 END),0)::text,
		   count(*) FILTER (WHERE layer = 'strategy')::text
		 FROM defindex_flows
		 WHERE ledger_close_time > now() - $1::interval AND layer = 'strategy'
		 GROUP BY contract_id
		 ORDER BY count(*) DESC LIMIT 25`, since)
	if err != nil {
		return nil, err
	}
	if len(tbl.Rows) > 0 {
		blk.Tables = append(blk.Tables, tbl)
	}
	return blk, nil
}

// bespokeOracle builds the oracle bespoke block (reflector-dex/cex/fx, band,
// redstone) from oracle_updates scoped by source: feed count (distinct
// asset/quote), update cadence (updates/day), and a latest-prices table.
// price is a NUMERIC at the row's `decimals` scale — shown raw with the scale
// column, not rescaled (decimals can vary per feed).
func (s *Store) bespokeOracle(ctx context.Context, source string, windowDays int) (*BespokeBlock, error) {
	since := fmt.Sprintf("%d days", windowDays)
	blk := &BespokeBlock{
		Category: "oracle",
		Notes: []string{
			"Scoped to oracle_updates.source = the protocol's feed contract. price is the raw on-chain integer at the row's `decimals` scale (not rescaled — decimals can differ per feed). asset/quote are the feed pair; asset is a token contract id shown shortened, quote is often `native`.",
		},
	}

	var updates, feeds, perDay string
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*)::text,
		       count(DISTINCT (asset, quote))::text,
		       CASE WHEN $2 > 0 THEN round(count(*)::numeric / $2, 1)::text ELSE '0' END
		FROM oracle_updates
		WHERE source = $1 AND ts > now() - $3::interval`, source, windowDays, since).
		Scan(&updates, &feeds, &perDay)
	if err != nil {
		return nil, fmt.Errorf("timescale: bespokeOracle KPIs: %w", err)
	}
	if updates == "0" {
		return nil, nil // no updates for this feed in the window — omit, don't show zeros
	}
	blk.KPIs = append(blk.KPIs,
		BespokeKPI{Label: fmt.Sprintf("Updates (%dd)", windowDays), Value: updates},
		BespokeKPI{Label: "Distinct feeds", Value: feeds, Hint: "distinct asset/quote pairs seen in the window"},
		BespokeKPI{Label: "Updates / day", Value: perDay, Hint: "mean update cadence over the window"},
	)

	series, err := s.scanDailySeries(ctx, `
		SELECT to_char(date_trunc('day', ts), 'YYYY-MM-DD'), count(*)::text
		FROM oracle_updates WHERE source = $1 AND ts > now() - $2::interval
		GROUP BY 1 ORDER BY 1 ASC`, source, since)
	if err != nil {
		return nil, err
	}
	if len(series) > 0 {
		blk.Series = append(blk.Series, BespokeSeries{Name: "Daily updates", Unit: "updates", Points: series})
	}

	tbl, err := s.scanTable(ctx,
		BespokeTable{Title: "Latest feed prices", Columns: []string{"Asset", "Quote", "Price (raw)", "Decimals", "Updated"}},
		`SELECT asset,
		        quote,
		        COALESCE(price::text,'—'),
		        COALESCE(decimals::text,'—'),
		        to_char(ts, 'YYYY-MM-DD HH24:MI')
		   FROM (
		     SELECT DISTINCT ON (asset, quote) asset, quote, price, decimals, ts
		       FROM oracle_updates
		      WHERE source = $1 AND ts > now() - $2::interval
		      ORDER BY asset, quote, ts DESC
		   ) latest
		  ORDER BY ts DESC LIMIT 50`, source, since)
	if err != nil {
		return nil, err
	}
	if len(tbl.Rows) > 0 {
		blk.Tables = append(blk.Tables, tbl)
	}
	return blk, nil
}
