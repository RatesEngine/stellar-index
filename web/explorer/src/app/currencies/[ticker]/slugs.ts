// Friendly-slug map for fiat currencies. Resolves URLs like
// /currencies/us-dollar to the underlying ISO 4217 code (USD)
// the data layer expects. Curated for the major currencies users
// are most likely to deep-link by name; less common currencies
// fall back to their ISO code as the slug.
//
// Reverse direction: ISO code → friendly slug (used by the
// listing's row href to produce SEO-friendly URLs).
//
// One-line addition extends the map either direction. Names are
// the central bank's own English form unless ambiguity required
// disambiguation (e.g. "australian-dollar" vs the bare "dollar"
// which would collide with USD/CAD/HKD/etc).

const FRIENDLY_SLUG_TO_TICKER: Record<string, string> = {
  'us-dollar': 'USD',
  'usd': 'USD',
  'euro': 'EUR',
  'eur': 'EUR',
  'british-pound': 'GBP',
  'pound-sterling': 'GBP',
  'gbp': 'GBP',
  'japanese-yen': 'JPY',
  'yen': 'JPY',
  'jpy': 'JPY',
  'swiss-franc': 'CHF',
  'chf': 'CHF',
  'canadian-dollar': 'CAD',
  'cad': 'CAD',
  'australian-dollar': 'AUD',
  'aud': 'AUD',
  'new-zealand-dollar': 'NZD',
  'nzd': 'NZD',
  'chinese-yuan': 'CNY',
  'yuan': 'CNY',
  'cny': 'CNY',
  'renminbi': 'CNY',
  'indian-rupee': 'INR',
  'rupee': 'INR',
  'inr': 'INR',
  'brazilian-real': 'BRL',
  'real': 'BRL',
  'brl': 'BRL',
  'mexican-peso': 'MXN',
  'mxn': 'MXN',
  'south-african-rand': 'ZAR',
  'rand': 'ZAR',
  'zar': 'ZAR',
  'singapore-dollar': 'SGD',
  'sgd': 'SGD',
  'hong-kong-dollar': 'HKD',
  'hkd': 'HKD',
  'swedish-krona': 'SEK',
  'sek': 'SEK',
  'norwegian-krone': 'NOK',
  'nok': 'NOK',
  'danish-krone': 'DKK',
  'dkk': 'DKK',
  'south-korean-won': 'KRW',
  'won': 'KRW',
  'krw': 'KRW',
  'turkish-lira': 'TRY',
  'lira': 'TRY',
  'try': 'TRY',
  'polish-zloty': 'PLN',
  'zloty': 'PLN',
  'pln': 'PLN',
  'russian-ruble': 'RUB',
  'ruble': 'RUB',
  'rouble': 'RUB',
  'rub': 'RUB',
  'thai-baht': 'THB',
  'baht': 'THB',
  'thb': 'THB',
  'philippine-peso': 'PHP',
  'php': 'PHP',
  'nigerian-naira': 'NGN',
  'naira': 'NGN',
  'ngn': 'NGN',
};

const TICKER_TO_FRIENDLY_SLUG: Record<string, string> = {
  USD: 'us-dollar',
  EUR: 'euro',
  GBP: 'british-pound',
  JPY: 'japanese-yen',
  CHF: 'swiss-franc',
  CAD: 'canadian-dollar',
  AUD: 'australian-dollar',
  NZD: 'new-zealand-dollar',
  CNY: 'chinese-yuan',
  INR: 'indian-rupee',
  BRL: 'brazilian-real',
  MXN: 'mexican-peso',
  ZAR: 'south-african-rand',
  SGD: 'singapore-dollar',
  HKD: 'hong-kong-dollar',
  SEK: 'swedish-krona',
  NOK: 'norwegian-krone',
  DKK: 'danish-krone',
  KRW: 'south-korean-won',
  TRY: 'turkish-lira',
  PLN: 'polish-zloty',
  RUB: 'russian-ruble',
  THB: 'thai-baht',
  PHP: 'philippine-peso',
  NGN: 'nigerian-naira',
};

