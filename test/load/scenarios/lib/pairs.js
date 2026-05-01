// Representative pair fixtures used by every scenario.
//
// Mix is intentional: XLM majors dominate (matches wallet-shaped
// traffic per the Freighter RFP), with stablecoin majors and a
// long-tail Soroban token to exercise non-cached paths.

export const PAIRS = [
  { asset: 'XLM',    quote: 'USD',  weight: 40 },
  { asset: 'USDC',   quote: 'USD',  weight: 15 },
  { asset: 'XLM',    quote: 'EUR',  weight: 10 },
  { asset: 'USDT',   quote: 'USD',  weight: 8  },
  { asset: 'BTCLN',  quote: 'USD',  weight: 7  },
  { asset: 'AQUA',   quote: 'XLM',  weight: 5  },
  { asset: 'yXLM',   quote: 'USD',  weight: 5  },
  { asset: 'EURC',   quote: 'EUR',  weight: 4  },
  { asset: 'BLND',   quote: 'USD',  weight: 3  },
  { asset: 'XLM',    quote: 'JPY',  weight: 3  },
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
