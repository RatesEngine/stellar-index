import type { Metadata } from 'next';
import { Suspense } from 'react';
import { CoinsTable } from './CoinsTable';

export const metadata: Metadata = {
  title: 'Coins — every asset on Stellar',
  description:
    'Browse every classic and Soroban asset that has been observed on Stellar, ranked by activity. Click through to live charts, markets, issuer detail.',
};

/**
 * /coins server-component shell. Wraps the table in Suspense so
 * static export can pre-render the chrome while the client-side
 * `useSearchParams` reads the sort param at hydration time.
 */
export default function CoinsPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 p-6">
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Coins</h1>
        <p className="text-sm text-slate-500">
          Every asset trading on Stellar — native XLM, classic
          credits, and Soroban tokens. Sorted by 24h volume.
        </p>
      </header>
      <Suspense fallback={<div className="text-sm text-slate-500">Loading…</div>}>
        <CoinsTable />
      </Suspense>
      <p className="text-xs text-slate-500">
        Static seed today. Real data lands when{' '}
        <code className="font-mono">/v1/coins</code> ships
        (data-inventory §10.1) — backed by the registry-aware super-
        table joining{' '}
        <code className="font-mono">classic_assets</code> +{' '}
        <code className="font-mono">change_summary_5m</code>.
      </p>
    </div>
  );
}
