// Curated currency-detail FAQ + name map. Shared between the
// page-level server component (which emits JSON-LD structured
// data at build time) and the client detail view (which renders
// the visible FAQ panel from the same source).
//
// CURATED_NAMES is the build-time fallback when the API has not
// yet responded — JSON-LD has to be present in the static HTML
// for crawlers to pick up the rich snippet, so we can't rely on
// runtime data for the canonical names. The runtime view still
// prefers the API's `detail.name` when available.

export const CURATED_NAMES: Record<string, string> = {
  USD: 'US Dollar',
  EUR: 'Euro',
  GBP: 'Pound Sterling',
  JPY: 'Japanese Yen',
  CHF: 'Swiss Franc',
  CAD: 'Canadian Dollar',
  AUD: 'Australian Dollar',
  NZD: 'New Zealand Dollar',
  CNY: 'Chinese Yuan Renminbi',
  INR: 'Indian Rupee',
  BRL: 'Brazilian Real',
  MXN: 'Mexican Peso',
  ZAR: 'South African Rand',
  SGD: 'Singapore Dollar',
  HKD: 'Hong Kong Dollar',
  SEK: 'Swedish Krona',
  NOK: 'Norwegian Krone',
  DKK: 'Danish Krone',
  KRW: 'South Korean Won',
  TRY: 'Turkish Lira',
  PLN: 'Polish Złoty',
  RUB: 'Russian Ruble',
  THB: 'Thai Baht',
  PHP: 'Philippine Peso',
  NGN: 'Nigerian Naira',
  IDR: 'Indonesian Rupiah',
  ILS: 'Israeli New Shekel',
  AED: 'UAE Dirham',
  SAR: 'Saudi Riyal',
  CZK: 'Czech Koruna',
  HUF: 'Hungarian Forint',
  CLP: 'Chilean Peso',
  COP: 'Colombian Peso',
  PEN: 'Peruvian Sol',
  EGP: 'Egyptian Pound',
  PKR: 'Pakistani Rupee',
  BDT: 'Bangladeshi Taka',
  VND: 'Vietnamese Đồng',
  MYR: 'Malaysian Ringgit',
  TWD: 'New Taiwan Dollar',
};

export function nameFor(ticker: string): string {
  return CURATED_NAMES[ticker.toUpperCase()] ?? ticker.toUpperCase();
}

export function faqFor(ticker: string, name?: string): { q: string; a: string }[] {
  const display = name && name.trim() !== '' ? name : nameFor(ticker);
  return [
    {
      q: `What is ${ticker}?`,
      a: `${ticker} is the ISO 4217 currency code for ${display}. Rates Engine quotes its rate against USD via the Massive (Polygon.io) forex feed, refreshed hourly with daily-grain data sourced from major reference series.`,
    },
    {
      q: `How is the rate calculated?`,
      a: `We pull the upstream's grouped-daily snapshot once an hour and surface its USD-base rate verbatim. The "1 USD = N units" form is canonical; the "1 unit = $X" inverse is computed at display time. No internal smoothing or aggregation is applied to the fiat feed — the value you see is what the upstream published.`,
    },
    {
      q: `What is circulating supply for a fiat currency?`,
      a: `For fiat we use the central bank's broadest commonly-published monetary aggregate (typically M2). Where that's unavailable we fall back to monetary base or the issuer's own circulation declaration. Sourced from the curated CSV in internal/sources/forex/circulation_data.csv; not every currency has a recent series.`,
    },
    {
      q: `How often does the ${ticker} rate update?`,
      a: `Hourly. The forex worker refreshes from the upstream every 60 minutes; persistent fx_quotes hypertable rows are upserted on the same cadence. The detail page's "Source published" timestamp shows the upstream's own publish date.`,
    },
  ];
}
