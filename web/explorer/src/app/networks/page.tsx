import type { Metadata } from 'next';
import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { NetworkLivePanel } from '../HomeLivePanels';
import { HomeNetworkStrip } from '../HomeNetworkStrip';
import { HomeTopMarkets } from '../HomeTopMarkets';
import { HomeTopAssets } from '../HomeTopAssets';

export const metadata: Metadata = {
  title: 'Networks — connected blockchains',
  description:
    'Per-network macro pulse: total assets, active markets, 24h USD volume, source contributors, ingest tip. Stellar today; more networks as we connect them.',
  alternates: { canonical: '/networks' },
};

export default function NetworkPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-8 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Networks</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Live macro pulse for every connected network. Stellar is the only
          one wired today; more land as we extend ingest. Every figure below
          comes straight from the public API — no synthesised data, no
          estimates. Cross-region active-active per{' '}
          <Link href="/research/adr/0008" className="underline decoration-dotted">
            ADR-0008
          </Link>
          ; you&apos;re seeing R1 (Hetzner) right now.
        </p>
      </header>

      <HomeNetworkStrip />

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <NetworkLivePanel />

        <Panel
          title="Architecture"
          hint="Three-region active-active per ADR-0008"
          bodyClassName="text-sm text-slate-600 dark:text-slate-400 space-y-2"
        >
          <p>
            Three full validators in geographically separate regions
            (R1 Hetzner, R2 AWS, R3 Vultr) per{' '}
            <Link
              href="/research/adr/0004"
              className="underline decoration-dotted"
            >
              ADR-0004
            </Link>
            , each with an independent history archive. Each region runs
            its own indexer + aggregator + API.
          </p>
          <p>
            Per{' '}
            <Link
              href="/research/adr/0015"
              className="underline decoration-dotted"
            >
              ADR-0015
            </Link>
            , every region serves the same rate at the same wall-clock
            time even though each ingests independently — the API only
            ever serves CLOSED buckets. The in-progress bucket is
            invisible until the next minute boundary.
          </p>
          <p>
            R2 + R3 are deferred — see{' '}
            <Link href="/diagnostics" className="underline decoration-dotted">
              /diagnostics
            </Link>{' '}
            for live ingest cursors on the running R1 region.
          </p>
        </Panel>
      </div>

      <HomeTopMarkets />

      <HomeTopAssets />

      <Panel
        title="What's not on this page (yet)"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400 space-y-2"
      >
        <p>
          The full enterprise macro pulse — total network TVL, multi-window
          deltas, Soroban activity index, peg-health strip, ops-per-ledger
          throughput, and fee-market history — plumbs in as the underlying
          Phase 3 writers ship. Schema is in place (migrations 0021 + 0022
          cover TVL + change_summary; peg-health and source-diversity
          computers land next).
        </p>
        <p>
          For per-source ingest health, see{' '}
          <Link href="/diagnostics" className="underline decoration-dotted">
            /diagnostics
          </Link>
          . For per-pair detail, browse{' '}
          <Link href="/markets" className="underline decoration-dotted">
            /markets
          </Link>
          . For asset coverage, see{' '}
          <Link href="/assets" className="underline decoration-dotted">
            /assets
          </Link>
          .
        </p>
      </Panel>
    </div>
  );
}
