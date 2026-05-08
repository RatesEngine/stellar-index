import type { Metadata } from 'next';
import Link from 'next/link';

import { formatCompact } from '@/lib/format';
import { PairChart } from './PairChart';

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net';

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

const BUILD_FETCH_TIMEOUT_MS = 8_000;

type Params = Promise<{ pair: string }>;

interface Market {
  base: string;
  quote: string;
  trade_count_24h: number;
  last_trade_at: string;
  volume_24h_usd?: string | null;
}

interface PriceResp {
  asset_id: string;
  quote: string;
  price?: string;
  price_type?: string;
  observed_at?: string;
  window_seconds?: number;
}

interface ChartPoint {
  t: string;
  p: string;
  v_usd?: string | null;
}

interface ChartResp {
  asset_id: string;
  quote: string;
  granularity?: string;
  timeframe?: string;
  price_type?: string;
  points: ChartPoint[];
}

interface OhlcResp {
  from: string;
  to: string;
  open: string;
  high: string;
  low: string;
  close: string;
  base_volume: string;
  quote_volume: string;
  trade_count: number;
  truncated: boolean;
}

interface HistoryTrade {
  source: string;
  ledger?: number;
  tx_hash?: string;
  op_index?: number;
  ts: string;
  base_asset: string;
  quote_asset: string;
  base_amount?: string;
  quote_amount?: string;
  price: string;
}

const PAIR_SEPARATOR = '~';

/**
 * Pair slug = `${base}${PAIR_SEPARATOR}${quote}`, URL-encoded once.
 * `~` is a URL-safe char that doesn't appear in any Stellar
 * canonical asset identifier (asset_ids use `-`, `:`, alphanum;
 * the off-chain `crypto:BTC` / `fiat:USD` shapes likewise don't
 * contain `~`). Single decodeURIComponent restores the asset_ids
 * verbatim — the backend accepts them as-is on the `asset=` and
 * `quote=` query parameters.
 */
function pairSlug(base: string, quote: string): string {
  return `${base}${PAIR_SEPARATOR}${quote}`;
}

function decodePairSlug(slug: string): { base: string; quote: string } | null {
  const decoded = decodeURIComponent(slug);
  const ix = decoded.indexOf(PAIR_SEPARATOR);
  if (ix === -1) return null;
  return { base: decoded.slice(0, ix), quote: decoded.slice(ix + 1) };
}

export async function generateStaticParams() {
  // Top 100 pairs by 24h volume — we pre-render the most interesting
  // pairs and fall back to one canonical pair (XLM/USDC) when CI's
  // stub host can't be reached.
  const fallback = [
    {
      pair: pairSlug(
        'native',
        'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
      ),
    },
  ];
  if (isCIStub) return fallback;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/markets?limit=100&order_by=volume_24h_usd_desc`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: Market[] };
    const markets = env.data ?? [];
    const out = markets
      .filter((m) => m.base && m.quote)
      .map((m) => ({ pair: pairSlug(m.base, m.quote) }));
    return out.length > 0 ? out : fallback;
  } catch {
    return fallback;
  }
}

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { pair } = await params;
  const decoded = decodePairSlug(pair);
  if (!decoded) return { title: 'Pair — Rates Engine' };
  const baseLabel = shortAsset(decoded.base);
  const quoteLabel = shortAsset(decoded.quote);
  // Best-effort price fetch so the social-share preview reads as
  // a real ticker rather than boilerplate.
  const price = await fetchPrice(decoded.base, decoded.quote);
  const priceNum = price?.price ? Number(price.price) : null;
  let suffix = '';
  if (priceNum != null && Number.isFinite(priceNum)) {
    suffix =
      priceNum >= 1
        ? ` ${priceNum.toFixed(priceNum >= 100 ? 2 : 4)}`
        : priceNum >= 0.001
          ? ` ${priceNum.toFixed(6)}`
          : ` ${priceNum.toExponential(3)}`;
  }
  const title = `${baseLabel} / ${quoteLabel}${suffix} — pair detail`;
  const description = `Live VWAP${suffix ? ` (${suffix.trim()})` : ''}, recent trades, and per-source breakdown for ${baseLabel} / ${quoteLabel} on Stellar.`;
  // Canonical URL: the URL-encoded pair slug. Without this,
  // any case- or encoding-variant of the same pair would be
  // treated as a separate page by Google.
  const canonical = `https://ratesengine.net/markets/${encodeURIComponent(pair)}`;
  return {
    title,
    description,
    alternates: { canonical },
    openGraph: {
      title,
      description,
      url: canonical,
      type: 'website',
    },
    twitter: {
      card: 'summary_large_image',
      title,
      description,
    },
  };
}

