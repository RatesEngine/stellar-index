import type { Metadata } from 'next';
import Link from 'next/link';
import { notFound } from 'next/navigation';
import { ArrowLeft, ExternalLink } from 'lucide-react';

import { PoolsTable } from './PoolsTable';
import { SourceStatsPanel } from './SourceStatsPanel';

// Curated list of DEX sources with friendly names + audit links.
// Mirrors the 5 cards on /dexes; per-DEX detail pages are
// statically pre-rendered for these slugs only. New DEXes added
// here automatically get a /dexes/<source> page.
const DEX_INFO: Record<
  string,
  { name: string; type: string; status: string; discoveryDoc: string; contractsUrl?: string; blurb: string }
> = {
  soroswap: {
    name: 'Soroswap',
    type: 'Uniswap V2 clone (Soroban)',
    status: 'live',
    discoveryDoc: '/research/discovery/soroswap',
    contractsUrl: 'https://github.com/soroswap/core',
    blurb:
      'Constant-product AMM. Each pool below is a SoroswapPair contract. Click a pool to drill into its trade history and live VWAP.',
  },
  phoenix: {
    name: 'Phoenix',
    type: 'AMM (Soroban)',
    status: 'live',
    discoveryDoc: '/research/discovery/phoenix',
    blurb:
      'Soroban AMM with per-field event split. Each pool below is one Phoenix pair contract.',
  },
  aquarius: {
    name: 'Aquarius',
    type: 'AMM with gauges (Soroban)',
    status: 'live',
    discoveryDoc: '/research/discovery/aquarius',
    blurb:
      'Curve-style AMM with bribe/gauge layer. Constant-product and stableswap pools render uniformly.',
  },
  sdex: {
    name: 'SDEX',
    type: 'Native order book (classic)',
    status: 'native',
    discoveryDoc: '/research/discovery/sdex',
    blurb:
      'Stellar-native on-chain order book. Each row below is a (base, quote) classic-asset pair that traded on SDEX in the recency window.',
  },
  comet: {
    name: 'Comet',
    type: 'Balancer V1 fork (Soroban)',
    status: 'experimental',
    discoveryDoc: '/research/discovery/comet',
    blurb:
      'Balancer-style multi-asset pool. Shared ("POOL", <event>) topic across every Comet pool contract.',
  },
};

type Params = Promise<{ source: string }>;

export function generateStaticParams() {
  return Object.keys(DEX_INFO).map((source) => ({ source }));
}

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { source } = await params;
  const info = DEX_INFO[source];
  if (!info) return { title: 'DEX not found' };
  const canonical = `https://ratesengine.net/dexes/${encodeURIComponent(source)}`;
  return {
    title: `${info.name} — every pool, live`,
    description: `All ${info.name} pools observed in the last 14 days, with per-pool 24h trade count + last trade. Source: /v1/markets?source=${source}.`,
    alternates: { canonical },
  };
}

export default async function SourceDetailPage({
  params,
}: {
  params: Params;
}) {
  const { source } = await params;
  const info = DEX_INFO[source];
  if (!info) notFound();

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <Link
        href="/dexes"
        className="inline-flex items-center gap-1.5 text-sm text-slate-600 hover:text-brand-600 dark:text-slate-400"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        All DEXes
      </Link>

      <header className="space-y-2 border-b border-slate-200 pb-4 dark:border-slate-800">
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="text-3xl font-semibold tracking-tight">
            {info.name}
          </h1>
          <span className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-400">
            {info.type}
          </span>
        </div>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          {info.blurb}
        </p>
      </header>

      <SourceStatsPanel source={source} />

      <PoolsTable source={source} sourceName={info.name} />

      <div className="flex flex-wrap gap-3 text-xs">
        <Link
          href={info.discoveryDoc}
          className="inline-flex items-center gap-1 text-brand-600 hover:underline"
        >
          Read integration audit →
        </Link>
        <Link
          href={`/sources/${source}`}
          className="inline-flex items-center gap-1 text-slate-500 hover:text-brand-600"
        >
          Source registry detail →
        </Link>
        {info.contractsUrl && (
          <a
            href={info.contractsUrl}
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex items-center gap-1 text-slate-500 hover:underline"
          >
            Contracts source
            <ExternalLink className="h-3 w-3" />
          </a>
        )}
      </div>
    </div>
  );
}

