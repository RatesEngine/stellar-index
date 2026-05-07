import type { Metadata } from 'next';
import { Suspense } from 'react';

import { CompareView } from './CompareView';

export const metadata: Metadata = {
  title: 'Compare assets',
  description:
    'Side-by-side comparison of any 2-6 Stellar assets — price, 1h/24h/7d change, volume, markets count, sparkline. Pick assets via the URL: /compare?assets=USDC,XLM,USDT.',
};

/**
 * /compare?assets=A,B,C — side-by-side asset comparison.
 *
 * The comma-separated `assets` query param picks slugs (the same
 * slugs used by /assets/{slug}). Each cell pulls /v1/coins/{slug}
 * client-side via React Query so the comparison stays current
 * across navigations + refresh; the server component is just the
 * shell.
 *
 * Empty / missing param renders a getting-started UI prompting
 * the user to pick assets via the search bar.
 */
export default function ComparePage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Compare assets</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Side-by-side view of any 2&ndash;6 Stellar assets. Pick assets via
          the URL (<code className="rounded bg-slate-100 px-1 py-0.5 font-mono text-xs dark:bg-slate-800">?assets=USDC,XLM,USDT</code>);
          each column refreshes from <code className="rounded bg-slate-100 px-1 py-0.5 font-mono text-xs dark:bg-slate-800">/v1/coins/{'{slug}'}</code> as you watch.
        </p>
      </header>
      <Suspense fallback={<div className="text-sm text-slate-500">Loading…</div>}>
        <CompareView />
      </Suspense>
    </div>
  );
}
