package timescale

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/sources/blend"
)

// blendAssetAmountRow is the JSONB element shape for the bid /
// lot columns. Amount is a stringified *big.Int to preserve
// full i128 precision through the JSON boundary.
type blendAssetAmountRow struct {
	Asset  string `json:"asset"`
	Amount string `json:"amount"`
}

// encodeBlendAssetAmounts converts a slice of decoded asset/amount
// pairs to a JSONB-ready []byte. Returns nil (which becomes SQL
// NULL on insert) for empty input — the storage column is
// nullable to handle delete-event rows.
func encodeBlendAssetAmounts(in []blend.AssetAmount) ([]byte, error) {
	if len(in) == 0 {
		return nil, nil
	}
	rows := make([]blendAssetAmountRow, len(in))
	for i, a := range in {
		rows[i] = blendAssetAmountRow{
			Asset:  a.Asset.String(),
			Amount: a.Amount.String(),
		}
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return b, nil
}

// decodeBlendAssetAmounts is the inverse — read the JSONB string
// from Postgres back into the read-side projection. Each entry's
// amount stays as a decimal string (the caller can parse with
// big.Int's SetString if numeric ops are needed).
func decodeBlendAssetAmounts(jsonStr string) ([]BlendAssetAmount, error) {
	if jsonStr == "" {
		return nil, nil
	}
	var rows []blendAssetAmountRow
	if err := json.Unmarshal([]byte(jsonStr), &rows); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	out := make([]BlendAssetAmount, len(rows))
	for i, r := range rows {
		out[i] = BlendAssetAmount(r)
	}
	return out, nil
}

// InsertBlendNewAuction writes a `new_auction` event row.
// Idempotent on (ledger, tx_hash, op_index, ts) — re-running over
// the same range is a no-op rather than producing duplicates.
//
// Per docs/discovery/dexes-amms/blend.md the auction lifecycle
// produces multiple rows in this table:
//
//	new_auction      → event_kind='new'
//	fill_auction(s)  → event_kind='fill'
//	delete_auction   → event_kind='delete' (rare; admin removal)
//
// Read-side queries group these by (pool, auction_type,
// user_address) ordered by ledger / ts to reconstruct a single
// auction's lifecycle.
func (s *Store) InsertBlendNewAuction(ctx context.Context, e blend.NewAuctionEvent) error {
	bid, err := encodeBlendAssetAmounts(e.Data.Bid)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendNewAuction: bid: %w", err)
	}
	lot, err := encodeBlendAssetAmounts(e.Data.Lot)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendNewAuction: lot: %w", err)
	}
	const q = `
        INSERT INTO blend_auctions (
            pool, auction_type, user_address,
            ledger, tx_hash, op_index, ts,
            event_kind, percent,
            block, bid, lot
        ) VALUES (
            $1, $2, $3,
            $4, $5, $6, $7,
            'new', $8,
            $9, $10, $11
        )
        ON CONFLICT (ledger, tx_hash, op_index, ts) DO NOTHING
    `
	_, err = s.db.ExecContext(ctx, q,
		e.Pool, int(e.AuctionType), e.User,
		int(e.Ledger), e.TxHash, int(e.OpIndex), e.Timestamp.UTC(),
		int(e.Percent),
		int(e.Data.Block), bid, lot,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendNewAuction: %w", err)
	}
	return nil
}

// InsertBlendFillAuction writes a `fill_auction` event row.
// Multiple fill events may share the same (pool, auction_type,
// user_address) — they're distinguished by (ledger, tx_hash,
// op_index).
func (s *Store) InsertBlendFillAuction(ctx context.Context, e blend.FillAuctionEvent) error {
	bid, err := encodeBlendAssetAmounts(e.Data.Bid)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendFillAuction: bid: %w", err)
	}
	lot, err := encodeBlendAssetAmounts(e.Data.Lot)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendFillAuction: lot: %w", err)
	}
	fillPct := e.FillPercent.String() // i128 → numeric column accepts text
	const q = `
        INSERT INTO blend_auctions (
            pool, auction_type, user_address,
            ledger, tx_hash, op_index, ts,
            event_kind,
            filler, fill_percent,
            block, bid, lot
        ) VALUES (
            $1, $2, $3,
            $4, $5, $6, $7,
            'fill',
            $8, $9::numeric,
            $10, $11, $12
        )
        ON CONFLICT (ledger, tx_hash, op_index, ts) DO NOTHING
    `
	_, err = s.db.ExecContext(ctx, q,
		e.Pool, int(e.AuctionType), e.User,
		int(e.Ledger), e.TxHash, int(e.OpIndex), e.Timestamp.UTC(),
		e.Filler, fillPct,
		int(e.Data.Block), bid, lot,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendFillAuction: %w", err)
	}
	return nil
}

// InsertBlendDeleteAuction writes a `delete_auction` row. No body
// fields — body is the unit value () on the wire.
func (s *Store) InsertBlendDeleteAuction(ctx context.Context, e blend.DeleteAuctionEvent) error {
	const q = `
        INSERT INTO blend_auctions (
            pool, auction_type, user_address,
            ledger, tx_hash, op_index, ts,
            event_kind
        ) VALUES (
            $1, $2, $3,
            $4, $5, $6, $7,
            'delete'
        )
        ON CONFLICT (ledger, tx_hash, op_index, ts) DO NOTHING
    `
	_, err := s.db.ExecContext(ctx, q,
		e.Pool, int(e.AuctionType), e.User,
		int(e.Ledger), e.TxHash, int(e.OpIndex), e.Timestamp.UTC(),
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendDeleteAuction: %w", err)
	}
	return nil
}

