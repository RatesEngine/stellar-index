import type { Metadata } from 'next';
import { Suspense } from 'react';
import { AssetsTable } from './AssetsTable';

export const metadata: Metadata = {
  title: 'Assets — every token on Stellar',
  description:
    'Browse every classic and Soroban asset observed on Stellar — live price, 24h volume, market cap, supply, issuer. The canonical Stellar asset directory.',
};

/**
 * /assets — the explorer's asset directory.
 *
 * Server-component shell wraps a client-side table in Suspense so
 * the static export can pre-render the page chrome while the
 * client reads `?cursor=` / `?limit=` / `?issuer=` from the URL.
 */
export default function AssetsPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight text-ink dark:text-slate-100">
          Assets
        </h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Every classic + Soroban asset observed on Stellar. Live price
          via VWAP across on-chain DEXes, classic SDEX, and major
          off-chain venues. Click through for live charts, recent
          trades, supply detail, and issuer profile.
        </p>
      </header>
      <Suspense
        fallback={
          <div className="rounded-md border border-slate-200 bg-white p-8 text-center text-sm text-slate-500 dark:border-slate-800 dark:bg-slate-900">
            Loading…
          </div>
        }
      >
        <AssetsTable />
      </Suspense>
    </div>
  );
}
