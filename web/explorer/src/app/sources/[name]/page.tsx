import type { Metadata } from 'next';
import Link from 'next/link';

import { Breadcrumbs } from '@/components/ui';
import { SourceStatsPanel } from '@/app/dexes/[source]/SourceStatsPanel';
import { SourceTopChart } from '@/app/dexes/[source]/SourceTopChart';
import { formatCompact } from '@/lib/format';
import { SITE_OG_IMAGES, SITE_TWITTER_IMAGES, serializeJsonLd } from '@/lib/seo';

// Sources that also have a dedicated DEX or CEX detail page — used to
// offer a "view as …" cross-link from the generic source profile.
const DEX_PAGES = new Set(['soroswap', 'phoenix', 'aquarius', 'sdex', 'comet']);
const EXCHANGE_PAGES = new Set(['binance', 'coinbase', 'kraken', 'bitstamp']);

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.stellarindex.io';

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

const BUILD_FETCH_TIMEOUT_MS = 8_000;

type Params = Promise<{ name: string }>;

interface VolumeBucket {
  hour: string;
  volume_usd: string;
}

interface Source {
  name: string;
  class: 'exchange' | 'aggregator' | 'oracle' | 'authority_sanity';
  subclass?: string;
  include_in_vwap: boolean;
  paid: boolean;
  backfill_available: boolean;
  backfill_safe: boolean;
  default_weight?: number;
  trade_count_24h?: number;
  volume_24h_usd?: string | null;
  markets_count_24h?: number;
  volume_history_24h?: VolumeBucket[];
}

interface CursorRow {
  source: string;
  sub_source?: string;
  last_ledger: number;
  last_updated: string;
  lag_seconds: number;
}

export async function generateStaticParams() {
  // Pre-render every registered source so deep links resolve at
  // build time. CI stub falls back to `sdex` (the highest-volume
  // on-chain venue) so static export anchors on something
  // recognisable.
  const fallback = [{ name: 'sdex' }];
  if (isCIStub) return fallback;
  try {
    const res = await fetch(`${API_BASE_URL}/v1/sources`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: Source[] };
    const names = env.data?.map((s) => s.name).filter(Boolean) ?? [];
    return names.length > 0 ? names.map((name) => ({ name })) : fallback;
  } catch {
    return fallback;
  }
}

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { name } = await params;
  const canonical = `https://stellarindex.io/sources/${encodeURIComponent(name)}`;
  const title = `${name} — source detail · Stellar Index`;
  const description = `Per-venue source metadata, ingest cursor, and contribution profile for ${name}.`;
  return {
    title,
    description,
    alternates: { canonical },
    openGraph: { title, description, url: canonical, type: 'website', images: SITE_OG_IMAGES },
    twitter: { card: 'summary_large_image', title, description, images: SITE_TWITTER_IMAGES },
  };
}

async function fetchSource(name: string): Promise<Source | null> {
  if (isCIStub) return null;
  try {
    // Plain /v1/sources (no stats/sparkline). The unfiltered
    // call returns the full registry projection in <100ms and
    // is enough to render the registry-profile panel +
    // not-found check. Per-source 24h stats + sparkline used
    // to be in-line via include=stats,sparkline but that
    // endpoint takes 10-25s under cold-cache and was timing
    // out static-export budgets at build time. The stats now
    // load client-side via SourceStatsBlock below.
    const res = await fetch(`${API_BASE_URL}/v1/sources`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) return null;
    const env = (await res.json()) as { data: Source[] };
    return env.data?.find((s) => s.name === name) ?? null;
  } catch {
    return null;
  }
}

async function fetchCursors(): Promise<CursorRow[]> {
  if (isCIStub) return [];
  try {
    const res = await fetch(`${API_BASE_URL}/v1/diagnostics/cursors`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) return [];
    const env = (await res.json()) as { data: CursorRow[] };
    return env.data ?? [];
  } catch {
    return [];
  }
}

