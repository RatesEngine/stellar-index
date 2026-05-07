import type { Metadata } from 'next';
import Link from 'next/link';
import { ExternalLink } from 'lucide-react';

import { Panel } from '@/components/reveal';

export const metadata: Metadata = {
  title: 'Lending — collateralised lending on Stellar',
  description:
    'Blend is the primary collateralised-lending protocol on Stellar. Backstop pools, Reflector dependency, MEV-relevant liquidations.',
};

export default function LendingPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Lending</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Collateralised lending protocols on Stellar. Yield comes from
          borrowers paying interest, not from external strategies — see{' '}
          <Link href="/aggregators" className="underline decoration-dotted">
            /aggregators
          </Link>{' '}
          for protocols that route into these.
        </p>
      </header>

      <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-baseline justify-between gap-2">
          <h2 className="text-2xl font-semibold tracking-tight">Blend</h2>
          <span className="rounded bg-up-soft px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-up-strong">
            Live
          </span>
        </div>
        <p className="mt-1 text-xs uppercase tracking-wider text-slate-500">
          Isolated-pool lending · Reflector-priced collateral · Comet
          backstop
        </p>
        <p className="mt-3 text-sm text-slate-700 dark:text-slate-300">
          Blend is the primary lending protocol on Stellar. Each pool is
          isolated (Aave-V3 style), with collateral and borrow assets
          chosen per-pool by the operator. Liquidations execute against
          a Comet-style auction backstop.
        </p>
        <ul className="mt-3 space-y-2 text-sm text-slate-600 dark:text-slate-400">
          <li className="flex gap-2">
            <span className="text-slate-400">•</span>
            <span>
              <strong className="text-slate-700 dark:text-slate-200">
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
            <span className="text-slate-400">•</span>
            <span>
              <strong className="text-slate-700 dark:text-slate-200">
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
            <span className="text-slate-400">•</span>
            <span>
              <strong className="text-slate-700 dark:text-slate-200">
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
          <Link
            href="/research/discovery/blend"
            className="inline-flex items-center gap-1 text-brand-600 hover:underline"
          >
            Read Blend integration audit →
          </Link>
          <Link
            href="/research/discovery/comet"
            className="inline-flex items-center gap-1 text-slate-500 hover:underline"
          >
            Comet backstop audit →
          </Link>
          <a
            href="https://github.com/blend-capital/blend-contracts"
            className="inline-flex items-center gap-1 text-slate-500 hover:underline"
            target="_blank"
            rel="noreferrer"
          >
            Contracts source
            <ExternalLink className="h-3 w-3" />
          </a>
        </div>
      </div>

      <Panel
        title="Coming next"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400 space-y-2"
      >
        <p>
          Per-pool TVL + utilisation + supply/borrow APY plumb in once
          the TVL writer worker ships (Phase 3 — pending the
          protocol→pool registry on r1). The auctions panel and
          backstop coverage % follow alongside.
        </p>
        <p>
          For now, head to{' '}
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
