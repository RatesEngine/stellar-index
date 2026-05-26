-- 0044 up — Phoenix liquidity + stake event hypertables (#27).
--
-- Phoenix's pool contract (both volatile `contracts/pool/` and
-- stableswap `contracts/pool_stable/`) emits provide_liquidity (5
-- events) and withdraw_liquidity (4 events) using the same
-- N-events-per-action wire shape as `swap` (8 events). The
-- per-pool stake contract emits bond / unbond (3 events each) for
-- LP-share staking. None of this was previously decoded — events
-- silently dropped at the per-field topic match. Task #27 ships
-- the decoders + the storage these tables back.
--
-- Two tables, same scoping decision as cctp_events (0038) and
-- rozo_events (0039) — per-protocol, not a shared "liquidity_events"
-- bucket — because the field sets diverge (provide has TokenA/B +
-- two amounts; withdraw has shares + two return amounts; stake has
-- a user + LP-token + amount). A jsonb attributes blob would just
-- hide what's actually there.
--
-- Identity per Phoenix's swap-already-shipped pattern:
-- (pool, ledger, tx_hash, op_index). One pool operation emits all
-- per-field events with the same (ledger, tx, op) triple, so the
-- triple uniquely identifies the reassembled record. action is
-- pulled into the PK so a hypothetical pool that emits a provide
-- AND a withdraw in the same op (auto-rebalance flow) doesn't
-- collide. ledger_close_time drags into both PKs because
-- TimescaleDB requires the partition column in every unique index
-- on a hypertable (TS103, see 0041's lesson).
--
-- Retention: NONE. Granular-coverage mission keeps LP history; the
-- aggregator and exposure-pipeline both consume this.
--
-- Compression after 7 days, segment-by pool (liquidity) /
-- stake_contract (stake) — matches the dominant query shape
-- "every LP event for this pool over a time range".

BEGIN;

-- ─── phoenix_liquidity ──────────────────────────────────────────
--
-- Per-event provide_liquidity / withdraw_liquidity rows.
--
-- provide_liquidity rows: sender + (token_a, amount_a, token_b,
--                         amount_b). shares_amount stays NULL on
--                         these rows — pool contract does not emit
--                         the LP-shares-minted total in any of the
--                         5 events (the share-token mint shows up
--                         as a SEP-41 mint event on the pool's
--                         share-token contract, captured by the
--                         sep41_supply observer separately).
--
-- withdraw_liquidity rows: sender + shares_amount + (amount_a,
--                          amount_b). token_a / token_b stay NULL —
--                          the withdraw event carries only the
--                          per-token return amounts, not the token
--                          addresses. Downstream resolves the pair
--                          by joining pool against the most recent
--                          provide_liquidity row for the same pool.
CREATE TABLE phoenix_liquidity (
    pool                text         NOT NULL,
    ledger              integer      NOT NULL CHECK (ledger >= 0),
    ledger_close_time   timestamptz  NOT NULL,
    tx_hash             text         NOT NULL,
    op_index            smallint     NOT NULL CHECK (op_index >= 0),

    action              text         NOT NULL CHECK (action IN (
        'provide_liquidity', 'withdraw_liquidity')),

    sender              text         NOT NULL,

    -- Asset addresses present on provide_liquidity rows; NULL on
    -- withdraw rows (the contract doesn't emit them on withdraw).
    token_a             text,
    token_b             text,

    -- Per-token amounts present on both event types. On provide
    -- these are `actual_received_a` / `actual_received_b` (after
    -- slippage truncation); on withdraw they are `return_amount_a`
    -- / `return_amount_b`. NUMERIC per ADR-0003 (i128 never
    -- truncates to int64).
    amount_a            numeric      NOT NULL CHECK (amount_a >= 0),
    amount_b            numeric      NOT NULL CHECK (amount_b >= 0),

    -- LP-share-token amount burned. Present only on withdraw rows;
    -- NULL on provide rows (the contract doesn't emit it).
    shares_amount       numeric      CHECK (shares_amount IS NULL OR shares_amount >= 0),

    ingested_at         timestamptz  NOT NULL DEFAULT now(),

    -- PK includes ledger_close_time per TimescaleDB TS103.
    PRIMARY KEY (ledger_close_time, pool, ledger, tx_hash, op_index, action)
);

COMMENT ON TABLE phoenix_liquidity IS
    'Per-event Phoenix pool provide_liquidity / withdraw_liquidity '
    'reassembled rows. Hypertable on ledger_close_time. See #27 '
    'and internal/sources/phoenix/decode.go.';
COMMENT ON COLUMN phoenix_liquidity.token_a IS
    'Provide-only — withdraw events do not carry per-token addresses.';
COMMENT ON COLUMN phoenix_liquidity.shares_amount IS
    'Withdraw-only — provide events do not surface LP-shares minted; '
    'see the pool''s share-token SEP-41 mint event instead.';

SELECT create_hypertable(
    'phoenix_liquidity',
    'ledger_close_time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists       => TRUE
);

-- Per-pool walk (the dominant query shape).
CREATE INDEX phoenix_liquidity_pool_ts_idx
    ON phoenix_liquidity (pool, ledger_close_time DESC);

-- Per-sender walk ("show me an LP's positions across pools").
CREATE INDEX phoenix_liquidity_sender_ts_idx
    ON phoenix_liquidity (sender, ledger_close_time DESC);

-- Cross-pool per-action scan ("recent withdraws across Phoenix").
CREATE INDEX phoenix_liquidity_action_ts_idx
    ON phoenix_liquidity (action, ledger_close_time DESC);

ALTER TABLE phoenix_liquidity SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'pool, action',
    timescaledb.compress_orderby   = 'ledger_close_time DESC'
);

SELECT add_compression_policy(
    'phoenix_liquidity',
    INTERVAL '7 days',
    if_not_exists => TRUE
);

-- ─── phoenix_stake_events ──────────────────────────────────────
--
-- Per-event bond / unbond rows from per-pool stake contracts.
-- One stake contract per pool; each accepts only the pool's
-- LP-share token as the staked asset. `stake_contract` is the
-- event emitter C-strkey, `lp_token` is the share-token contract
-- being bonded / unbonded, `amount` is the share-token amount
-- (always positive — the `action` discriminator carries the
-- direction).
CREATE TABLE phoenix_stake_events (
    stake_contract      text         NOT NULL,
    ledger              integer      NOT NULL CHECK (ledger >= 0),
    ledger_close_time   timestamptz  NOT NULL,
    tx_hash             text         NOT NULL,
    op_index            smallint     NOT NULL CHECK (op_index >= 0),

    action              text         NOT NULL CHECK (action IN ('bond', 'unbond')),

    user_addr           text         NOT NULL,
    lp_token            text         NOT NULL,
    amount              numeric      NOT NULL CHECK (amount >= 0),

    ingested_at         timestamptz  NOT NULL DEFAULT now(),

    -- PK includes ledger_close_time per TimescaleDB TS103.
    PRIMARY KEY (ledger_close_time, stake_contract, ledger, tx_hash, op_index, action)
);

COMMENT ON TABLE phoenix_stake_events IS
    'Per-event Phoenix stake contract bond / unbond reassembled rows. '
    'Hypertable on ledger_close_time. See #27 and '
    'internal/sources/phoenix/decode.go.';

SELECT create_hypertable(
    'phoenix_stake_events',
    'ledger_close_time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists       => TRUE
);

-- Per-stake-contract walk ("history of one pool's stakers").
CREATE INDEX phoenix_stake_events_contract_ts_idx
    ON phoenix_stake_events (stake_contract, ledger_close_time DESC);

-- Per-user walk ("show me a user's stake positions across pools").
CREATE INDEX phoenix_stake_events_user_ts_idx
    ON phoenix_stake_events (user_addr, ledger_close_time DESC);

-- Cross-contract per-action scan ("recent unbonds across Phoenix").
CREATE INDEX phoenix_stake_events_action_ts_idx
    ON phoenix_stake_events (action, ledger_close_time DESC);

ALTER TABLE phoenix_stake_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'stake_contract, action',
    timescaledb.compress_orderby   = 'ledger_close_time DESC'
);

SELECT add_compression_policy(
    'phoenix_stake_events',
    INTERVAL '7 days',
    if_not_exists => TRUE
);

COMMIT;
