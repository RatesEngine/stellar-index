import type { Metadata } from 'next';
import Link from 'next/link';
import { ExternalLink } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { ReferencePriceAggregators } from './ReferencePriceAggregators';

export const metadata: Metadata = {
  title: 'Aggregators — routers and yield wrappers on Stellar',
  description:
    'Soroswap router, DeFindex yield vaults — protocols that route into the underlying DEXes and lending pools. Excluded from VWAP to avoid double-counting upstream markets.',
  alternates: { canonical: '/aggregators' },
};

type ContractRef = {
  label: string;
  cstrkey: string;
};

type Entry = {
  name: string;
  type: 'router' | 'yield';
  blurb: string;
  notes: string[];
  contractsRepo?: string;
  contractRefs?: ContractRef[];
  homepage?: string;
  /** Internal protocol-page slug (/protocols/<slug>) when one exists. */
  protocolSlug?: string;
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
    contractsRepo: 'https://github.com/soroswap/core',
    protocolSlug: 'soroswap',
    contractRefs: [
      // Sourced from soroswap/core public/mainnet.contracts.json,
      // last verified 2026-05-08.
      { label: 'Router', cstrkey: 'CAG5LRYQ5JVEUI5TEID72EYOVX44TTUJT5BQR2J6J77FH65PCCFAJDDH' },
      { label: 'Pair factory', cstrkey: 'CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2' },
    ],
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
    contractsRepo: 'https://github.com/paltalabs/defindex',
    protocolSlug: 'defindex',
    contractRefs: [
      // Sourced from paltalabs/defindex public/mainnet.contracts.json,
      // last verified 2026-05-08.
      { label: 'Factory', cstrkey: 'CDKFHFJIET3A73A2YN4KV7NSV32S6YGQMUFH3DNJXLBWL4SKEGVRNFKI' },
      { label: 'USDC autocompound', cstrkey: 'CDB2WMKQQNVZMEBY7Q7GZ5C7E7IAFSNMZ7GGVD6WKTCEWK7XOIAVZSAP' },
      { label: 'EURC autocompound', cstrkey: 'CC5CE6MWISDXT3MLNQ7R3FVILFVFEIH3COWGH45GJKL6BD2ZHF7F7JVI' },
      { label: 'XLM autocompound', cstrkey: 'CDPWNUW7UMCSVO36VAJSQHQECISPJLCVPDASKHRC5SEROAAZDUQ5DG2Z' },
    ],
    homepage: 'https://defindex.io',
  },
];

export default function AggregatorsPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Aggregators</h1>
        <p className="max-w-3xl text-sm text-ink-body">
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
        bodyClassName="text-sm text-ink-body space-y-2"
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

      <ReferencePriceAggregators />

      <Panel
        title="Coming next"
        bodyClassName="text-sm text-ink-body space-y-2"
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
    <div className="rounded-card border border-line bg-surface p-5 shadow-card">
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-lg font-semibold tracking-tight">{entry.name}</h2>
        <span
          className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wider ${
            entry.type === 'router'
              ? 'bg-brand-100 text-brand-700'
              : 'bg-surface-subtle text-ink-body'
          }`}
        >
          {entry.type === 'router' ? 'Router' : 'Yield vault'}
        </span>
      </div>
      <p className="mt-3 text-sm text-ink-body">
        {entry.blurb}
      </p>
      <ul className="mt-3 space-y-1.5 text-xs text-ink-body">
        {entry.notes.map((n, i) => (
          <li key={i} className="flex gap-2">
            <span className="text-ink-faint">•</span>
            <span>{n}</span>
          </li>
        ))}
      </ul>
      {entry.contractRefs && entry.contractRefs.length > 0 && (
        <div className="mt-4 border-t border-line pt-3">
          <div className="text-[10px] font-medium uppercase tracking-wider text-ink-muted">
            Mainnet contracts
          </div>
          <ul className="mt-1.5 space-y-1 text-xs">
            {entry.contractRefs.map((c) => (
              <li key={c.cstrkey} className="flex items-baseline justify-between gap-3">
                <span className="text-ink-body">
                  {c.label}
                </span>
                <Link
                  href={`/contract?id=${encodeURIComponent(c.cstrkey)}`}
                  className="font-mono text-[11px] text-brand-600 hover:underline"
                  title={`${c.cstrkey} — contract events + code`}
                >
                  {c.cstrkey.slice(0, 6)}…{c.cstrkey.slice(-4)}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}
      <div className="mt-4 flex flex-wrap gap-3 text-xs">
        {entry.protocolSlug && (
          <Link
            href={`/protocols/${entry.protocolSlug}`}
            className="inline-flex items-center gap-1 font-medium text-brand-600 hover:underline"
          >
            Protocol analytics →
          </Link>
        )}
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
        {entry.contractsRepo && (
          <a
            href={entry.contractsRepo}
            className="inline-flex items-center gap-1 text-ink-muted hover:underline"
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