async function fetchPrice(base: string, quote: string): Promise<PriceResp | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/price?asset=${encodeURIComponent(base)}&quote=${encodeURIComponent(quote)}`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: PriceResp };
    return env.data ?? null;
  } catch {
    return null;
  }
}

async function fetchChart(base: string, quote: string): Promise<ChartResp | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/chart?asset=${encodeURIComponent(base)}&quote=${encodeURIComponent(quote)}&timeframe=24h&granularity=1h`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: ChartResp };
    return env.data ?? null;
  } catch {
    return null;
  }
}

async function fetchOhlc(base: string, quote: string): Promise<OhlcResp | null> {
  if (isCIStub) return null;
  try {
    // /v1/ohlc returns a single bar over [from, to). Default window
    // is "now - 1h" → "now" when both are absent — exactly what we
    // want for the markets-pair OHLC strip. The earlier `interval=1h`
    // param was silently ignored (not in the API schema) but the
    // default-window happened to match. Drop it for honesty.
    const res = await fetch(
      `${API_BASE_URL}/v1/ohlc?base=${encodeURIComponent(base)}&quote=${encodeURIComponent(quote)}`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: OhlcResp };
    return env.data ?? null;
  } catch {
    return null;
  }
}

async function fetchHistory(
  base: string,
  quote: string,
): Promise<HistoryTrade[]> {
  if (isCIStub) return [];
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/history?base=${encodeURIComponent(base)}&quote=${encodeURIComponent(quote)}&limit=50`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return [];
    const env = (await res.json()) as { data: HistoryTrade[] };
    return env.data ?? [];
  } catch {
    return [];
  }
}

interface PoolRow {
  source: string;
  trade_count_24h: number;
  volume_24h_usd?: string | null;
  last_price?: string | null;
  last_trade_at: string;
}

async function fetchSourceBreakdown(base: string, quote: string): Promise<PoolRow[]> {
  if (isCIStub) return [];
  try {
    // /v1/pools?base=&quote= returns one row per source contributing
    // to this exact pair. Naturally sorted by 24h USD volume (the
    // endpoint default), which is the right order for the panel.
    const res = await fetch(
      `${API_BASE_URL}/v1/pools?base=${encodeURIComponent(base)}&quote=${encodeURIComponent(quote)}&limit=50`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return [];
    const env = (await res.json()) as { data: PoolRow[] };
    return env.data ?? [];
  } catch {
    return [];
  }
}

export default async function PairPage({ params }: { params: Params }) {
  const { pair } = await params;
  const decoded = decodePairSlug(pair);
  if (!decoded) {
    return <PairNotFound />;
  }
  const { base, quote } = decoded;

  const [price, chart, ohlc, history, sourceBreakdown] = await Promise.all([
    fetchPrice(base, quote),
    fetchChart(base, quote),
    fetchOhlc(base, quote),
    fetchHistory(base, quote),
    fetchSourceBreakdown(base, quote),
  ]);

  const baseLabel = shortAsset(base);
  const quoteLabel = shortAsset(quote);
  const priceNum = price?.price ? Number(price.price) : null;

  // Per-source breakdown: count trades by source in the history sample.
  // (The full 24h source distribution is rendered by SourceBreakdownPanel
  // below, which pulls real volume from /v1/pools?base=&quote=. perSource
  // here remains for the Activity panel's "Sources: N" stat.)
  const perSource = new Map<string, number>();
  for (const t of history) {
    perSource.set(t.source, (perSource.get(t.source) ?? 0) + 1);
  }

  // Compute change from the chart points: last vs 24h-ago.
  const points = chart?.points ?? [];
  const change24h =
    points.length >= 2 && points[0]?.p && points[points.length - 1]?.p
      ? ((Number(points[points.length - 1].p) / Number(points[0].p) - 1) * 100)
      : null;

  // Schema.org BreadcrumbList — Home → Markets → BASE/QUOTE.
  const breadcrumbLD = {
    '@context': 'https://schema.org',
    '@type': 'BreadcrumbList',
    itemListElement: [
      {
        '@type': 'ListItem',
        position: 1,
        name: 'Home',
        item: 'https://ratesengine.net',
      },
      {
        '@type': 'ListItem',
        position: 2,
        name: 'Markets',
        item: 'https://ratesengine.net/markets',
      },
      {
        '@type': 'ListItem',
        position: 3,
        name: `${baseLabel} / ${quoteLabel}`,
        item: `https://ratesengine.net/markets/${encodeURIComponent(`${base}~${quote}`)}`,
      },
    ],
  };
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(breadcrumbLD) }}
      />
      <header className="space-y-3">
        <Link
          href="/markets"
          className="inline-flex items-center gap-1 text-xs text-slate-500 hover:text-brand-600"
        >
          ← All markets
        </Link>
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="text-3xl font-semibold tracking-tight">
            <AssetBadge canonical={base} /> /{' '}
            <AssetBadge canonical={quote} />
          </h1>
          {price?.price_type && (
            <span className="rounded bg-slate-100 px-2 py-0.5 font-mono text-xs uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-300">
              {price.price_type}
            </span>
          )}
        </div>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Live VWAP, hourly chart, and the last 50 trades on this pair.
          Pair source: <code className="font-mono">{base}</code> /{' '}
          <code className="font-mono">{quote}</code>.
        </p>
      </header>

      <section className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Panel
          title="Price"
          subtitle={`${baseLabel} → ${quoteLabel}`}
          className="lg:col-span-2"
        >
          <div className="flex flex-wrap items-baseline gap-4">
            <span className="font-mono text-3xl tabular-nums">
              {priceNum != null ? formatPriceCompact(priceNum) : '—'}
            </span>
            {change24h != null && Number.isFinite(change24h) && (
              <ChangeBadge pct={change24h} window="24h" />
            )}
            {price?.observed_at && (
              <span className="text-xs text-slate-500">
                as of {formatTimestamp(price.observed_at)}
              </span>
            )}
          </div>
          <div className="mt-4">
            <PairChart
              base={base}
              quote={quote}
              baseLabel={baseLabel}
              quoteLabel={quoteLabel}
            />
          </div>
        </Panel>

        <Panel title="Activity" subtitle="last 24h">
          <dl className="grid grid-cols-2 gap-2 text-sm">
            <Stat label="Trades sampled" value={history.length.toLocaleString()} />
            <Stat
              label="Sources"
              value={perSource.size.toString()}
            />
            {points[points.length - 1]?.v_usd && (
              <Stat
                label="Last hour USD vol"
                value={formatUsd(Number(points[points.length - 1].v_usd))}
              />
            )}
          </dl>
        </Panel>
      </section>

      {ohlc && (
        <Panel
          title="OHLC — last 1h"
          subtitle={`${ohlc.from.slice(0, 16).replace('T', ' ')}Z → ${ohlc.to.slice(0, 16).replace('T', ' ')}Z`}
        >
          <dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-6">
            <Stat label="Open" value={ohlc.open} />
            <Stat label="High" value={ohlc.high} />
            <Stat label="Low" value={ohlc.low} />
            <Stat label="Close" value={ohlc.close} />
            <Stat
              label="Quote vol"
              value={formatUsd(Number(ohlc.quote_volume) / 1e8)}
            />
            <Stat
              label="Trades"
              value={ohlc.trade_count.toLocaleString()}
            />
          </dl>
        </Panel>
      )}

      {sourceBreakdown.length > 0 && (
        <SourceBreakdownPanel rows={sourceBreakdown} />
      )}

      {history.length > 0 ? (
        <Panel title="Recent trades" subtitle={`${history.length} most recent across all sources`}>
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
                  <th className="px-3 py-2 font-medium">Time</th>
                  <th className="px-3 py-2 font-medium">Source</th>
                  <th className="px-3 py-2 text-right font-medium">Price</th>
                  <th className="px-3 py-2 text-right font-medium">
                    Base amount
                  </th>
                  <th className="px-3 py-2 text-right font-medium">
                    Quote amount
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100 font-mono text-xs dark:divide-slate-800">
                {history.map((t, i) => (
                  <tr
                    key={`${t.tx_hash ?? ''}|${t.op_index ?? i}|${t.ts}`}
                    className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
                  >
                    <td className="px-3 py-2 tabular-nums text-slate-500">
                      {t.tx_hash ? (
                        <a
                          href={`https://stellar.expert/explorer/public/tx/${t.tx_hash}`}
                          target="_blank"
                          rel="noreferrer noopener"
                          className="hover:text-brand-600 hover:underline"
                          title={`View tx ${t.tx_hash} on stellar.expert`}
                        >
                          {formatTimestamp(t.ts)}
                        </a>
                      ) : (
                        formatTimestamp(t.ts)
                      )}
                    </td>
                    <td className="px-3 py-2 uppercase tracking-wider">
                      <Link
                        href={`/sources/${t.source}`}
                        className="hover:text-brand-600 hover:underline"
                      >
                        {t.source}
                      </Link>
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">
                      {t.price}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums text-slate-500">
                      {t.base_amount ?? '—'}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums text-slate-500">
                      {t.quote_amount ?? '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Panel>
      ) : (
        <Panel title="Recent trades">
          <p className="text-sm text-slate-500">
            No trades returned for this pair in the last sample.
          </p>
        </Panel>
      )}
    </div>
  );
}

function SourceBreakdownPanel({ rows }: { rows: PoolRow[] }) {
  // Total 24h USD volume across all sources contributing to this
  // pair. The `volume_24h_usd` field is null on sources that
  // contributed trades but the aggregator hasn't priced (Phase 1
  // USD-pegged-quote rule). Those rows show "—" and don't count
  // toward the bar denominator.
  const totalUSD = rows.reduce((acc, r) => {
    const v = Number(r.volume_24h_usd ?? '0');
    return Number.isFinite(v) ? acc + v : acc;
  }, 0);
  return (
    <Panel
      title="Sources contributing"
      subtitle={`${rows.length} venue${rows.length === 1 ? '' : 's'} · ranked by 24h USD volume · /v1/pools?base=&quote=`}
    >
      <ul className="space-y-2">
        {rows.map((r) => {
          const v = r.volume_24h_usd ? Number(r.volume_24h_usd) : null;
          const pct = totalUSD > 0 && v != null && Number.isFinite(v) ? (v / totalUSD) * 100 : null;
          const lp = r.last_price ? Number(r.last_price) : null;
          const lpFixed =
            lp == null
              ? null
              : lp >= 1000
                ? lp.toFixed(2)
                : lp >= 1
                  ? lp.toFixed(4)
                  : lp >= 0.0001
                    ? lp.toFixed(6)
                    : lp.toExponential(3);
          return (
            <li key={r.source} className="flex items-center gap-3 text-sm">
              <Link
                href={`/sources/${r.source}`}
                className="w-32 font-mono text-xs uppercase tracking-wider text-slate-600 hover:text-brand-600 dark:text-slate-300"
              >
                {r.source}
              </Link>
              <div className="flex-1">
                <div className="h-2 overflow-hidden rounded bg-slate-100 dark:bg-slate-800">
                  <div
                    className="h-full bg-brand-500"
                    style={{ width: `${pct ?? 0}%` }}
                  />
                </div>
              </div>
              <span className="w-24 text-right font-mono tabular-nums text-xs text-slate-500">
                {lpFixed ?? '—'}
              </span>
              <span className="w-28 text-right font-mono tabular-nums text-xs text-slate-700 dark:text-slate-300">
                {v != null && Number.isFinite(v) && v > 0
                  ? `$${formatCompact(v)}`
                  : '—'}
              </span>
              <span className="w-12 text-right font-mono tabular-nums text-xs text-slate-500">
                {pct != null ? `${pct.toFixed(0)}%` : '—'}
              </span>
            </li>
          );
        })}
      </ul>
    </Panel>
  );
}

function PairNotFound() {
  return (
    <div className="mx-auto max-w-3xl px-6 py-16 text-center">
      <h1 className="text-2xl font-semibold">Pair not found</h1>
      <p className="mt-2 text-sm text-slate-500">
        The slug must be in the form{' '}
        <code className="font-mono">{`base${PAIR_SEPARATOR}quote`}</code>.
      </p>
      <Link
        href="/markets"
        className="mt-6 inline-flex items-center gap-1 text-sm text-brand-600 hover:underline"
      >
        Browse all markets →
      </Link>
    </div>
  );
}

function Panel({
  title,
  subtitle,
  className,
  children,
}: {
  title: string;
  subtitle?: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <section
      className={`rounded-lg border border-slate-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900 ${className ?? ''}`}
    >
      <header className="mb-3 flex items-baseline justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-300">
          {title}
        </h2>
        {subtitle && (
          <span className="text-xs text-slate-400">{subtitle}</span>
        )}
      </header>
      {children}
    </section>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-[11px] uppercase tracking-wider text-slate-500">
        {label}
      </dt>
      <dd className="mt-1 font-mono text-sm tabular-nums">{value}</dd>
    </div>
  );
}

function AssetBadge({ canonical }: { canonical: string }) {
  if (canonical === 'native') {
    return <span>XLM</span>;
  }
  if (canonical.startsWith('fiat:')) {
    return <span>{canonical.replace('fiat:', '')}</span>;
  }
  if (canonical.startsWith('crypto:')) {
    return <span>{canonical.replace('crypto:', '')}</span>;
  }
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return <span>{canonical}</span>;
  return <span>{canonical.slice(0, dashIx)}</span>;
}

function shortAsset(canonical: string): string {
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.replace('fiat:', '');
  if (canonical.startsWith('crypto:')) return canonical.replace('crypto:', '');
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return canonical;
  return canonical.slice(0, dashIx);
}

function ChangeBadge({ pct, window }: { pct: number; window: string }) {
  const tone =
    pct > 0
      ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
      : pct < 0
        ? 'bg-rose-50 text-rose-700 dark:bg-rose-950/40 dark:text-rose-300'
        : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400';
  const sign = pct > 0 ? '+' : '';
  return (
    <span
      className={`rounded px-2 py-0.5 font-mono text-xs tabular-nums ${tone}`}
    >
      {sign}
      {pct.toFixed(2)}%
      <span className="ml-1 text-[10px] uppercase tracking-wider opacity-70">
        {window}
      </span>
    </span>
  );
}


function formatPriceCompact(n: number): string {
  if (n >= 1) return `$${n.toFixed(n >= 100 ? 2 : 4)}`;
  if (n >= 0.001) return `$${n.toFixed(6)}`;
  if (n > 0) return `$${n.toExponential(3)}`;
  return '—';
}

function formatUsd(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '—';
  if (n >= 1_000_000) return `$${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `$${(n / 1_000).toFixed(1)}k`;
  return `$${n.toFixed(2)}`;
}

function formatTimestamp(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toISOString().replace('T', ' ').slice(0, 19) + ' UTC';
}
