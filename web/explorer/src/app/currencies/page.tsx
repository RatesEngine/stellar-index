import type { Metadata } from 'next';
import { Suspense } from 'react';

import {
  CurrenciesView,
  type CryptoCoin,
  type FiatEnvelope,
} from './CurrenciesView';

export const metadata: Metadata = {
  title: 'Currencies — fiat forex coverage',
  description:
    'World fiat currencies — live USD-base rates from Massive (Polygon.io). Ticker, name, USD-denominated rate, inverse rate. Source: /v1/currencies.',
};

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net';

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

const BUILD_FETCH_TIMEOUT_MS = 8_000;

// fetchInitialCrypto / fetchInitialFiat — at-build-time snapshots
// embedded into the static HTML so first paint shows real rows
// instead of "Loading…". TanStack Query treats these as
// immediately stale (initialDataUpdatedAt: 0) so the listing
// still refetches on mount and gets fresh prices within the
// usual 15s/60s cadence.
async function fetchInitialCrypto(): Promise<CryptoCoin[] | undefined> {
  if (isCIStub) return undefined;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/coins?limit=200&include=sparkline`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return undefined;
    const env = (await res.json()) as { data: { coins: CryptoCoin[] } };
    return env.data?.coins ?? undefined;
  } catch {
    return undefined;
  }
}

async function fetchInitialFiat(): Promise<FiatEnvelope | undefined> {
  if (isCIStub) return undefined;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/currencies?include=sparkline`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return undefined;
    const env = (await res.json()) as { data: FiatEnvelope };
    return env.data ?? undefined;
  } catch {
    return undefined;
  }
}

export default async function CurrenciesPage() {
  const [initialCrypto, initialFiat] = await Promise.all([
    fetchInitialCrypto(),
    fetchInitialFiat(),
  ]);
  // Suspense wrapper required because CurrenciesView reads
  // useSearchParams() (PR #1062 added URL state for filter+sort).
  // Next.js 15 requires Suspense around any client component that
  // reads search params during static export.
  return (
    <Suspense fallback={<div className="mx-auto max-w-7xl px-6 py-8 text-sm text-slate-500">Loading…</div>}>
      <CurrenciesView initialCrypto={initialCrypto} initialFiat={initialFiat} />
    </Suspense>
  );
}
