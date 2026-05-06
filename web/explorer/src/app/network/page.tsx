import type { Metadata } from 'next';
import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { NetworkLivePanel } from '../HomeLivePanels';

export const metadata: Metadata = {
  title: 'Network — Stellar macro pulse',
  description:
    'Stellar network state: ledger tip, classic-asset count, ingest motion. Macro-level metrics for the Stellar pricing surface.',
};

export default function NetworkPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Network</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Stellar-network macro state. Today: ledger tip and indexed
          asset count. The full macro pulse — total TVL, total volume,
          Soroban activity, peg health, fee market — lights up as the
          underlying writers ship (Phase 3).
        </p>
      </header>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <NetworkLivePanel />

        <Panel
          title="Architecture"
          hint="Three-region active-active per ADR-0008"
          bodyClassName="text-sm text-slate-600 dark:text-slate-400 space-y-2"
        >
          <p>
            Three full validators in geographically separate regions
            (R1 Hetzner, R2 AWS, R3 Vultr) per ADR-0004, each with an
            independent history archive. Each region runs its own
            indexer + aggregator + API.
          </p>
          <p>
            Per ADR-0015, every region serves the same rate at the same
            wall-clock time even though each ingests independently —
            the API only ever serves CLOSED buckets. The in-progress
            bucket is invisible until the next minute boundary.
          </p>
        </Panel>
      </div>

      <Panel
        title="Coming next"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400 space-y-2"
      >
        <p>
          Total TVL + multi-window deltas, total network volume, Soroban
          activity index, stablecoin peg-health strip, ops-per-ledger
          throughput, and fee-market history all plumb in once the
          underlying Phase 3 writers ship — the schema is in place
          (migrations 0021 + 0022 cover TVL + change_summary; peg-
          health and source-diversity computers land next).
        </p>
        <p>
          For ingest-side liveness today, see{' '}
          <Link href="/diagnostics" className="underline decoration-dotted">
            /diagnostics
          </Link>{' '}
          (per-source cursors), and for the indexed asset directory
          see{' '}
          <Link href="/assets" className="underline decoration-dotted">
            /assets
          </Link>
          .
        </p>
      </Panel>
    </div>
  );
}
