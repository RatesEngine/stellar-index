import type { Metadata } from 'next';
import Link from 'next/link';
import { ExternalLink } from 'lucide-react';

import { Panel } from '@/components/reveal';

export const metadata: Metadata = {
  title: 'Aggregators — routers and yield wrappers on Stellar',
  description:
    'Soroswap router, DeFindex yield vaults — protocols that route into the underlying DEXes and lending pools. Excluded from VWAP to avoid double-counting upstream markets.',
};

type Entry = {
  name: string;
  type: 'router' | 'yield';
  blurb: string;
  notes: string[];
  contracts?: string;
  homepage?: string;
};

const ENTRIES: Entry[] = [
  {
    name: 'Soroswap Router',
    type: 'router',
    blurb:
      'Multi-hop convenience layer over Soroswap pair contracts. Trades that go through the router resolve to the same pair-level swap events we already index — the router is just the entry point.',
    notes: [
      'No router-specific decoder needed for trades — the underlying SoroswapPair swap events fire regardless. The router contract is tracked separately for routed-via attribution (what % of pair volume arrived via the router).',
      'Per the protocol-class contract, routers are in `/v1/sources` with class=aggregator and contribute zero VWAP weight by default. Including them would double-count the underlying pair trade.',
    ],
    contracts: 'https://github.com/soroswap/core',
    homepage: 'https://soroswap.finance',
  },
  {
    name: 'DeFindex',
    type: 'yield',
    blurb:
      'Yield-aggregator vaults that deposit into Blend and other lending pools. Earns the underlying borrow APY plus any incentive emissions, minus a management fee.',
    notes: [
      'Position changes happen on the vault contract (deposit/withdraw); the actual yield-bearing legs are at Blend. We track the vault TVL but rely on Blend for the underlying yield observation.',
      'Excluded from VWAP — yield-aggregator inflows are not price-discovery events.',
    ],
    homepage: 'https://defindex.io',
  },
];

export default function AggregatorsPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Aggregators</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Routers and yield wrappers — protocols that route into the
          underlying{' '}
          <Link href="/dexes" className="underline decoration-dotted">
            DEXes
          </Link>{' '}
          and{' '}
          <Link href="/lending" className="underline decoration-dotted">
            lending pools
          </Link>
          . Reported alongside but excluded from VWAP so we don&apos;t
          double-count the upstream price-discovery event.
        </p>
      </header>

      <Panel
        title="Why aggregators don't price into VWAP"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400 space-y-2"
      >
        <p>
          A trade routed through the Soroswap router still emits a
          SoroswapPair swap event on the underlying pair contract — that
          event is the one we VWAP. Counting the router-level call
          separately would double the same price-discovery moment.
        </p>
        <p>
          The same logic applies to DeFindex: a vault deposit moves
          shares but doesn&apos;t set a price. The underlying Blend
          loan&apos;s collateral revaluation is what we care about, and
          we get that directly from Blend.
        </p>
      </Panel>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {ENTRIES.map((e) => (
          <Card key={e.name} entry={e} />
        ))}
      </div>

      <Panel
        title="Coming next"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400 space-y-2"
      >
        <p>
          Routed-via attribution (what fraction of each underlying pair
          volume came in via a router or aggregator) is in the database
          schema — see migration{' '}
          <code className="font-mono text-xs">
            0025_create_routers_and_aggregator_exposures.sql
          </code>{' '}
          — but needs the routers registry seeded on r1 before the
          attribution endpoint goes live.
        </p>
      </Panel>
    </div>
  );
}

function Card({ entry }: { entry: Entry }) {
  return (
    <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-800 dark:bg-slate-900">
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-lg font-semibold tracking-tight">{entry.name}</h2>
        <span
          className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wider ${
            entry.type === 'router'
              ? 'bg-brand-100 text-brand-700 dark:bg-brand-900 dark:text-brand-200'
              : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400'
          }`}
        >
          {entry.type === 'router' ? 'Router' : 'Yield vault'}
        </span>
      </div>
      <p className="mt-3 text-sm text-slate-700 dark:text-slate-300">
        {entry.blurb}
      </p>
      <ul className="mt-3 space-y-1.5 text-xs text-slate-600 dark:text-slate-400">
        {entry.notes.map((n, i) => (
          <li key={i} className="flex gap-2">
            <span className="text-slate-400">•</span>
            <span>{n}</span>
          </li>
        ))}
      </ul>
      <div className="mt-4 flex flex-wrap gap-3 text-xs">
        {entry.homepage && (
          <a
            href={entry.homepage}
            className="inline-flex items-center gap-1 text-brand-600 hover:underline"
            target="_blank"
            rel="noreferrer"
          >
            Homepage
            <ExternalLink className="h-3 w-3" />
          </a>
        )}
        {entry.contracts && (
          <a
            href={entry.contracts}
            className="inline-flex items-center gap-1 text-slate-500 hover:underline"
            target="_blank"
            rel="noreferrer"
          >
            Contracts source
            <ExternalLink className="h-3 w-3" />
          </a>
        )}
      </div>
    </div>
  );
}