/**
 * resolveFiatSlug — turn whatever the URL provided (ticker, alias,
 * or friendly slug) into the canonical 3-letter ISO 4217 code.
 * Returns null when the input matches nothing — caller should
 * 404 in that case.
 *
 * Crypto friendly slugs (stellar/aquarius/...) deliberately do NOT
 * resolve here — they're served by `public/_redirects` 301s at the
 * CF Pages edge, so /currencies/stellar lands at /assets/XLM
 * before this page even loads.
 */
export function resolveFiatSlug(slug: string): string | null {
  if (!slug) return null;
  const lower = slug.toLowerCase().trim();
  // Exact friendly-slug hit.
  if (FRIENDLY_SLUG_TO_TICKER[lower]) return FRIENDLY_SLUG_TO_TICKER[lower];
  // Plain 3-letter ISO code (case-insensitive).
  if (/^[a-z]{3}$/.test(lower)) return lower.toUpperCase();
  return null;
}

// Friendly-slug map for Stellar-native crypto. Maps the SEO-friendly
// URL fragment to the backend's canonical /v1/coins slug. Limited
// to assets we actually have pricing data for — non-Stellar names
// like "bitcoin" / "ethereum" are intentionally absent until the
// external supply source ships (#114).
const CRYPTO_FRIENDLY_TO_BACKEND: Record<string, string> = {
  stellar: 'XLM',
  lumens: 'XLM',
  xlm: 'XLM',
  'usd-coin': 'USDC',
  usdc: 'USDC',
  'euro-coin': 'EURC',
  eurc: 'EURC',
  aquarius: 'AQUA',
  aqua: 'AQUA',
  'stronghold-token': 'SHX',
  shx: 'SHX',
  'velo-token': 'VELO',
  velo: 'VELO',
  yxlm: 'yXLM',
  yusdc: 'yUSDC',
};

const BACKEND_TO_CRYPTO_FRIENDLY: Record<string, string> = {
  XLM: 'stellar',
  USDC: 'usd-coin',
  EURC: 'euro-coin',
  AQUA: 'aquarius',
  SHX: 'stronghold-token',
  VELO: 'velo-token',
  yXLM: 'yxlm',
  yUSDC: 'yusdc',
};

/**
 * cryptoFriendlySlugFor — preferred unified `/currencies/{slug}`
 * URL fragment for a crypto asset's backend slug. Returns null
 * for assets without a curated friendly alias.
 */
export function cryptoFriendlySlugFor(backendSlug: string): string | null {
  return BACKEND_TO_CRYPTO_FRIENDLY[backendSlug] ?? null;
}

/**
 * allCryptoFriendlyRedirects — every (friendly-slug, backend-slug)
 * pair this module knows about. Surfaced as a function so future
 * tooling (e.g. a CI lint that diffs this against `public/_redirects`)
 * can iterate without re-exporting the underlying map.
 */
export function allCryptoFriendlyRedirects(): { from: string; to: string }[] {
  return Object.entries(CRYPTO_FRIENDLY_TO_BACKEND).map(([from, to]) => ({
    from,
    to,
  }));
}

/**
 * friendlySlugFor — preferred external-facing URL slug for a
 * given ticker. Falls back to the lower-cased ticker for codes
 * without a curated friendly name.
 */
export function friendlySlugFor(ticker: string): string {
  const upper = ticker.toUpperCase();
  return TICKER_TO_FRIENDLY_SLUG[upper] ?? upper.toLowerCase();
}

/**
 * allFriendlySlugs — every key in the friendly-slug map. Used at
 * build time to pre-render aliasing routes alongside their bare-
 * ticker counterparts.
 */
export function allFriendlySlugs(): string[] {
  return Object.keys(FRIENDLY_SLUG_TO_TICKER);
}
