// Representative pair fixtures used by every scenario.
//
// Mix is intentional: XLM majors dominate (matches wallet-shaped
// traffic), with stablecoin majors and a couple of long-tail
// assets to exercise non-cached paths.
//
// CRITICAL: every `asset` and `quote` here MUST be a form that
// canonical.ParseAsset (internal/canonical/asset.go) accepts, or
// the request 400s and the scenario measures an error path instead
// of the real handler. The four accepted shapes used below are:
//
//   - `native`                       — XLM (the per-network form;
//                                       SDEX trades land under this)
//   - `crypto:<TICKER>`              — off-chain global ticker
//                                       (ADR-0014 allow-list; XLM as
//                                       `crypto:XLM` is the form CEX
//                                       trades + the aggregator VWAP
//                                       under, BTC as `crypto:BTC`)
//   - `fiat:<ISO4217>`               — off-chain reference currency
//                                       (ADR-0010 allow-list)
//   - `<CODE>-<G_ISSUER>`            — classic Stellar asset
//
// Bare codes ('XLM' aside, which ParseAsset aliases to native) like
// 'USD'/'USDC'/'AQUA' are REJECTED — they need a prefix or an
// issuer. The pre-G22-01 fixture used bare codes and silently
// measured 400 paths for ~half the mix.
//
// Issuers below are mainnet, deterministic on r1:
//   USDC  — Circle:    GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN
//   AQUA  — Aqua:      GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA
//            (vanity issuer — ends in `AQUA`; a pre-2026-06-13 fixture
//            had `…M67AB6V`, an invalid CRC, which 400'd every request
//            on the AQUA pair and inflated the acceptance error rate.)
//
// EVERY pair below is verified to return 200 on ALL FOUR endpoints the
// acceptance scenario drives (/price, /price/tip, /ohlc, /assets/{base})
// against r1. Pairs that 404 on the served tier are NOT measuring server
// latency — they measure a no-data path and poison the error-rate
// threshold. The dropped `crypto:USDT/fiat:USD` and `crypto:USDC/fiat:USD`
// pairs are exactly this trap: USDT/USDC are CEX *quote* currencies, so
// there is no spot USDT/USD or USDC/USD trade stream to price (404).

const USDC = 'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN';
const AQUA = 'AQUA-GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA';

export const PAIRS = [
  // XLM majors — both canonical forms so the alias-loop read path
  // (readPriceWithAliases) is exercised in both directions.
  { asset: 'native',     quote: 'fiat:USD',   weight: 30 },
  { asset: 'crypto:XLM', quote: 'fiat:USD',   weight: 12 },
  { asset: 'native',     quote: 'fiat:EUR',   weight: 8  },
  // Stablecoin major quoted in fiat (USDC/USD has a real trade stream).
  { asset: USDC,         quote: 'fiat:USD',   weight: 14 },
  // Crypto majors quoted in fiat.
  { asset: 'crypto:BTC', quote: 'fiat:USD',   weight: 8  },
  { asset: 'crypto:ETH', quote: 'fiat:USD',   weight: 6  },
  // Stellar-native long-tail + stablecoin against XLM (deep SDEX/AMM
  // liquidity — exercises the on-chain pricing path).
  { asset: AQUA,         quote: 'native',     weight: 8  },
  { asset: USDC,         quote: 'native',     weight: 8  },
  // XLM priced in a stablecoin (USDT-as-USD-proxy aggregator path).
  { asset: 'native',     quote: 'crypto:USDT', weight: 6 },
];

const totalWeight = PAIRS.reduce((s, p) => s + p.weight, 0);

// pickWeighted returns a pair sampled by weight. Deterministic-ish
// across VUs because k6 seeds Math.random per VU.
export function pickWeighted() {
  let r = Math.random() * totalWeight;
  for (const p of PAIRS) {
    r -= p.weight;
    if (r <= 0) return p;
  }
  return PAIRS[PAIRS.length - 1];
}

// pickN returns N distinct pairs without replacement; used by
// /v1/price/batch scenarios.
export function pickN(n) {
  const pool = [...PAIRS];
  const out = [];
  for (let i = 0; i < n && pool.length; i++) {
    const idx = Math.floor(Math.random() * pool.length);
    out.push(pool.splice(idx, 1)[0]);
  }
  return out;
}

// enc URL-encodes a canonical asset id for safe inclusion in a query
// string. `fiat:USD` / `crypto:XLM` carry a colon and classic ids a
// dash — all query-safe unencoded per RFC 3986, but encoding keeps
// the suite robust against any future id shape (e.g. muxed forms).
export function enc(id) {
  return encodeURIComponent(id);
}
