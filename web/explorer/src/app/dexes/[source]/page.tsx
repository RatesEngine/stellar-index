import type { Metadata } from 'next';
import Link from 'next/link';
import { notFound } from 'next/navigation';
import { ArrowLeft, ExternalLink } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { PoolsTable } from './PoolsTable';

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net';

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

const BUILD_FETCH_TIMEOUT_MS = 8_000;

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
  return {
    title: `${info.name} — every pool, live`,
    description: `All ${info.name} pools observed in the last 14 days, with per-pool 24h trade count + last trade. Source: /v1/markets?source=${source}.`,
  };
}

interface SourceStats {
  trade_count_24h?: number;
  volume_24h_usd?: string;
  markets_count_24h?: number;
}

async function fetchSourceStats(source: string): Promise<SourceStats | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(`${API_BASE_URL}/v1/sources?include=stats`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) return null;
    const env = (await res.json()) as { data: Array<{ name: string } & SourceStats> };
    const row = env.data?.find((r) => r.name === source);
    return row ?? null;
  } catch {
    return null;
  }
}

export default async function SourceDetailPage({
  params,
}: {
  params: Params;
}) {
  const { source } = await params;
  const info = DEX_INFO[source];
  if (!info) notFound();

  const stats = await fetchSourceStats(source);
  const trades = stats?.trade_count_24h ?? 0;
  const volume = stats?.volume_24h_usd ? Number(stats.volume_24h_usd) : 0;
  const markets = stats?.markets_count_24h ?? 0;

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

      <Panel
        title="24h activity"
        hint={`Live from /v1/sources?include=stats (source=${source})`}
        source={asExample('/v1/sources', { include: 'stats' })}
      >
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          <Stat
            label="24h volume"
            value={volume > 0 ? `$${formatCompact(volume)}` : '—'}
          />
          <Stat
            label="24h trades"
            value={trades > 0 ? formatCompact(trades) : '—'}
          />
          <Stat
            label="24h pools"
            value={markets > 0 ? markets.toLocaleString() : '—'}
          />
        </div>
      </Panel>

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

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wider text-slate-500">
        {label}
      </div>
      <div className="mt-1 text-2xl font-semibold tabular-nums">{value}</div>
    </div>
  );
}

function formatCompact(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(2)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return n.toLocaleString();
}
