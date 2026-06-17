# chainlink ingest source

Off-chain ingest for **Chainlink Price Feeds** on Ethereum mainnet.
Until Chainlink ships Soroban Data Feeds on Stellar, we read directly
from the EVM aggregator contracts via JSON-RPC and project each round
into a `canonical.OracleUpdate` row.

Sister to (and structurally similar to) the existing pollers under
`internal/sources/external/`. Different from
`internal/divergence/chainlink.go` — that's a synchronous cross-check
helper for the divergence service; **this** is a real ingest source
that writes to `oracle_updates` on its own goroutine.

## Why a real ingest source

Per `internal/canonical/oracle.go`:
> "OracleUpdate is one price observation published by an on-chain or
> off-chain oracle (Reflector, Redstone, Band, Chainlink-HTTP, …)"

Chainlink-HTTP was always intended as a real source; up to this
session it lived only as the divergence cross-check. Promoting it
gives us:

- per-feed history queryable via `/v1/oracle/...` endpoints
- continuous-aggregate (CAGG) coverage on the new `oracle_prices_*`
  ladder (migration 0034)
- a 4th oracle in the oracle-visibility surface
- attribution for the first time someone asks "what did Chainlink
  say about ETH/USD at this timestamp"

## Wire shape

Each Chainlink AggregatorV3 contract exposes
`latestRoundData() returns (uint80 roundId, int256 answer,
uint256 startedAt, uint256 updatedAt, uint80 answeredInRound)`.

We poll on a configurable cadence (default 30s — matches the 30s
freshness ceiling for current-price endpoints) and dedupe by
`(feed_address, roundId)`. Only a roundId we haven't seen before
results in a `canonical.OracleUpdate` row — repeated polls of an
unchanged feed are no-ops.

## Files

```
README.md          — this file
events.go          — source name, default feed inventory, types
client.go          — minimal EVM JSON-RPC client (eth_call, eth_blockNumber, eth_getLogs)
decode.go          — ABI decoders: latestRoundData (5-tuple), int256 → big.Int, AnswerUpdated event
poller.go          — implements external.Poller — live ingest path
backfill.go        — eth_getLogs walk over historical block ranges
poller_test.go     — ABI decode + fixture tests
```

## Phase A scope (this session)

- Live ingest via `latestRoundData` polling, dedupe by roundId
- Backfill via `eth_getLogs` for `AnswerUpdated` events (chunked at
  5k blocks/call to stay under provider response-size caps)
- Default feed set covers the 6 majors already in
  `internal/divergence/chainlink.go` (BTC/ETH/LINK/EUR/GBP/JPY vs USD)
- Operator extends via TOML config: `[external.chainlink].feed_map`

## Phase B follow-ups

1. **Auto-discover all 516 ETH-mainnet feeds.** Today the operator
   curates `feed_map`; v2 walks the official Chainlink registry
   contract (or the published JSON catalogue) and auto-populates.
2. **WebSocket subscription** (`eth_subscribe('logs', ...)`) instead of
   poll. Drops live RPC cost ~500x. Needs WSS endpoint + connection
   management. Most Alchemy plans cap free WSS subscriptions at 5;
   would need paid for >5 feeds simultaneously.
3. **Multi-chain** (Polygon / Arbitrum / Base / Optimism). Same code,
   one client per chain, ~4x RPC budget.
4. **Token API integration** (Alchemy paid feature on the same key).
   ERC-20 metadata + supply queries — could feed
   `internal/supply/` for any non-Stellar token coverage.
5. **Prices API** (also Alchemy). Aggregated cross-venue prices —
   could be a 4th divergence reference alongside CG/CMC/Reflector.

## Operator setup

```toml
[external.chainlink]
enabled        = true
rpc_url        = "https://eth-mainnet.g.alchemy.com/v2/<ALCHEMY_KEY>"
poll_interval  = "30s"

# Per-feed map. Address is the AggregatorV3 proxy on Ethereum mainnet.
# decimals defaults to 8 (Chainlink's standard).
[external.chainlink.feed_map]
  "crypto:BTC/fiat:USD"  = { address = "0xF4030086522a5bEEa4988F8cA5B36dbC97BeE88c" }
  "crypto:ETH/fiat:USD"  = { address = "0x5f4eC3Df9cbd43714FE2740f5E3616155c5b8419" }
  # ... etc
```

The `<ALCHEMY_KEY>` placeholder is operator-supplied; it lives in r1's
`/etc/stellarindex/stellarindex.toml` (chmod 600), never in the repo.
