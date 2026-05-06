import type { Metadata } from 'next';
import Link from 'next/link';
import { Suspense } from 'react';

import { Panel } from '@/components/reveal';
import { asExample, API_BASE_URL } from '@/api/client';
import { formatCompact, formatPrice } from '@/lib/format';
import { CoinTabs, ActiveTabSlot } from './CoinTabs';
import { ChartPanel } from './ChartPanel';
import { IssuerPanel } from './IssuerPanel';
import { MarketsTabPanel } from './MarketsTabPanel';
import { HistoryTabPanel } from './HistoryTabPanel';
import { SupplyTabPanel } from './SupplyTabPanel';

/**
 * /coins/[slug] — single asset detail page.
 *
 * Server component fetches every panel's data from the live API
 * at request time. There is no seed / synthesised content;
 * fields the API doesn't yet expose render as `—` rather than
 * a fabricated value.
 */
export async function generateStaticParams() {
  // Build-time fetch from the live API so every asset returned
  // by /v1/coins (capped at 500 per page today) gets a
  // pre-rendered route. CI builds without network connectivity
  // fall back to a single canonical slug so static export has
  // something to anchor on — Next.js refuses to build a dynamic
  // route under output:'export' with zero params.
  const fallback = [{ slug: 'native' }];
  try {
    const res = await fetch(`${API_BASE_URL}/v1/coins?limit=500`, {
      signal: AbortSignal.timeout(2_000),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: { coins: { slug: string }[] } };
    const slugs = (env.data?.coins ?? []).map((c) => c.slug);
    return slugs.length > 0 ? slugs.map((slug) => ({ slug })) : fallback;
  } catch {
    return fallback;
  }
}

type Params = Promise<{ slug: string }>;

interface CoinSummary {
  asset_id: string;
  code: string;
  slug: string;
  issuer: string;
  observation_count: number;
  first_seen_ledger: number;
  last_seen_ledger: number;
}

interface AssetDetail {
  asset_id: string;
  code: string;
  issuer?: string;
  type?: string;
  home_domain?: string;
  total_supply?: string;
  circulating_supply?: string;
  market_cap_usd?: string;
  fdv_usd?: string;
  volume_24h_usd?: string;
  supply_basis?: string;
}

interface PriceResp {
  price?: string;
  quote?: string;
  age_seconds?: number;
  flags?: { stale?: boolean; triangulated?: boolean };
}

// Static export hits every page once at build time. CI's stub
// hostname doesn't resolve, and Node's DNS retry budget swallows
// the AbortSignal — bypass network entirely when the URL looks
// like a CI placeholder.
const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

async function fetchCoin(slug: string): Promise<CoinSummary | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(`${API_BASE_URL}/v1/coins?limit=500`, {
      signal: AbortSignal.timeout(2_000),
    });
    if (!res.ok) return null;
    const env = (await res.json()) as { data: { coins: CoinSummary[] } };
    return env.data?.coins?.find((c) => c.slug === slug) ?? null;
  } catch {
    return null;
  }
}

async function fetchAssetDetail(assetId: string): Promise<AssetDetail | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/assets/${encodeURIComponent(assetId)}`,
      { signal: AbortSignal.timeout(2_000) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: AssetDetail };
    return env.data ?? null;
  } catch {
    return null;
  }
}

async function fetchPrice(assetId: string): Promise<PriceResp | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/price?asset=${encodeURIComponent(assetId)}&quote=fiat:USD`,
      { signal: AbortSignal.timeout(2_000) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: PriceResp };
    return env.data ?? null;
  } catch {
    return null;
  }
}

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { slug } = await params;
  const coin = await fetchCoin(slug);
  const code = coin?.code ?? slug;
  return {
    title: `${code} — Stellar asset`,
    description: `Live price, markets, and issuer detail for ${code} on Stellar — VWAP'd across on-chain DEXes, classic SDEX, and major exchanges.`,
    openGraph: {
      title: `${code} — Stellar asset`,
      description: `Live price, markets, and issuer detail for ${code} on Stellar.`,
      url: `/coins/${slug}`,
      type: 'website',
    },
  };
}

export default async function CoinDetailPage({ params }: { params: Params }) {
  const { slug } = await params;
  const coin = await fetchCoin(slug);

  if (!coin) {
    return (
      <div className="mx-auto max-w-6xl space-y-6 p-6">
        <header className="space-y-3">
          <nav className="text-xs text-slate-500">
            <Link href="/assets" className="hover:text-brand-600">
              Assets
            </Link>{' '}
            / <span>{slug}</span>
          </nav>
          <h1 className="text-3xl font-semibold tracking-tight">
            {slug}
          </h1>
        </header>
        <Panel title="Asset not found" bodyClassName="text-sm text-slate-600 dark:text-slate-400">
          <p>
            The slug{' '}
            <code className="rounded bg-slate-100 px-1 font-mono text-xs dark:bg-slate-800">
              {slug}
            </code>{' '}
            doesn&apos;t match any asset the indexer has observed yet. Asset
            slugs are derived from canonical asset IDs (e.g.{' '}
            <code className="font-mono">native</code>,{' '}
            <code className="font-mono">USDC-GA5Z…</code>); a typo or a
            never-traded asset both end up here.
          </p>
        </Panel>
      </div>
    );
  }

  const [detail, price] = await Promise.all([
    fetchAssetDetail(coin.asset_id),
    fetchPrice(coin.asset_id),
  ]);

  return (
    <div className="mx-auto max-w-6xl space-y-6 p-6">
      <header className="space-y-3">
        <nav className="text-xs text-slate-500">
          <Link href="/assets" className="hover:text-brand-600">
            Assets
          </Link>{' '}
          /{' '}
          <span className="text-slate-700 dark:text-slate-300">
            {coin.code}
          </span>
        </nav>
        <div className="flex flex-wrap items-baseline gap-4">
          <h1 className="text-3xl font-semibold tracking-tight">
            {coin.code}
          </h1>
          {detail?.type && (
            <span
              className="rounded-md bg-slate-100 px-2 py-0.5 text-[11px] uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-300"
              title="Asset type"
            >
              {detail.type}
            </span>
          )}
        </div>
        {detail?.home_domain && (
          <p className="text-sm text-slate-600 dark:text-slate-400">
            Issuer home domain:{' '}
            <code className="font-mono">{detail.home_domain}</code>
          </p>
        )}
      </header>

      <Suspense fallback={null}>
        <CoinTabs slug={coin.slug} hasIssuer={!!coin.issuer} />
      </Suspense>

      <Suspense fallback={null}>
        <ActiveTabSlot
          overview={
            <OverviewBody coin={coin} detail={detail} price={price} />
          }
          chart={<ChartPanel slug={coin.slug} startPrice={parsePrice(price?.price) ?? 0.01} />}
          markets={<MarketsTabPanel slug={coin.slug} />}
          history={<HistoryTabPanel slug={coin.slug} />}
          supply={<SupplyTabPanel slug={coin.slug} />}
          issuer={
            coin.issuer ? <IssuerPanel gStrkey={coin.issuer} /> : undefined
          }
        />
      </Suspense>
    </div>
  );
}

