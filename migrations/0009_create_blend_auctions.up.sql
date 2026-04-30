-- 0009 up — `blend_auctions` hypertable.
--
-- Stores one row per observed Blend auction event (new_auction,
-- fill_auction, delete_auction) across every Blend pool deployed
-- on mainnet. Per docs/discovery/dexes-amms/blend.md, auctions are
-- the primary directional / state-change signal we extract from
-- Blend's lending protocol — they're not spot trades, so they
-- live in their own table rather than the trades hypertable.
--
-- Identity is per-event: a single auction lifecycle produces
-- multiple rows (announcement -> partial fills -> [optional
-- deletion]). Aggregation by (pool, auction_type, user_address)
-- groups all events of one auction; ordering by ledger / ts gives
-- the lifecycle.
--
-- AuctionData (bid / lot) is stored as JSONB rather than separate
-- per-asset rows because:
--   * read-side queries are typically "show me this auction's
--     full state at a point in time", not "every auction touching
--     asset X".
--   * the array shape is small (2-4 assets typical) so JSONB is
--     fine for storage + index doesn't need to be deep.
-- Asset-centric queries can use the GIN index on bid+lot below.
--
-- Retention: NONE. Auctions are low-volume (a handful per day
-- protocol-wide) and high-value (each is a stress-price reference
-- point + audit signal). Indefinite retention is the default; if
-- the table grows beyond a useful size in years, the policy lands
-- in a follow-up migration.

BEGIN;

CREATE TABLE blend_auctions (
    -- Pool contract address. The Blend pool factory deploys per-asset-
    -- group pools; the dispatcher matches pool events by topic, then
    -- stamps the contract id here.
    pool             text         NOT NULL,

    -- Auction kind: 0 = UserLiquidation, 1 = BadDebt, 2 = Interest.
    -- Verified against pool/src/auctions/auction.rs constants.
    auction_type     smallint     NOT NULL CHECK (auction_type BETWEEN 0 AND 2),

    -- The address whose position is being auctioned. G-strkey for
    -- account-controlled positions, C-strkey for contract-owned
    -- positions (e.g. backstop module).
    user_address     text         NOT NULL,

    -- Per-event identity (Soroban ledger coordinates).
    ledger           integer      NOT NULL CHECK (ledger >= 0),
    tx_hash          char(64)     NOT NULL,
    op_index         integer      NOT NULL CHECK (op_index >= 0),
    ts               timestamptz  NOT NULL,

    -- One of 'new', 'fill', 'delete'. Distinguishes the three
    -- event variants emitted across an auction's lifecycle.
    event_kind       text         NOT NULL CHECK (event_kind IN ('new', 'fill', 'delete')),

    -- new_auction body: percent of the position being auctioned
    -- (0-100 in basis-style u32). NULL for fill / delete.
    percent          integer      CHECK (percent IS NULL OR (percent >= 0 AND percent <= 100)),

    -- fill_auction body: filler address (G or C) + fill_percent
    -- (i128; stored NUMERIC for full-precision parity with the
    -- contract's u128-shaped fraction). NULL for new / delete.
    filler           text,
    fill_percent     numeric,

    -- AuctionData fields.
    -- block is the contract-side auction-start block (used by
    -- the contract to scale auction prices). Stored for analytics;
    -- NULL for delete (which carries no body).
    block            integer      CHECK (block IS NULL OR block >= 0),

    -- bid and lot are arrays of {asset, amount} pairs. Each amount
    -- is the i128 quantity stored as a string for full-precision
    -- preservation — JSONB handles strings natively, NUMERIC
    -- inside JSONB does not.
    --
    -- Shape: jsonb '[{"asset":"C...","amount":"123"}, ...]'
    -- NULL for delete (no body).
    bid              jsonb,
    lot              jsonb,

    ingested_at      timestamptz  NOT NULL DEFAULT now(),

    -- A single Soroban event is uniquely identified by
    -- (ledger, tx_hash, op_index) but Timescale hypertables
    -- require the partitioning column in the PK, so ts is
    -- included. Per docs/audit-2026-04-29/05-findings-register.md
    -- F-0010, the trade table's identity has the same shape; we
    -- intentionally match for consistency and will revisit if
    -- F-0010 lands a canonical fix.
    PRIMARY KEY (ledger, tx_hash, op_index, ts)
);

COMMENT ON TABLE blend_auctions IS
    'One row per Blend auction event (new / fill / delete). '
    'Hypertable partitioned on ts. See ADR-0006 + docs/discovery/dexes-amms/blend.md.';

COMMENT ON COLUMN blend_auctions.auction_type IS
    '0=UserLiquidation, 1=BadDebt, 2=Interest. Verified against '
    'blend-contracts-v2 pool/src/auctions/auction.rs.';
COMMENT ON COLUMN blend_auctions.event_kind IS
    'Lifecycle event variant: new (announcement), fill (partial / '
    'full clearance), delete (admin removal).';
COMMENT ON COLUMN blend_auctions.bid IS
    'JSONB array of {asset, amount} — assets the filler spends. '
    'amount is the i128 stored as string for full-precision parity.';
COMMENT ON COLUMN blend_auctions.lot IS
    'JSONB array of {asset, amount} — assets the filler receives.';

-- Hypertable. 1-day chunks consistent with trades / oracle_updates.
SELECT create_hypertable(
    'blend_auctions',
    'ts',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists       => TRUE
);

-- Secondary indexes.

-- Most-common query: "show me every event for this auction"
-- (UI-side: "what happened to user G... in this pool's bad-debt
-- auction"). Group all of an auction's lifecycle rows.
CREATE INDEX blend_auctions_pool_user_type_idx
    ON blend_auctions (pool, user_address, auction_type, ts DESC);

-- Pool-centric stream ("recent activity in this Blend pool").
CREATE INDEX blend_auctions_pool_ts_idx
    ON blend_auctions (pool, ts DESC);

-- Source-centric replay / debug — let ops walk events by ledger.
CREATE INDEX blend_auctions_ledger_idx
    ON blend_auctions (ledger DESC);

-- GIN for asset-centric queries on bid / lot ("did this asset
-- ever get liquidated against?"). The index covers both columns
-- via two separate single-column indexes; combined queries can
-- use either independently.
CREATE INDEX blend_auctions_bid_gin   ON blend_auctions USING gin (bid jsonb_path_ops);
CREATE INDEX blend_auctions_lot_gin   ON blend_auctions USING gin (lot jsonb_path_ops);

-- Compression — group by pool + auction_type for dictionary reuse;
-- many of the per-auction events from one pool will compress well
-- together. Within a chunk, order by time DESC.
ALTER TABLE blend_auctions SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'pool, auction_type',
    timescaledb.compress_orderby   = 'ts DESC, ledger DESC'
);

-- Compress chunks older than 7 days. No retention policy: auctions
-- are kept indefinitely (low volume + high audit value).
SELECT add_compression_policy('blend_auctions', INTERVAL '7 days');

COMMIT;
