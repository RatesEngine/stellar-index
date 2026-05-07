import type { Metadata } from 'next';

import { CurrencyDetailView } from './CurrencyDetailView';

// Fallback list when the build-time fetch fails. Covers the
// majors so /currencies/EUR etc. always pre-renders even on a
// brand-new build with no upstream connectivity.
const FALLBACK_TICKERS = [
  'USD', 'EUR', 'GBP', 'JPY', 'CHF', 'CAD', 'AUD', 'CNY',
  'INR', 'BRL', 'MXN', 'ZAR', 'NZD', 'SGD', 'HKD', 'SEK',
  'NOK', 'KRW', 'TRY', 'PLN',
];

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net';

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

const BUILD_FETCH_TIMEOUT_MS = 8_000;

type Params = Promise<{ ticker: string }>;

// Fetch the full currency list at build so every supported
// ticker has a static page. Falls back to the majors list when
// the upstream is unavailable (CI stub / cold cache).
export async function generateStaticParams() {
  if (isCIStub) {
    return FALLBACK_TICKERS.map((ticker) => ({ ticker }));
  }
  try {
    const res = await fetch(`${API_BASE_URL}/v1/currencies`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as {
      data: { currencies?: { ticker: string }[] };
    };
    const tickers = env.data?.currencies?.map((c) => c.ticker) ?? [];
    if (tickers.length === 0) return FALLBACK_TICKERS.map((ticker) => ({ ticker }));
    return tickers.map((ticker) => ({ ticker }));
  } catch {
    return FALLBACK_TICKERS.map((ticker) => ({ ticker }));
  }
}

export async function generateMetadata({ params }: { params: Params }): Promise<Metadata> {
  const { ticker } = await params;
  const upper = ticker.toUpperCase();
  return {
    title: `${upper} — currency converter + cross-rates`,
    description: `Live ${upper} forex rate against USD, currency converter widget, and cross-rates against every other ${upper}-denominated pair we track. Source: /v1/currencies/${upper}.`,
  };
}

export default async function CurrencyDetailPage({ params }: { params: Params }) {
  const { ticker } = await params;
  return <CurrencyDetailView ticker={ticker} />;
}
