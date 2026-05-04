import type { CandlePoint } from '@/components/charts/CandleChart';

/**
 * Deterministic-but-realistic-looking OHLC seed for a slug. Used
 * by the chart tab during the design-iteration phase, swapped for
 * /v1/chart hookups once that endpoint feeds OHLC bars.
 *
 * Seeded by the slug so two identical visits produce the same
 * chart — important for snapshot comparisons during review.
 */
export function chartSeedFor(
  slug: string,
  startPrice: number,
  count = 144,
  bucketSeconds = 60 * 10,
): CandlePoint[] {
  const seed = hashString(slug);
  const rand = lcg(seed);
  const out: CandlePoint[] = [];
  let last = startPrice;
  const now = Math.floor(Date.now() / bucketSeconds / 1000) * bucketSeconds;
  for (let i = count - 1; i >= 0; i--) {
    const time = now - i * bucketSeconds;
    const drift = (rand() - 0.5) * 0.02 * last; // up to ±1% per bar
    const open = last;
    const close = Math.max(0.000001, last + drift);
    const high = Math.max(open, close) * (1 + rand() * 0.005);
    const low = Math.min(open, close) * (1 - rand() * 0.005);
    out.push({ time, open, high, low, close });
    last = close;
  }
  return out;
}

/** djb2 hash → 32-bit unsigned. Stable for the same input across builds. */
function hashString(s: string): number {
  let h = 5381 >>> 0;
  for (let i = 0; i < s.length; i++) {
    h = ((h << 5) + h + s.charCodeAt(i)) >>> 0;
  }
  return h;
}

/** Linear-congruential generator — deterministic [0, 1) stream. */
function lcg(seed: number): () => number {
  let state = seed || 1;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 4294967296;
  };
}
