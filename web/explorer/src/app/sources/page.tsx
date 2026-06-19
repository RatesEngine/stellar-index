import type { Metadata } from 'next';
import Link from 'next/link';
import { SourcesTable } from './SourcesTable';

export const metadata: Metadata = {
  alternates: { canonical: '/sources' },
  title: 'Stellar sources — every on-chain venue we index',
  description:
    'Live registry of the Stellar on-chain sources we index, grouped by class (DEX / oracle / lending / router / bridge). Only DEX-class contributes to VWAP by default.',
};

/**
 * /sources — directory of every venue we ingest.
 *
 * Live-data pass: groups by class (exchange / aggregator / oracle /
 * authority_sanity) so the "only Class=exchange contributes to VWAP
 * by default" boundary is visible at a glance. Per-source health
 * metrics (events seen 24h, decode errors, orphan rate, last
 * decoded ledger) plumb in once `/v1/sources/{name}/health` ships;
 * the WASM-history pane lands once decoder_stats + wasm_versions
 * are joined into the response.
 */
export default function SourcesPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Stellar sources</h1>
        <p className="max-w-3xl text-sm text-ink-body">
          Every Stellar on-chain venue we index, grouped by class. Only DEX
          venues contribute to VWAP by default — on-chain oracles, lending,
          routers and bridges are reported alongside but excluded so we
          don&apos;t import their methodology. Off-chain reference feeds (CEX,
          aggregators, FX) that back the pricing layer live under{' '}
          <Link href="/exchanges" className="text-brand-600 hover:underline">
            exchanges
          </Link>
          .
        </p>
      </header>

      <SourcesTable />
    </div>
  );
}
