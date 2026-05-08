import type { Metadata } from 'next';
import Link from 'next/link';
import { ExternalLink, ArrowLeft } from 'lucide-react';

import { Panel } from '@/components/reveal';

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net';

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

const BUILD_FETCH_TIMEOUT_MS = 8_000;

type Params = Promise<{ pool: string }>;

interface LendingPool {
  protocol: string;
  pool: string;
  auctions_24h: number;
  auctions_total: number;
  unique_users_30d: number;
  last_seen: string;
}

// Curated annotations for well-known Blend mainnet contracts.
// Same map as LendingPoolsTable — kept here too so the detail
// route can render the label even when arrived-at via deep link.
// Sourced from docs/operations/wasm-audits/blend.md.
const BLEND_POOL_LABELS: Record<string, { name: string; note?: string }> = {
  CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7: {
    name: 'Backstop V2',
    note: 'Holds the protocol-wide BLND-USDC LP shares that backstop pool insolvency. Receives auction proceeds when borrower positions liquidate at a loss.',
  },
  CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU: {
    name: 'Pool Factory V2',
    note: 'Spawns new isolated lending-pool contracts. Each user-facing pool (with its own reserves and risk parameters) is a child of this factory.',
  },
};

export async function generateStaticParams() {
  const fallback = [{ pool: 'CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7' }];
  if (isCIStub) return fallback;
  try {
    const res = await fetch(`${API_BASE_URL}/v1/lending/pools`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: LendingPool[] };
    const pools = (env.data ?? []).map((p) => ({ pool: p.pool })).filter(p => p.pool);
    return pools.length > 0 ? pools : fallback;
  } catch {
    return fallback;
  }
}

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { pool } = await params;
  const label = BLEND_POOL_LABELS[pool]?.name ?? `${pool.slice(0, 6)}…${pool.slice(-6)}`;
  return {
    title: `${label} — Blend lending pool`,
    description: `Auction activity, user count, and contract metadata for the Blend pool at ${pool}.`,
  };
}

async function fetchPool(pool: string): Promise<LendingPool | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(`${API_BASE_URL}/v1/lending/pools`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) return null;
    const env = (await res.json()) as { data: LendingPool[] };
    return (env.data ?? []).find((p) => p.pool === pool) ?? null;
  } catch {
    return null;
  }
}

export default async function LendingPoolPage({ params }: { params: Params }) {
  const { pool } = await params;
  const data = await fetchPool(pool);
  const label = BLEND_POOL_LABELS[pool];

  return (
    <div className="mx-auto max-w-5xl space-y-6 px-6 py-8">
      <Link
        href="/lending"
        className="inline-flex items-center gap-1 text-sm text-slate-500 hover:text-brand-600"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        All lending pools
      </Link>

      <header className="space-y-2">
        <div className="flex items-center gap-2">
          <span className="rounded bg-emerald-100 px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-wider text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200">
            Blend
          </span>
          {label && (
            <span className="rounded bg-brand-100 px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-wider text-brand-800 dark:bg-brand-900/40 dark:text-brand-200">
              {label.name}
            </span>
          )}
        </div>
        <h1 className="break-all font-mono text-2xl tracking-tight">
          {pool.slice(0, 8)}…{pool.slice(-8)}
        </h1>
        <p className="break-all font-mono text-xs text-slate-500">{pool}</p>
        <div className="flex flex-wrap gap-3 pt-1 text-xs">
          <a
            href={`https://stellar.expert/explorer/public/contract/${pool}`}
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex items-center gap-1 text-brand-600 hover:underline"
          >
            View on stellar.expert
            <ExternalLink className="h-3 w-3" />
          </a>
          <a
            href="https://blend.capital"
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex items-center gap-1 text-slate-500 hover:underline"
          >
            blend.capital
            <ExternalLink className="h-3 w-3" />
          </a>
        </div>
      </header>

      {label?.note && (
        <Panel title="About this contract">
          <p className="text-sm leading-relaxed text-slate-700 dark:text-slate-300">
            {label.note}
          </p>
        </Panel>
      )}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Stat label="Auctions (24h)" value={data?.auctions_24h ?? 0} />
        <Stat label="Auctions (total)" value={data?.auctions_total ?? 0} />
        <Stat label="Unique users (30d)" value={data?.unique_users_30d ?? 0} />
      </div>

      {data && (
        <Panel title="Last activity">
          <div className="space-y-1 text-sm">
            <div className="text-slate-600 dark:text-slate-400">
              Most recent auction event:{' '}
              <span className="font-mono text-slate-800 dark:text-slate-200">
                {new Date(data.last_seen).toUTCString()}
              </span>
            </div>
          </div>
        </Panel>
      )}

      <Panel
        title="Reserve composition"
        hint="Per-asset supply / borrow APY — pending Soroban storage reader (#84)"
      >
        <p className="text-sm text-slate-600 dark:text-slate-400">
          Today this view tracks pools by their <em>auction</em> stream — every
          time a borrower position liquidates and the pool emits an auction
          event, we count it here. The per-reserve breakdown (which assets
          this pool accepts as collateral, current supply/borrow APYs, total
          deposited, total borrowed) needs a Blend-pool-storage reader that
          queries each pool contract&apos;s persistent storage. That reader is
          on the roadmap; until it lands the pool address is the canonical
          identifier and stellar.expert has the raw on-chain state.
        </p>
      </Panel>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm dark:border-slate-800 dark:bg-slate-900">
      <div className="text-[10px] uppercase tracking-wider text-slate-500">
        {label}
      </div>
      <div className="mt-1 font-mono text-2xl tabular-nums text-slate-900 dark:text-slate-100">
        {value.toLocaleString()}
      </div>
    </div>
  );
}
