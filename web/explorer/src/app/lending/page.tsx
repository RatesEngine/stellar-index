import type { Metadata } from 'next';
import Link from 'next/link';
import { ExternalLink } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { LendingPoolsTable } from './LendingPoolsTable';

export const metadata: Metadata = {
  alternates: { canonical: '/lending' },
  title: 'Lending — collateralised lending on Stellar',
  description:
    'Blend is the primary collateralised-lending protocol on Stellar. Backstop pools, Reflector dependency, MEV-relevant liquidations.',
};

export default function LendingPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Lending</h1>
        <p className="max-w-3xl text-sm text-ink-body">
          Collateralised lending protocols on Stellar. Yield comes from
          borrowers paying interest, not from external strategies — see{' '}
          <Link href="/aggregators" className="underline decoration-dotted">
            /aggregators
          </Link>{' '}
          for protocols that route into these.
        </p>
      </header>

      <div className="rounded-xl border border-line bg-surface p-5 shadow-sm">
        <div className="flex items-baseline justify-between gap-2">
          <h2 className="text-2xl font-semibold tracking-tight">Blend</h2>
          <span className="rounded-sm bg-up-soft px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-up-strong">
            Live
          </span>
        </div>
        <p className="mt-1 text-xs uppercase tracking-wider text-ink-muted">
          Isolated-pool lending · Reflector-priced collateral · Comet
          backstop
        </p>
        <p className="mt-3 text-sm text-ink-body">
          Blend is the primary lending protocol on Stellar. Each pool is
          isolated (Aave-V3 style), with collateral and borrow assets
          chosen per-pool by the operator. Liquidations execute against
          a Comet-style auction backstop.
        </p>
        <ul className="mt-3 space-y-2 text-sm text-ink-body">
          <li className="flex gap-2">
            <span className="text-ink-faint">•</span>
            <span>
              <strong className="text-ink-body">
                Reflector-priced.
              </strong>{' '}
              Each pool reads the SEP-40 Reflector oracle for collateral
              valuation. A divergence between Reflector and our VWAP
              materially changes the liquidation threshold — we surface
              it on the canonical coin pages via{' '}
              <code className="font-mono text-xs">flags.divergence_warning</code>.
            </span>
          </li>
          <li className="flex gap-2">
            <span className="text-ink-faint">•</span>
            <span>
              <strong className="text-ink-body">
                Backstop is a Comet pool.
              </strong>{' '}
              The Balancer-V1-derived Comet contract auctions liquidated
              positions. We index the same Comet code path (see{' '}
              <Link
                href="/dexes"
                className="underline decoration-dotted hover:text-brand-600"
              >
                /dexes
              </Link>
              ); the Blend backstop is one specific contract address.
            </span>
          </li>
          <li className="flex gap-2">
            <span className="text-ink-faint">•</span>
            <span>
              <strong className="text-ink-body">
                MEV-relevant.
              </strong>{' '}
              Liquidations can sandwich, especially when the oracle
              update and the liquidate call land in the same ledger. The
              future{' '}
              <Link href="/mev" className="underline decoration-dotted">
                /mev
              </Link>{' '}
              page tracks Blend liquidation MEV separately from DEX MEV.
            </span>
          </li>
        </ul>
        <div className="mt-4 flex flex-wrap gap-3 text-xs">
          <a
            href="https://github.com/blend-capital/blend-contracts"
            className="inline-flex items-center gap-1 text-ink-muted hover:underline"
            target="_blank"
            rel="noreferrer"
          >
            Contracts source
            <ExternalLink className="h-3 w-3" />
          </a>
        </div>
      </div>

      <LendingPoolsTable />

      <Panel
        title="Coming next"
        bodyClassName="text-sm text-ink-body space-y-2"
      >
        <p>
          Per-pool TVL + utilisation + supply/borrow APY plumb in once
          the pool-storage reader worker ships. For now the table
          shows pools observed in the auction stream — every pool
          that has emitted a new / fill / delete auction event.
          Pools that exist on-chain but haven&apos;t had a liquidation
          since launch won&apos;t appear until they do.
        </p>
        <p>
          For more context: head to{' '}
          <Link href="/sources" className="underline decoration-dotted">
            /sources
          </Link>{' '}
          to see Blend in the source registry, or to{' '}
          <Link href="/oracles" className="underline decoration-dotted">
            /oracles
          </Link>{' '}
          to see the Reflector dependency Blend reads from.
        </p>
      </Panel>
    </div>
  );
}
