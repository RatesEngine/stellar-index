import type { Metadata } from 'next';
import Link from 'next/link';
import { ExternalLink, ArrowLeft } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { SITE_OG_IMAGES, SITE_TWITTER_IMAGES, serializeJsonLd } from '@/lib/seo';

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.stellarindex.io';

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

// Curated annotations for every Blend mainnet contract we track.
// Mirrors the BLEND_POOL_META map in LendingPoolsTable; kept in
// sync so the detail route renders identical context whether the
// user arrived via the listing or a deep link.
// Sourced from docs/operations/wasm-audits/blend.md (Phase 4 walk).
const BLEND_POOL_LABELS: Record<
  string,
  { name: string; note?: string; deployedAt?: string; initiator?: string }
> = {
  CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7: {
    name: 'Backstop V2',
    note: 'Holds the protocol-wide BLND-USDC LP shares that backstop pool insolvency. Receives auction proceeds when borrower positions liquidate at a loss.',
    deployedAt: '2025-04-14',
    initiator: 'GAX2VVWVHU5YQY5J3NJBXKHI3FFKZN54BE6GRJCWSIKSBZTQWJJNJMPC',
  },
  CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU: {
    name: 'Pool Factory V2',
    note: 'Spawns new isolated lending-pool contracts. Each user-facing pool (with its own reserves and risk parameters) is a child of this factory.',
    deployedAt: '2025-04-14',
    initiator: 'GAX2VVWVHU5YQY5J3NJBXKHI3FFKZN54BE6GRJCWSIKSBZTQWJJNJMPC',
  },
  CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD: {
    name: 'Pool #1 (genesis)',
    note: 'First pool deployed by the Pool Factory V2, ~4 minutes after the factory itself. Initiator overlaps with the protocol team — likely a reference/genesis pool.',
    deployedAt: '2025-04-14',
    initiator: 'GAX2VVWVHU5YQY5J3NJBXKHI3FFKZN54BE6GRJCWSIKSBZTQWJJNJMPC',
  },
  CBNR7PYFY775UG7W37B4OJG2OBBUKLFW6VIBHFDKKLR2HECPRMRZMDK3: {
    name: 'Pool #2',
    deployedAt: '2025-04-15',
    initiator: 'GBCAS7XIGDRZY4BMABJMGGW7J3YTITRRV5BTEMFQE5ZZSSVWHHX2ZSS4',
  },
  CCCCIQSDILITHMM7PBSLVDT5MISSY7R26MNZXCX4H7J5JQ5FPIYOGYFS: {
    name: 'Pool #3',
    deployedAt: '2025-04-17',
    initiator: 'GBCAS7XIGDRZY4BMABJMGGW7J3YTITRRV5BTEMFQE5ZZSSVWHHX2ZSS4',
  },
  CB4OFHAY2TAEYUVPOJS36S657C6NYMSIFUNCCA5AHYT46Y5XUID3O2ED: {
    name: 'Pool #4',
    deployedAt: '2025-05-01',
    initiator: 'GBIWJGAOSFC4KUPHXM573TKTWHMI7VW7D4GCHYZYH243Q6HVBV7ORBIT',
  },
  CAE7QVOMBLZ53CDRGK3UNRRHG5EZ5NQA7HHTFASEMYBWHG6MDFZTYHXC: {
    name: 'Pool #5',
    deployedAt: '2025-05-01',
    initiator: 'GBIWJGAOSFC4KUPHXM573TKTWHMI7VW7D4GCHYZYH243Q6HVBV7ORBIT',
  },
  CBYOBT7ZCCLQCBUYYIABZLSEGDPEUWXCUXQTZYOG3YBDR7U357D5ZIRF: {
    name: 'Pool #6',
    deployedAt: '2025-07-13',
    initiator: 'GCCI7K6QU6FVVIXWSLKRPTBKJCFBLEJKPTZMP27A2KL37N4ZL3OCM3GI',
  },
  CALRF5I2OCJCU577R6MZBCY5IIXNMAAG6PNMN7GUKEYIXBJCJN2FJRVI: {
    name: 'Pool #7',
    deployedAt: '2025-11-22',
    initiator: 'GDH3FRHOOWXYXEASH43N2VOVFOPJSVJF3EQFSLBLJYFPHOUAF4N4AETH',
  },
  CADR6Q2UOCDJAGXMAB2E6SRT35STLZ2IGLZUCXJQG7TC2LNKCU5RTQVY: {
    name: 'Pool #8',
    deployedAt: '2025-11-25',
    initiator: 'GDH3FRHOOWXYXEASH43N2VOVFOPJSVJF3EQFSLBLJYFPHOUAF4N4AETH',
  },
  CDMAVJPFXPADND3YRL4BSM3AKZWCTFMX27GLLXCML3PD62HEQS5FPVAI: {
    name: 'Pool #9',
    deployedAt: '2025-11-25',
    initiator: 'GDH3FRHOOWXYXEASH43N2VOVFOPJSVJF3EQFSLBLJYFPHOUAF4N4AETH',
  },
};