function OverviewBody({
  coin,
  detail,
  price,
}: {
  coin: CoinSummary;
  detail: AssetDetail | null;
  price: PriceResp | null;
}) {
  const priceNum = parsePrice(price?.price);
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Panel
          title="Price"
          source={asExample('/v1/price', { asset: coin.asset_id, quote: 'fiat:USD' })}
          panelId="price-card"
          className="lg:col-span-2"
          bodyClassName="space-y-4"
        >
          <div className="flex flex-wrap items-baseline gap-4">
            <span className="font-mono text-3xl tabular-nums">
              {priceNum != null ? `$${formatPrice(priceNum)}` : '—'}
            </span>
            {price?.flags?.stale && (
              <span className="rounded bg-amber-100 px-2 py-0.5 text-[11px] uppercase tracking-wider text-amber-800 dark:bg-amber-900/40 dark:text-amber-200">
                Stale
              </span>
            )}
            {price?.flags?.triangulated && (
              <span className="rounded bg-sky-100 px-2 py-0.5 text-[11px] uppercase tracking-wider text-sky-800 dark:bg-sky-900/40 dark:text-sky-200">
                Triangulated
              </span>
            )}
          </div>
          <p className="text-xs text-slate-500">
            % change windows pending — the aggregator emits closed-bucket VWAPs
            but the per-window deltas aren&apos;t served by{' '}
            <code className="font-mono">/v1/price</code> yet.
          </p>
        </Panel>

        <Panel title="Observations" panelId="obs-card">
          <dl className="grid grid-cols-2 gap-2 text-sm">
            <Stat
              label="Total"
              value={formatCompact(coin.observation_count)}
            />
            <Stat
              label="First seen"
              mono
              value={`#${coin.first_seen_ledger.toLocaleString()}`}
            />
            <Stat
              label="Last seen"
              mono
              value={`#${coin.last_seen_ledger.toLocaleString()}`}
            />
          </dl>
        </Panel>
      </div>

      <Panel
        title="Supply + market cap"
        source={asExample('/v1/assets/{asset_id}', { asset_id: coin.asset_id })}
      >
        <dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
          <Stat
            label="Volume 24h"
            value={fmtUsd(detail?.volume_24h_usd)}
          />
          <Stat
            label="Market cap"
            value={fmtUsd(detail?.market_cap_usd)}
          />
          <Stat
            label="Circulating"
            value={fmtNum(detail?.circulating_supply)}
          />
          <Stat label="Total" value={fmtNum(detail?.total_supply)} />
          <Stat label="FDV" value={fmtUsd(detail?.fdv_usd)} />
          <Stat
            label="Supply basis"
            value={detail?.supply_basis ?? '—'}
          />
        </dl>
        {!detail && (
          <p className="mt-3 text-xs text-slate-500">
            Asset detail endpoint returned no data — newly observed asset, or
            the F2 fields haven&apos;t been computed for this issuer yet.
          </p>
        )}
      </Panel>

      {coin.issuer && (
        <Panel
          title="Issuer"
          source={asExample(`/v1/issuers/${coin.issuer}`)}
        >
          <dl className="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2">
            <Stat
              label="G-strkey"
              mono
              value={`${coin.issuer.slice(0, 12)}…${coin.issuer.slice(-6)}`}
            />
            {detail?.home_domain && (
              <Stat label="Home domain" mono value={detail.home_domain} />
            )}
          </dl>
        </Panel>
      )}
    </div>
  );
}

function parsePrice(raw: string | undefined): number | null {
  if (!raw) return null;
  const n = Number(raw);
  return Number.isFinite(n) ? n : null;
}

function fmtUsd(raw: string | undefined): string {
  if (!raw) return '—';
  const n = Number(raw);
  if (!Number.isFinite(n)) return '—';
  return `$${formatCompact(n)}`;
}

function fmtNum(raw: string | undefined): string {
  if (!raw) return '—';
  const n = Number(raw);
  if (!Number.isFinite(n)) return '—';
  return formatCompact(n);
}

function Stat({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <dt className="text-[11px] uppercase tracking-wider text-slate-500">
        {label}
      </dt>
      <dd className={mono ? 'font-mono text-xs' : 'tabular-nums'}>{value}</dd>
    </div>
  );
}
