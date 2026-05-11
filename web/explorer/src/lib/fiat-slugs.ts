// Ticker → friendly /assets/ slug map for fiat currencies.
// Mirrors the verified-currency catalogue's seed.yaml slug values
// — these are the canonical URLs (US-Dollar over USD, chinese-yuan
// over cny) used in <Link href> from the explorer's currency
// listing surfaces.
//
// One entry per ticker (1:1, primary mapping). The catalogue has
// 19 fiat entries; this map covers each one's friendly form.
// Adding a fiat is a one-line addition; the API catalogue is the
// source of truth, this is the routing-layer projection.
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
};

// fiatSlugFor returns the friendly URL slug for an ISO 4217 ticker.
// Unknown tickers fall back to the lower-cased ticker itself so the
// caller always gets a stable URL path (e.g., `AED` → `aed`).
export function fiatSlugFor(ticker: string): string {
  return TICKER_TO_FRIENDLY_SLUG[ticker.toUpperCase()] ?? ticker.toLowerCase();
}

// assetHrefFor wraps fiatSlugFor + the /assets/ prefix so caller
// sites read declaratively: `<Link href={assetHrefFor('USD')}>`.
export function assetHrefFor(ticker: string): string {
  return `/assets/${fiatSlugFor(ticker)}`;
}
