import type { Metadata } from 'next';
import { notFound } from 'next/navigation';

import { SITE_OG_IMAGES, SITE_TWITTER_IMAGES } from '@/lib/seo';
import { CurrencyDetailView } from './CurrencyDetailView';
import { faqFor, nameFor } from './faq';
import { allFriendlySlugs, friendlySlugFor, resolveFiatSlug } from './slugs';

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
  const name = nameFor(resolved);
  const canonical = `https://ratesengine.net/currencies/${friendlySlugFor(resolved)}`;
  // USD vs USD is always 1.0 by definition — frame the page as a
  // cross-rates hub for the base currency rather than as a USD
  // exchange-rate page (which would read as "USD rate against USD").
  const isUSD = resolved === 'USD';
  const title = isUSD
    ? `${resolved} (${name}) — cross-rates against every currency`
    : `${resolved} (${name}) — live USD rate + cross-rates`;
  const description = isUSD
    ? `Live cross-rates from US Dollar (USD) into every other currency we track, currency converter, and 7d / 30d / 90d / 1y / 5y / 10y history. Sourced from Massive (Polygon.io); refreshed hourly. CSV export available.`
    : `Live ${name} (${resolved}) forex rate against USD, currency converter, and cross-rates against every other currency we track. Sourced from Massive (Polygon.io); refreshed hourly. Includes 7d / 30d / 90d / 1y / 5y / 10y history and CSV export.`;
  return {
    title,
    description,
    alternates: { canonical },
    openGraph: {
      title,
      description,
      url: canonical,
      type: 'website',
      siteName: 'Rates Engine',
      images: SITE_OG_IMAGES,
    },
    twitter: {
      card: 'summary_large_image',
      title,
      description,
      images: SITE_TWITTER_IMAGES,
    },
  };
}

export default async function CurrencyDetailPage({ params }: { params: Params }) {
  const { ticker } = await params;
  const resolved = resolveFiatSlug(ticker);
  if (!resolved) notFound();
  const name = nameFor(resolved);
  const canonical = `https://ratesengine.net/currencies/${friendlySlugFor(resolved)}`;
  const faqLd = {
    '@context': 'https://schema.org',
    '@type': 'FAQPage',
    mainEntity: faqFor(resolved, name).map((entry) => ({
      '@type': 'Question',
      name: entry.q,
      acceptedAnswer: {
        '@type': 'Answer',
        text: entry.a,
      },
    })),
  };
  const breadcrumbLd = {
    '@context': 'https://schema.org',
    '@type': 'BreadcrumbList',
    itemListElement: [
      {
        '@type': 'ListItem',
        position: 1,
        name: 'Currencies',
        item: 'https://ratesengine.net/currencies',
      },
      {
        '@type': 'ListItem',
        position: 2,
        name: `${resolved} — ${name}`,
        item: canonical,
      },
    ],
  };
  return (
    <>
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(faqLd) }}
      />
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(breadcrumbLd) }}
      />
      <CurrencyDetailView ticker={resolved} />
    </>
  );
}
