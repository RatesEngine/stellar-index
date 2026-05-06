import type { Metadata } from 'next';
import Link from 'next/link';
import { Suspense } from 'react';

import { Panel } from '@/components/reveal';
import { asExample, API_BASE_URL } from '@/api/client';
import { formatCompact, formatPrice } from '@/lib/format';
import { AssetTabs, ActiveTabSlot } from './AssetTabs';
import { ChartPanel } from './ChartPanel';
import { IssuerPanel } from './IssuerPanel';
import { MarketsTabPanel } from './MarketsTabPanel';
import { HistoryTabPanel } from './HistoryTabPanel';
import { SupplyTabPanel } from './SupplyTabPanel';

/**
 * /assets/[slug] — single asset detail page.
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
    // 10s here — generateStaticParams is a one-shot at build
    // time; the 500-asset listing query is heavier than a single
    // /v1/coins/{slug} hit so allow more headroom.
    const res = await fetch(`${API_BASE_URL}/v1/coins?limit=500`, {
      signal: AbortSignal.timeout(10_000),
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
  // Optional per-row metrics from /v1/coins/{slug} — null when
  // the asset has no off-chain peg / supply snapshot yet.
  price_usd?: string | null;
  volume_24h_usd?: string | null;
  market_cap_usd?: string | null;
  circulating_supply?: string | null;
  change_24h_pct?: string | null;
  // Top 5 markets the asset participates in (as base or
  // quote), ordered by 24h USD volume desc. Empty array
  // when the asset has no recent trades.
  top_markets?: TopMarket[];
}

interface TopMarket {
  counterparty: string;
  side: 'base' | 'quote';
  volume_24h_usd?: string | null;
  trade_count_24h: number;
}

interface AssetDetail {
  asset_id: string;
  code: string;
  issuer?: string;
  type?: string;
  home_domain?: string;
  total_supply?: string;
  circulating_supply?: string;
  max_supply?: string;
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

// 8s per fetch. The previous 2s was too aggressive — every detail
// page was rendering "Asset not found" because the build's first
// /v1/coins/{slug} hit (cold connection pool) timed out, fetchCoin
// returned null, and the page render branched into the not-found
// state. With ~500 pages × 4 fetches × 8s worst case = ~30 min,
// well within CF Pages's 20-min build window per page (parallelised),
// and in practice the API responds in <300ms steady-state.
const BUILD_FETCH_TIMEOUT_MS = 8_000;

async function fetchCoin(slug: string): Promise<CoinSummary | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/coins/${encodeURIComponent(slug)}`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: CoinSummary };
    return env.data ?? null;
  } catch {
    return null;
  }
}

async function fetchAssetDetail(assetId: string): Promise<AssetDetail | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/assets/${encodeURIComponent(assetId)}`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: AssetDetail };
    return env.data ?? null;
  } catch {
    return null;
  }
}

async function fetchPriceDirect(
  asset: string,
  quote: string,
): Promise<PriceResp | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/price?asset=${encodeURIComponent(asset)}&quote=${encodeURIComponent(quote)}`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: PriceResp };
    return env.data ?? null;
  } catch {
    return null;
  }
}

// fetchPrice tries direct asset→USD first; on 404 it triangulates
// via XLM. Many active classic Stellar assets only trade against
// XLM (or stablecoins) on SDEX, so the aggregator's per-pair USD
// VWAP doesn't exist. The client-side compose lets the page show
// a real USD price anyway (asset/XLM × XLM/USD), tagged as
// triangulated so the user can see the provenance.
//
// XLM (asset_id "native") short-circuits — its direct USD VWAP
// is the canonical answer.
async function fetchPrice(assetId: string): Promise<PriceResp | null> {
  if (assetId === 'native') {
    return fetchPriceDirect('native', 'fiat:USD');
  }
  const direct = await fetchPriceDirect(assetId, 'fiat:USD');
  if (direct?.price) return direct;
  // Triangulate via XLM.
  const [vsXlm, xlmUsd] = await Promise.all([
    fetchPriceDirect(assetId, 'native'),
    fetchPriceDirect('native', 'fiat:USD'),
  ]);
  if (!vsXlm?.price || !xlmUsd?.price) return null;
  const a = Number(vsXlm.price);
  const b = Number(xlmUsd.price);
  if (!Number.isFinite(a) || !Number.isFinite(b) || a <= 0 || b <= 0) {
    return null;
  }
  const triangulated = (a * b).toFixed(12);
  return {
    price: triangulated,
    quote: 'fiat:USD',
    age_seconds: Math.max(
      vsXlm.age_seconds ?? 0,
      xlmUsd.age_seconds ?? 0,
    ),
    flags: { triangulated: true },
  };
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
      url: `/assets/${slug}`,
      type: 'website',
    },
  };
}

export default async function AssetDetailPage({ params }: { params: Params }) {
  const { slug } = await params;
  const coin = await fetchCoin(slug);

  if (!coin) {
    return (
      <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
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
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
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
        <AssetTabs slug={coin.slug} hasIssuer={!!coin.issuer} />
      </Suspense>

      <Suspense fallback={null}>
        <ActiveTabSlot
          overview={
            <OverviewBody coin={coin} detail={detail} price={price} />
          }
          chart={<ChartPanel assetID={coin.asset_id} />}
          markets={<MarketsTabPanel assetID={coin.asset_id} />}
          history={<HistoryTabPanel assetID={coin.asset_id} />}
          supply={<SupplyTabPanel assetID={coin.asset_id} />}
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
  const hasSupply =
    detail?.circulating_supply != null ||
    detail?.total_supply != null ||
    detail?.max_supply != null ||
    detail?.market_cap_usd != null ||
    detail?.fdv_usd != null;
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
                Triangulated via XLM
              </span>
            )}
          </div>
          <dl className="grid grid-cols-2 gap-3 border-t border-slate-200 pt-3 text-sm dark:border-slate-800 sm:grid-cols-3">
            <Stat
              label="Volume 24h"
              value={fmtUsd(detail?.volume_24h_usd ?? coin.volume_24h_usd)}
            />
            <Stat
              label="Market cap"
              value={fmtUsd(detail?.market_cap_usd ?? coin.market_cap_usd)}
            />
            <Stat
              label="Circulating"
              value={fmtNum(detail?.circulating_supply ?? coin.circulating_supply)}
            />
          </dl>
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

      {coin.top_markets && coin.top_markets.length > 0 && (
        <Panel
          title="Top markets"
          hint={`${coin.top_markets.length} most active by 24h volume`}
          source={asExample('/v1/coins/{slug}', { slug: coin.slug })}
          bodyClassName="-mx-4"
        >
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
                  <th className="px-4 py-2 font-medium">Side</th>
                  <th className="px-4 py-2 font-medium">vs</th>
                  <th className="px-4 py-2 text-right font-medium">24h volume</th>
                  <th className="px-4 py-2 text-right font-medium">24h trades</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                {coin.top_markets.map((m) => (
                  <tr
                    key={`${m.side}|${m.counterparty}`}
                    className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
                  >
                    <td className="px-4 py-3">
                      <span className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-300">
                        {m.side}
                      </span>
                    </td>
                    <td className="px-4 py-3 font-mono text-xs">
                      {shortCounterparty(m.counterparty)}
                    </td>
                    <td className="px-4 py-3 text-right">
                      {m.volume_24h_usd ? (
                        <span className="font-mono tabular-nums">
                          ${fmtCompact(Number(m.volume_24h_usd))}
                        </span>
                      ) : (
                        <span className="text-slate-300 dark:text-slate-700">—</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-right font-mono tabular-nums text-slate-500">
                      {fmtCompact(m.trade_count_24h)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Panel>
      )}

      {hasSupply && (
        <Panel
          title="Supply"
          hint="From /v1/assets — circulating / total / max where the supply pipeline has computed them."
          source={asExample('/v1/assets/{asset_id}', { asset_id: coin.asset_id })}
        >
          <dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
            {detail?.circulating_supply != null && (
              <Stat
                label="Circulating"
                value={fmtNum(detail.circulating_supply)}
              />
            )}
            {detail?.total_supply != null && (
              <Stat label="Total" value={fmtNum(detail.total_supply)} />
            )}
            {detail?.max_supply != null && (
              <Stat label="Max" value={fmtNum(detail.max_supply)} />
            )}
            {detail?.fdv_usd != null && (
              <Stat label="FDV" value={fmtUsd(detail.fdv_usd)} />
            )}
            {detail?.supply_basis && (
              <Stat label="Supply basis" value={detail.supply_basis} />
            )}
          </dl>
        </Panel>
      )}
      {coin.issuer && (
        <Panel
          title="Issuer"
          source={asExample(`/v1/issuers/${coin.issuer}`)}
        >
          <dl className="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2">
            <div>
              <dt className="text-[11px] uppercase tracking-wider text-slate-500">
                G-strkey
              </dt>
              <dd className="font-mono text-xs">
                <Link
                  href={`/issuers/${coin.issuer}`}
                  className="hover:text-brand-600"
                  title={coin.issuer}
                >
                  {`${coin.issuer.slice(0, 12)}…${coin.issuer.slice(-6)}`}
                </Link>
              </dd>
            </div>
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

function fmtUsd(raw: string | null | undefined): string {
  if (!raw) return '—';
  const n = Number(raw);
  if (!Number.isFinite(n)) return '—';
  return `$${formatCompact(n)}`;
}

function fmtNum(raw: string | null | undefined): string {
  if (!raw) return '—';
  const n = Number(raw);
  if (!Number.isFinite(n)) return '—';
  return formatCompact(n);
}

// fmtCompact wraps formatCompact for inline use in table cells —
// the Markets preview uses it for both USD volume and the trade
// count column.
function fmtCompact(n: number): string {
  return formatCompact(n);
}

// shortCounterparty renders a counterparty asset_id compactly:
// `<code>-<short-issuer>` for classic, `crypto:<sym>` straight,
// numeric (XLM trustline form) → "XLM", `fiat:USD` → "USD".
function shortCounterparty(canonical: string): string {
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.replace('fiat:', '');
  if (canonical.startsWith('crypto:')) return canonical;
  if (/^\d+$/.test(canonical)) return 'XLM';
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return canonical;
  const code = canonical.slice(0, dashIx);
  const issuer = canonical.slice(dashIx + 1);
  return `${code} (${issuer.slice(0, 6)}…${issuer.slice(-4)})`;
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
