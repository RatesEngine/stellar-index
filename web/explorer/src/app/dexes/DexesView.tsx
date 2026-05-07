'use client';

import Link from 'next/link';
import { ExternalLink } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { useSources, type Source } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

type DexEntry = {
  name: string;
  source: string;
  type: string;
  status: 'live' | 'experimental' | 'native';
  blurb: string;
  notes: string[];
  contractsUrl?: string;
  discoveryDoc: string;
};

const DEXES: DexEntry[] = [
  {
    name: 'Soroswap',
    source: 'soroswap',
    type: 'Uniswap V2 clone (Soroban)',
    status: 'live',
    blurb:
      'Primary Soroban DEX source — factory + per-pair contracts, event-based ingest verified against the upstream Rust source.',
    notes: [
      'SwapEvent carries only in/out deltas — post-state reserves arrive in the immediately-following SyncEvent. Correlation key is (ledger, tx_hash, op_index).',
      'Pair registry persisted in postgres so live ingest + parallel backfill chunks share one source of truth.',
    ],
    contractsUrl: 'https://github.com/soroswap/core',
    discoveryDoc: '/research/discovery/soroswap',
  },
  {
    name: 'Phoenix',
    source: 'phoenix',
    type: 'AMM (Soroban)',
    status: 'live',
    blurb:
      'Soroban AMM with a unique per-field event split — reconstructing one swap requires grouping eight events tagged ("swap", "<field>") on the same (ledger, tx_hash, op_index).',
    notes: [
      'Every swap fans out across 8 events, one per field of the swap state. Decoder must group them before emitting a single canonical Trade.',
      'Topic shape is a 2-tuple ("swap", "<field>") — different from the protocol-namespaced shapes Soroswap and Aquarius use.',
    ],
    discoveryDoc: '/research/discovery/phoenix',
  },
  {
    name: 'Aquarius',
    source: 'aquarius',
    type: 'AMM with gauges (Soroban)',
    status: 'live',
    blurb:
      'Curve-style AMM with bribe/gauge layer. Constant-product and stableswap pools both ingest into the same canonical Trade shape.',
    notes: [
      'Decoder treats stableswap and constant-product pools uniformly — VWAP is a function of in/out amounts, not the bonding curve.',
    ],
    discoveryDoc: '/research/discovery/aquarius',
  },
  {
    name: 'SDEX',
    source: 'sdex',
    type: 'Native order book (classic)',
    status: 'native',
    blurb:
      'Stellar native on-chain order book. Trades extracted from CAP-67 unified token transfer events plus operations + effects pre-Protocol-23.',
    notes: [
      'Post-Protocol 23 (Whisk, mainnet 2025-09-03) every classic asset movement emits a unified transfer/mint/burn event with a 4th sep0011_asset topic.',
      'Pre-P23 path parses operations + effects directly. Decoder handles both code paths transparently.',
    ],
    discoveryDoc: '/research/discovery/sdex',
  },
  {
    name: 'Comet',
    source: 'comet',
    type: 'Balancer V1 fork (Soroban)',
    status: 'experimental',
    blurb:
      'Balancer-style multi-asset pool. Shared ("POOL", <event>) topic across every Comet pool contract — narrow coverage filters downstream by Trade.Source + contract address.',
    notes: [
      'Topic match alone identifies any pubnet contract that deployed Balancer-V1 Comet code — operators wanting only Blend backstop pools filter at aggregator time, not dispatch time.',
    ],
    discoveryDoc: '/research/discovery/comet',
  },
];