interface MarketRow {
  base: string;
  quote: string;
  last_trade_at: string;
  trade_count_24h: number;
  volume_24h_usd?: string | null;
  last_price?: string | null;
}

async function fetchSourceMarkets(name: string): Promise<MarketRow[]> {
  if (isCIStub) return [];
  try {
    // /v1/markets accepts ?source=<name> filter (DistinctPairs scoped
    // to one venue). Sort by volume desc and cap at 25 — the page
    // wants a "top markets" preview, not the full enumeration.
    const res = await fetch(
      `${API_BASE_URL}/v1/markets?source=${encodeURIComponent(name)}&order_by=volume_24h_usd_desc&limit=25`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return [];
    const env = (await res.json()) as { data: MarketRow[] };
    return env.data ?? [];
  } catch {
    return [];
  }
}

export default async function SourceDetailPage({
  params,
}: {
  params: Params;
}) {
  const { name } = await params;
  const [source, allCursors, topMarkets] = await Promise.all([
    fetchSource(name),
    fetchCursors(),
    fetchSourceMarkets(name),
  ]);

  if (!source) {
    return (
      <div className="mx-auto max-w-3xl px-6 py-16 text-center">
        <h1 className="text-2xl font-semibold">Source not found</h1>
        <p className="mt-2 text-sm text-ink-muted">
          No registered source named <code className="font-mono">{name}</code>.
        </p>
        <Link
          href="/sources"
          className="mt-6 inline-flex items-center gap-1 text-sm text-brand-600 hover:underline"
        >
          All sources →
        </Link>
      </div>
    );
  }

  const cursors = allCursors.filter((c) => c.source === name);

  // Schema.org BreadcrumbList — Home → Sources → <name>.
  const breadcrumbLD = {
    '@context': 'https://schema.org',
    '@type': 'BreadcrumbList',
    itemListElement: [
      { '@type': 'ListItem', position: 1, name: 'Home', item: 'https://stellarindex.io' },
      { '@type': 'ListItem', position: 2, name: 'Sources', item: 'https://stellarindex.io/sources' },
      { '@type': 'ListItem', position: 3, name, item: `https://stellarindex.io/sources/${name}` },
    ],
  };

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: serializeJsonLd(breadcrumbLD) }}
      />
      <header className="space-y-3">
        <Breadcrumbs
          items={[
            { label: 'Home', href: '/' },
            { label: 'Sources', href: '/sources' },
            { label: name },
          ]}
        />
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="text-3xl font-semibold tracking-tight">{name}</h1>
          <ClassBadge cls={source.class} />
          {source.subclass && (
            <span className="rounded bg-surface-subtle px-2 py-0.5 font-mono text-xs uppercase tracking-wider text-ink-body">
              {source.subclass}
            </span>
          )}
          {source.paid && (
            <span className="rounded bg-warn-50 px-2 py-0.5 text-[11px] uppercase tracking-wider text-warn-700">
              paid
            </span>
          )}
        </div>
        {/* Cross-link to the richer category view when one exists. */}
        <div className="flex flex-wrap gap-3 text-xs">
          {(DEX_PAGES.has(name) || source.subclass === 'dex') && (
            <Link href={`/dexes/${encodeURIComponent(name)}`} className="text-brand-600 hover:underline">
              View as DEX — pools &amp; chart →
            </Link>
          )}
          {EXCHANGE_PAGES.has(name) && (
            <Link href={`/exchanges/${encodeURIComponent(name)}`} className="text-brand-600 hover:underline">
              View exchange page →
            </Link>
          )}
          {source.class === 'oracle' && (
            <Link href="/oracles" className="text-brand-600 hover:underline">
              View in oracles →
            </Link>
          )}
        </div>
      </header>

      <Panel title="Registry profile">
        <dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
          <Stat
            label="Class"
            value={source.class}
            mono
          />
          <Stat
            label="Subclass"
            value={source.subclass ?? '—'}
            mono
          />
          <Stat
            label="Contributes to VWAP"
            value={source.include_in_vwap ? 'yes' : 'no'}
            tone={source.include_in_vwap ? 'ok' : undefined}
          />
          <Stat
            label="Default weight"
            value={
              source.default_weight != null
                ? source.default_weight.toString()
                : '—'
            }
            mono
          />
          <Stat
            label="Backfill available"
            value={source.backfill_available ? 'yes' : 'no'}
          />
          <Stat
            label="Backfill safe (audited)"
            value={source.backfill_safe ? 'yes' : 'no'}
            tone={source.backfill_safe ? 'ok' : 'warn'}
          />
          <Stat
            label="Paid tier"
            value={source.paid ? 'yes' : 'no'}
            tone={source.paid ? 'warn' : undefined}
          />
        </dl>
      </Panel>

      {/* 24h activity panel — client-side. Was inlined SSR with
          include=stats,sparkline but that endpoint takes 10-25s
          under cold-cache and was eating the static-export budget. */}
      <SourceStatsPanel
        source={name}
        unitsLabel={source.class === 'exchange' && source.subclass !== 'dex' ? 'pairs' : 'pools'}
      />

      <SourceTopChart source={name} sourceName={name} />

      <Panel
        title="Ingest cursors"
        subtitle={`${cursors.length} entries from /v1/diagnostics/cursors`}
        bodyClassName="-mx-4"
      >
        {cursors.length === 0 ? (
          <p className="px-4 py-3 text-sm text-ink-muted">
            No cursor recorded for this source. Likely either the source has
            never been started in this deployment, or it doesn&apos;t persist
            cursors (e.g. WebSocket-only venues that backfill via REST).
          </p>
        ) : (
          <table className="min-w-full divide-y divide-line text-sm">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-ink-muted">
                <th className="px-4 py-2 font-medium">Sub-source</th>
                <th className="px-4 py-2 text-right font-medium">
                  Last ledger
                </th>
                <th className="px-4 py-2 text-right font-medium">Updated</th>
                <th className="px-4 py-2 text-right font-medium">Lag</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line-subtle">
              {cursors.map((c, i) => (
                <tr
                  key={`${c.sub_source ?? ''}|${i}`}
                  className="hover:bg-surface-muted"
                >
                  <td className="px-4 py-2 font-mono text-xs">
                    {c.sub_source || '—'}
                  </td>
                  <td className="px-4 py-2 text-right font-mono tabular-nums">
                    #{c.last_ledger.toLocaleString()}
                  </td>
                  <td className="px-4 py-2 text-right font-mono text-xs text-ink-muted">
                    {c.last_updated.replace('T', ' ').slice(0, 19)} UTC
                  </td>
                  <td className="px-4 py-2 text-right">
                    <LagBadge seconds={c.lag_seconds} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      <Panel
        title="Top markets via this source"
        subtitle={`${topMarkets.length} pairs · ranked by 24h USD volume · /v1/markets?source=${name}`}
        bodyClassName="-mx-4"
      >
        {topMarkets.length === 0 ? (
          <p className="px-4 py-3 text-sm text-ink-muted">
            No markets observed for this source in the trailing 14 days. Either
            the venue isn&apos;t actively producing trades the indexer can decode,
            or the cursor hasn&apos;t advanced past the recency window yet.
          </p>
        ) : (
          <table className="min-w-full divide-y divide-line text-sm">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-ink-muted">
                <th className="px-4 py-2 font-medium">Base</th>
                <th className="px-4 py-2 font-medium">Quote</th>
                <th className="px-4 py-2 text-right font-medium">Last price</th>
                <th className="px-4 py-2 text-right font-medium">24h volume</th>
                <th className="px-4 py-2 text-right font-medium">24h trades</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line-subtle">
              {topMarkets.map((m) => {
                const slug = encodeURIComponent(`${m.base}~${m.quote}`);
                return (
                  <tr
                    key={`${m.base}|${m.quote}`}
                    className="hover:bg-surface-muted"
                  >
                    <td className="px-4 py-2">
                      <Link
                        href={`/markets/${slug}`}
                        className="font-mono text-xs hover:text-brand-600"
                      >
                        {shortAsset(m.base)}
                      </Link>
                    </td>
                    <td className="px-4 py-2">
                      <Link
                        href={`/markets/${slug}`}
                        className="font-mono text-xs hover:text-brand-600"
                      >
                        {shortAsset(m.quote)}
                      </Link>
                    </td>
                    <td className="px-4 py-2 text-right">
                      {m.last_price ? (
                        <span className="font-mono tabular-nums text-ink-body">
                          {formatLastPrice(m.last_price)}
                        </span>
                      ) : (
                        <span className="text-ink-faint">—</span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-right">
                      {m.volume_24h_usd ? (
                        <span className="font-mono tabular-nums">
                          ${formatCompact(Number(m.volume_24h_usd))}
                        </span>
                      ) : (
                        <span className="text-ink-faint">—</span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-right font-mono tabular-nums text-ink-muted">
                      {formatCompact(m.trade_count_24h)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  );
}

function formatLastPrice(raw: string): string {
  const n = Number(raw);
  if (!Number.isFinite(n)) return '—';
  return n >= 1000 ? n.toFixed(2) : n >= 1 ? n.toFixed(4) : n >= 0.0001 ? n.toFixed(6) : n.toExponential(3);
}

function shortAsset(canonical: string): string {
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.replace('fiat:', '');
  if (canonical.startsWith('crypto:')) return canonical;
  if (/^\d+$/.test(canonical)) return 'XLM';
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return canonical;
  return canonical.slice(0, dashIx);
}

function Panel({
  title,
  subtitle,
  bodyClassName,
  children,
}: {
  title: string;
  subtitle?: string;
  bodyClassName?: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-lg border border-line bg-surface p-4">
      <header className="mb-3 flex items-baseline justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-wider text-ink-body">
          {title}
        </h2>
        {subtitle && <span className="text-xs text-ink-faint">{subtitle}</span>}
      </header>
      <div className={bodyClassName ?? ''}>{children}</div>
    </section>
  );
}

function Stat({
  label,
  value,
  tone,
  mono,
}: {
  label: string;
  value: string;
  tone?: 'ok' | 'warn';
  mono?: boolean;
}) {
  const valueClass =
    tone === 'ok'
      ? 'text-up'
      : tone === 'warn'
        ? 'text-warn-700'
        : '';
  return (
    <div>
      <dt className="text-[10px] uppercase tracking-wider text-ink-muted">
        {label}
      </dt>
      <dd
        className={`mt-1 ${mono ? 'font-mono' : 'font-medium'} text-sm tabular-nums ${valueClass}`}
      >
        {value}
      </dd>
    </div>
  );
}

function ClassBadge({ cls }: { cls: Source['class'] }) {
  const tone =
    cls === 'exchange'
      ? 'bg-up-subtle text-up-strong'
      : cls === 'oracle'
        ? 'bg-brand-100 text-brand-800'
        : cls === 'aggregator'
          ? 'bg-warn-50 text-warn-700'
          : 'bg-surface-subtle text-ink-body';
  return (
    <span
      className={`rounded px-2 py-0.5 font-mono text-xs uppercase tracking-wider ${tone}`}
    >
      {cls}
    </span>
  );
}

function LagBadge({ seconds }: { seconds: number }) {
  const tone =
    seconds <= 60
      ? 'bg-up-subtle text-up'
      : seconds <= 600
        ? 'bg-warn-50 text-warn-700'
        : 'bg-down-subtle text-down';
  const label =
    seconds < 60
      ? `${seconds.toFixed(0)}s`
      : seconds < 3600
        ? `${(seconds / 60).toFixed(1)}m`
        : `${(seconds / 3600).toFixed(1)}h`;
  return (
    <span
      className={`inline-flex items-center rounded px-2 py-0.5 font-mono text-xs tabular-nums ${tone}`}
    >
      {label}
    </span>
  );
}