export async function generateStaticParams() {
  // Curated well-known factory contracts that don't emit auctions
  // and so don't show up in /v1/lending/pools — but operators and
  // users still deep-link to them. Keep these in the static-params
  // list so the routes pre-render even when the auction-stream
  // listing is empty.
  const curatedKeys = Object.keys(BLEND_POOL_LABELS).map((pool) => ({ pool }));
  if (isCIStub) return curatedKeys;
  try {
    const res = await fetch(`${API_BASE_URL}/v1/lending/pools`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: LendingPool[] };
    const fromAPI = (env.data ?? []).map((p) => ({ pool: p.pool })).filter((p) => p.pool);
    const seen = new Set<string>();
    const merged = [...fromAPI, ...curatedKeys].filter((p) => {
      if (seen.has(p.pool)) return false;
      seen.add(p.pool);
      return true;
    });
    return merged.length > 0 ? merged : curatedKeys;
  } catch {
    return curatedKeys;
  }
}

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { pool } = await params;
  const label = BLEND_POOL_LABELS[pool]?.name ?? `${pool.slice(0, 6)}…${pool.slice(-6)}`;
  const canonical = `https://stellarindex.io/lending/${pool}`;
  const title = `${label} — Blend lending pool`;
  const description = `Auction activity, user count, and contract metadata for the Blend pool at ${pool}.`;
  return {
    title,
    description,
    alternates: { canonical },
    openGraph: { title, description, url: canonical, type: 'website', images: SITE_OG_IMAGES },
    twitter: { card: 'summary_large_image', title, description, images: SITE_TWITTER_IMAGES },
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

  // Schema.org BreadcrumbList — Home → Lending → <pool name or short hash>.
  const poolName = label?.name || `${pool.slice(0, 8)}…${pool.slice(-8)}`;
  const breadcrumbLD = {
    '@context': 'https://schema.org',
    '@type': 'BreadcrumbList',
    itemListElement: [
      { '@type': 'ListItem', position: 1, name: 'Home', item: 'https://stellarindex.io' },
      { '@type': 'ListItem', position: 2, name: 'Lending', item: 'https://stellarindex.io/lending' },
      { '@type': 'ListItem', position: 3, name: poolName, item: `https://stellarindex.io/lending/${pool}` },
    ],
  };

  return (
    <div className="mx-auto max-w-5xl space-y-6 px-6 py-8">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: serializeJsonLd(breadcrumbLD) }}
      />
      <Link
        href="/lending"
        className="inline-flex items-center gap-1 text-sm text-ink-muted hover:text-brand-600"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        All lending pools
      </Link>

      <header className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <span className="rounded bg-up-subtle px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-wider text-up-strong">
            Blend
          </span>
          {label && (
            <span className="rounded bg-brand-100 px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-wider text-brand-800">
              {label.name}
            </span>
          )}
          {label?.deployedAt && (
            <span className="rounded bg-surface-subtle px-1.5 py-0.5 text-[11px] font-mono text-ink-body">
              deployed {label.deployedAt}
            </span>
          )}
        </div>
        <h1 className="break-all font-mono text-2xl tracking-tight">
          {pool.slice(0, 8)}…{pool.slice(-8)}
        </h1>
        <p className="break-all font-mono text-xs text-ink-muted">{pool}</p>
        {label?.initiator && (
          <p className="font-mono text-[11px] text-ink-muted">
            Deployed by{' '}
            <a
              href={`https://stellar.expert/explorer/public/account/${label.initiator}`}
              target="_blank"
              rel="noreferrer noopener"
              className="text-brand-600 hover:underline"
              title={label.initiator}
            >
              {label.initiator.slice(0, 6)}…{label.initiator.slice(-4)}
            </a>
          </p>
        )}
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
            className="inline-flex items-center gap-1 text-ink-muted hover:underline"
          >
            blend.capital
            <ExternalLink className="h-3 w-3" />
          </a>
        </div>
      </header>

      {label?.note && (
        <Panel title="About this contract">
          <p className="text-sm leading-relaxed text-ink-body">
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
            <div className="text-ink-body">
              Most recent auction event:{' '}
              <span className="font-mono text-ink">
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
        <p className="text-sm text-ink-body">
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
    <div className="rounded-xl border border-line bg-surface p-4 shadow-sm">
      <div className="text-[10px] uppercase tracking-wider text-ink-muted">
        {label}
      </div>
      <div className="mt-1 font-mono text-2xl tabular-nums text-ink">
        {value.toLocaleString()}
      </div>
    </div>
  );
}