export function DexesView() {
  // Live per-source 24h stats via /v1/sources?include=stats.
  // The map indexes by source name so DEXES[].source can look up
  // its row directly.
  const sources = useSources(undefined, true);
  const statsBySource = new Map<string, Source>();
  for (const s of sources.data ?? []) {
    statsBySource.set(s.name, s);
  }
  const totalVolume = (sources.data ?? [])
    .filter((s) => DEXES.some((d) => d.source === s.name))
    .reduce((sum, s) => {
      const v = s.volume_24h_usd ? Number(s.volume_24h_usd) : 0;
      return sum + (Number.isFinite(v) ? v : 0);
    }, 0);
  const totalTrades = (sources.data ?? [])
    .filter((s) => DEXES.some((d) => d.source === s.name))
    .reduce((sum, s) => sum + (s.trade_count_24h ?? 0), 0);

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">DEXes</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          AMMs and the native order book — every venue we ingest from
          on-chain Stellar. Live 24h numbers below come from{' '}
          <code className="font-mono text-xs">/v1/sources?include=stats</code>
          ; integration audit notes live in each card&apos;s linked
          discovery doc.
        </p>
      </header>

      <Panel
        title="DEX activity (last 24h)"
        hint="Aggregated across the five venues below"
        source={asExample('/v1/sources', { include: 'stats' })}
      >
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          <Stat
            label="Total trades"
            value={totalTrades > 0 ? formatCompact(totalTrades) : '—'}
          />
          <Stat
            label="Total volume"
            value={
              totalVolume > 0 ? `$${formatCompact(totalVolume)}` : '—'
            }
          />
          <Stat
            label="Live venues"
            value={
              sources.data
                ? `${
                    DEXES.filter((d) => {
                      const s = statsBySource.get(d.source);
                      return s && (s.trade_count_24h ?? 0) > 0;
                    }).length
                  } / ${DEXES.length}`
                : '—'
            }
          />
        </div>
      </Panel>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {DEXES.map((d) => (
          <DexCard
            key={d.source}
            entry={d}
            stats={statsBySource.get(d.source)}
            loading={sources.isLoading && !sources.data}
          />
        ))}
      </div>
    </div>
  );
}

function DexCard({
  entry,
  stats,
  loading,
}: {
  entry: DexEntry;
  stats: Source | undefined;
  loading: boolean;
}) {
  const trades = stats?.trade_count_24h ?? 0;
  const volume = stats?.volume_24h_usd ? Number(stats.volume_24h_usd) : 0;
  const markets = stats?.markets_count_24h ?? 0;
  return (
    <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-800 dark:bg-slate-900">
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-lg font-semibold tracking-tight">{entry.name}</h2>
        <StatusBadge status={entry.status} />
      </div>
      <p className="mt-1 text-xs uppercase tracking-wider text-slate-500">
        {entry.type}
      </p>

      <div className="mt-4 grid grid-cols-3 gap-3 rounded-md bg-slate-50 p-3 text-xs dark:bg-slate-950/40">
        <Metric
          label="24h volume"
          value={
            loading
              ? '…'
              : volume > 0
                ? `$${formatCompact(volume)}`
                : '—'
          }
        />
        <Metric
          label="24h trades"
          value={
            loading ? '…' : trades > 0 ? formatCompact(trades) : '—'
          }
        />
        <Metric
          label="24h pools"
          value={loading ? '…' : markets > 0 ? `${markets}` : '—'}
        />
      </div>

      <p className="mt-4 text-sm text-slate-700 dark:text-slate-300">
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
        <Link
          href={entry.discoveryDoc}
          className="inline-flex items-center gap-1 text-brand-600 hover:underline"
        >
          Read integration audit →
        </Link>
        <Link
          href={`/dexes/${entry.source}`}
          className="inline-flex items-center gap-1 font-medium text-brand-600 hover:underline"
        >
          View all pools →
        </Link>
        <Link
          href={`/sources/${entry.source}`}
          className="inline-flex items-center gap-1 text-slate-500 hover:text-brand-600"
        >
          Source registry →
        </Link>
        {entry.contractsUrl && (
          <a
            href={entry.contractsUrl}
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

function StatusBadge({ status }: { status: DexEntry['status'] }) {
  const cls =
    status === 'live'
      ? 'bg-up-soft text-up-strong'
      : status === 'experimental'
        ? 'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-200'
        : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400';
  const label = status === 'native' ? 'Stellar native' : status;
  return (
    <span
      className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wider ${cls}`}
    >
      {label}
    </span>
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

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wider text-slate-500">
        {label}
      </div>
      <div className="mt-0.5 font-mono text-sm font-semibold tabular-nums text-slate-900 dark:text-slate-100">
        {value}
      </div>
    </div>
  );
}
