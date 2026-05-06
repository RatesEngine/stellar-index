import type { Metadata } from 'next';
import Link from 'next/link';
import { ExternalLink } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';

export const metadata: Metadata = {
  title: 'DEXes — AMMs and order books on Stellar',
  description:
    'Every Stellar DEX we ingest — Soroswap, Phoenix, Aquarius, SDEX, Comet — with the on-chain quirks each presented during integration.',
};

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
      'Reserves are i128 — handled per ADR-0003 (no int64 truncation).',
      'Pair registry persisted in postgres so live ingest + parallel backfill chunks share one source of truth.',
    ],
    contractsUrl: 'https://github.com/soroswap/core',
    discoveryDoc:
      '/research',
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
    discoveryDoc:
      '/research',
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
    discoveryDoc:
      '/research',
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
    discoveryDoc:
      '/research',
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
    discoveryDoc:
      '/research',
  },
];

export default function DexesPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">DEXes</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          AMMs and the native order book — every venue we ingest from
          on-chain Stellar. Each card lists the integration quirk
          discovered during decoder development; full audit notes live
          in the linked discovery doc.
        </p>
      </header>

      <Panel
        title="Source registry"
        hint="Live row from /v1/sources for any of these"
        source={asExample('/v1/sources', { class: 'exchange' })}
        bodyClassName="text-sm text-slate-600 dark:text-slate-400"
      >
        Every venue here has{' '}
        <code className="font-mono text-xs">class=exchange</code> in
        the source registry — meaning trades from these venues
        contribute to the canonical VWAP by default. See{' '}
        <Link href="/sources" className="underline decoration-dotted">
          /sources
        </Link>{' '}
        for the full include-in-VWAP / backfill-safety / weight matrix.
      </Panel>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {DEXES.map((d) => (
          <DexCard key={d.source} entry={d} />
        ))}
      </div>
    </div>
  );
}

function DexCard({ entry }: { entry: DexEntry }) {
  return (
    <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-800 dark:bg-slate-900">
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-lg font-semibold tracking-tight">{entry.name}</h2>
        <StatusBadge status={entry.status} />
      </div>
      <p className="mt-1 text-xs uppercase tracking-wider text-slate-500">
        {entry.type}
      </p>
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
        <a
          href={entry.discoveryDoc}
          className="inline-flex items-center gap-1 text-brand-600 hover:underline"
          target="_blank"
          rel="noreferrer"
        >
          Discovery notes
          <ExternalLink className="h-3 w-3" />
        </a>
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
