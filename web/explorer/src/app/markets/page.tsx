import { Suspense } from 'react';
import type { Metadata } from 'next';
import { MarketsTable } from './MarketsTable';

export const metadata: Metadata = {
  title: 'Markets — every active trading pair',
  description:
    'Every (base, quote) pair that has traded on Stellar in the last 14 days. Sortable by 24h trade count, with last-trade-relative timestamps.',
};

/**
 * /markets — every active trading pair on Stellar.
 *
 * v0 wires the live `/v1/markets` endpoint with a sortable table.
 * The pair-heatmap, per-venue sub-tables, and live tape (via
 * `/v1/observations/stream`) follow as their underlying data
 * surfaces stabilise.
 */
export default function MarketsPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Markets</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Every (base, quote) pair that has traded on Stellar in the last
          14 days. Heatmap, per-venue sub-tables, and a live trade tape
          land in subsequent passes.
        </p>
      </header>

      <Suspense fallback={null}>
        <MarketsTable />
      </Suspense>
    </div>
  );
}