// LatestBlendAuctionEvent returns the most recent event row for a
// given (pool, auction_type, user_address). Returns
// (nil, ErrNotFound) on no match.
//
// Used by the auction-state reconstruction read path: the most
// recent event tells the caller whether the auction is currently
// announced, partially-filled, or filled / deleted.
func (s *Store) LatestBlendAuctionEvent(
	ctx context.Context,
	pool string,
	auctionType uint32,
	user string,
) (*BlendAuctionRow, error) {
	const q = `
        SELECT pool, auction_type, user_address,
               ledger, tx_hash, op_index, ts,
               event_kind,
               percent,
               filler, fill_percent,
               block, bid, lot
          FROM blend_auctions
         WHERE pool         = $1
           AND auction_type = $2
           AND user_address = $3
         ORDER BY ledger DESC, op_index DESC
         LIMIT 1
    `
	row := s.db.QueryRowContext(ctx, q, pool, int(auctionType), user)
	var (
		out                                 BlendAuctionRow
		auctionTypeRaw                      int
		percent, block                      sql.NullInt64
		filler, fillPercent, bidStr, lotStr sql.NullString
	)
	err := row.Scan(
		&out.Pool, &auctionTypeRaw, &out.User,
		&out.Ledger, &out.TxHash, &out.OpIndex, &out.Timestamp,
		&out.EventKind,
		&percent,
		&filler, &fillPercent,
		&block, &bidStr, &lotStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("timescale: LatestBlendAuctionEvent: %w", err)
	}
	out.AuctionType = uint32(auctionTypeRaw)
	if percent.Valid {
		v := uint32(percent.Int64)
		out.Percent = &v
	}
	if filler.Valid {
		v := filler.String
		out.Filler = &v
	}
	if fillPercent.Valid {
		v := fillPercent.String
		out.FillPercent = &v
	}
	if block.Valid {
		v := uint32(block.Int64)
		out.Block = &v
	}
	if bidStr.Valid {
		out.Bid, err = decodeBlendAssetAmounts(bidStr.String)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestBlendAuctionEvent: bid: %w", err)
		}
	}
	if lotStr.Valid {
		out.Lot, err = decodeBlendAssetAmounts(lotStr.String)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestBlendAuctionEvent: lot: %w", err)
		}
	}
	return &out, nil
}

// BlendAuctionRow is a flattened read-side projection of one
// blend_auctions row. Nullable fields are pointers so callers can
// distinguish absent from zero (e.g. a delete event has no
// percent / filler / block / bid / lot).
type BlendAuctionRow struct {
	Pool        string
	AuctionType uint32
	User        string
	Ledger      uint32
	TxHash      string
	OpIndex     uint32
	Timestamp   time.Time
	EventKind   string

	Percent     *uint32 // new only
	Filler      *string // fill only
	FillPercent *string // fill only — i128 as decimal string
	Block       *uint32 // new + fill (from AuctionData)

	// Bid / Lot — JSONB-decoded asset/amount pairs. Nil for delete.
	Bid []BlendAssetAmount
	Lot []BlendAssetAmount
}

// BlendAssetAmount is the read-side projection of one entry in
// the bid / lot arrays. Amount is a decimal string (i128
// preserving full precision).
type BlendAssetAmount struct {
	Asset  string
	Amount string
}

// BlendPoolSummary is one row of the /v1/lending/pools listing.
// Identifies a Blend pool contract observed in our auction
// stream (any pool that has ever emitted a new/fill/delete
// event into blend_auctions). Per-pool TVL / utilisation /
// supply-borrow APYs land once the pool-storage reader worker
// ships — surfaced via additional fields then.
type BlendPoolSummary struct {
	Pool           string
	Auctions24h    int64
	AuctionsTotal  int64
	UniqueUsers30d int64
	LastSeen       time.Time
}

// ListBlendPools returns one row per distinct pool contract
// observed in blend_auctions, with auction counts and last-seen
// timestamps. Sorted by AuctionsTotal desc so high-activity
// pools surface first; ties broken by pool address for
// deterministic output.
//
// 24h / 30d windows match the rest of the activity surfaces
// (24h on /assets / /sources, 30d for the unique-user roll-up
// since liquidations cluster).
func (s *Store) ListBlendPools(ctx context.Context) ([]BlendPoolSummary, error) {
	const q = `
        SELECT pool,
               COUNT(*) FILTER (WHERE ts > NOW() - INTERVAL '24 hours') AS auctions_24h,
               COUNT(*)                                                  AS auctions_total,
               COUNT(DISTINCT user_address)
                 FILTER (WHERE ts > NOW() - INTERVAL '30 days')          AS unique_users_30d,
               MAX(ts)                                                   AS last_seen
          FROM blend_auctions
         GROUP BY pool
         ORDER BY auctions_total DESC, pool ASC
    `
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("timescale: ListBlendPools: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []BlendPoolSummary
	for rows.Next() {
		var p BlendPoolSummary
		if err := rows.Scan(&p.Pool, &p.Auctions24h, &p.AuctionsTotal, &p.UniqueUsers30d, &p.LastSeen); err != nil {
			return nil, fmt.Errorf("timescale: ListBlendPools scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: ListBlendPools rows: %w", err)
	}
	return out, nil
}
