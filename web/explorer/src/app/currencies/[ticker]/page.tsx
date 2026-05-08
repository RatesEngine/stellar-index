import type { Metadata } from 'next';
import { notFound } from 'next/navigation';

import { CurrencyDetailView } from './CurrencyDetailView';
import { allFriendlySlugs, resolveFiatSlug } from './slugs';

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
// ticker has a static page, plus pre-render every curated friendly
// slug (`us-dollar`, `euro`, …) so the SEO-friendly URLs route
// without a build-time round trip.
export async function generateStaticParams() {
  const friendly = allFriendlySlugs().map((ticker) => ({ ticker }));
  if (isCIStub) {
    return [
      ...FALLBACK_TICKERS.map((ticker) => ({ ticker })),
      ...friendly,
    ];
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
    const merged = (tickers.length === 0 ? FALLBACK_TICKERS : tickers).map(
      (ticker) => ({ ticker }),
    );
    return [...merged, ...friendly];
  } catch {
    return [
      ...FALLBACK_TICKERS.map((ticker) => ({ ticker })),
      ...friendly,
    ];
  }
}

export async function generateMetadata({ params }: { params: Params }): Promise<Metadata> {
  const { ticker } = await params;
  const resolved = resolveFiatSlug(ticker);
  if (!resolved) {
    return { title: `Currency not found` };
  }
  return {
    title: `${resolved} — currency converter + cross-rates`,
    description: `Live ${resolved} forex rate against USD, currency converter widget, and cross-rates against every other ${resolved}-denominated pair we track. Source: /v1/currencies/${resolved}.`,
  };
}

export default async function CurrencyDetailPage({ params }: { params: Params }) {
  const { ticker } = await params;
  const resolved = resolveFiatSlug(ticker);
  if (!resolved) notFound();
  return <CurrencyDetailView ticker={resolved} />;
}
