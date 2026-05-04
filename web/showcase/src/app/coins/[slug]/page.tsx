import Link from 'next/link';
import { notFound } from 'next/navigation';

import { Panel } from '@/components/reveal';
import {
  AccelerationArrow,
  MultiWindowDelta,
  Sparkline,
  StreakIndicator,
} from '@/components/primitives';
import { SourceContributionDonut } from '@/components/panels/SourceContributionDonut';
import { asExample } from '@/api/client';
import { formatCompact, formatPrice } from '@/lib/format';
import { SEED_COINS, findCoin } from '@/lib/coins-seed';

/**
 * /coins/[slug] — single coin detail page. Overview tab today;
 * chart / markets / history / supply / issuer / liquidity tabs
 * land in subsequent PRs.
 *
 * `generateStaticParams` enumerates the seed slugs at build time.
 * Production must extend this to top-N by volume + a client-side
 * fallback for the long tail (per data-inventory §4 URL routing).
 */
export function generateStaticParams() {
  return SEED_COINS.map((c) => ({ slug: c.slug }));
}

type Params = Promise<{ slug: string }>;

export default async function CoinDetailPage({ params }: { params: Params }) {
  const { slug } = await params;
  const coin = findCoin(slug);
  if (!coin) notFound();

  return (
    <div className="mx-auto max-w-6xl space-y-6 p-6">
      <header className="space-y-3">
        <nav className="text-xs text-slate-500">
          <Link href="/coins" className="hover:text-brand-600">
            Coins
          </Link>{' '}
          /{' '}
          <span className="text-slate-700 dark:text-slate-300">
            {coin.ticker}
          </span>
        </nav>
        <div className="flex flex-wrap items-baseline gap-4">
          <h1 className="text-3xl font-semibold tracking-tight">
            {coin.name}
            <span className="ml-2 text-xl text-slate-500">{coin.ticker}</span>
          </h1>
          <span
            className="rounded-md bg-slate-100 px-2 py-0.5 text-[11px] uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-300"
            title="Asset type"
          >
            {coin.type}
          </span>
        </div>
        {coin.description && (
          <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
            {coin.description}
          </p>
        )}
      </header>

      <Tabs slug={coin.slug} active="overview" />

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Panel
          title="Price"
          source={asExample('/v1/price', { asset: coin.slug, quote: 'fiat:USD' })}
          panelId="price-card"
          className="lg:col-span-2"
          bodyClassName="space-y-4"
        >
          <div className="flex flex-wrap items-baseline gap-4">
            <span className="font-mono text-3xl tabular-nums">
              ${formatPrice(coin.price)}
            </span>
            <Sparkline values={coin.spark} width={140} height={36} />
            <AccelerationArrow
              direction={coin.h24 > 0 ? 'up' : coin.h24 < 0 ? 'down' : 'flat'}
              acceleration={coin.h24 > coin.h1 ? 'increasing' : 'flat'}
            />
            <StreakIndicator
              kind="streak"
              direction={coin.d7 > 0 ? 'up' : 'down'}
              days={Math.max(1, Math.round(Math.abs(coin.d7) / 2))}
            />
          </div>
          <MultiWindowDelta
            windows={[
              { label: '1h', deltaPct: coin.h1 },
              { label: '24h', deltaPct: coin.h24 },
              { label: '7d', deltaPct: coin.d7 },
              { label: '30d', deltaPct: coin.d30 },
            ]}
          />
        </Panel>

        <Panel
          title="Confidence"
          hint="Multi-factor score per ADR-0019"
          source={asExample('/v1/price', { asset: coin.slug, quote: 'fiat:USD' })}
          panelId="confidence-card"
        >
          <div className="space-y-2">
            <div className="text-3xl font-bold tabular-nums">
              {coin.confidence}/100
            </div>
            <ul className="space-y-1 text-xs text-slate-600 dark:text-slate-400">
              <li>✓ {coin.sources.length} sources</li>
              <li>✓ Cross-reference within 0.4%</li>
              <li>✓ Baseline freshness OK</li>
              <li>✓ Depth ${formatCompact(coin.volume24h / 24)}/hr</li>
            </ul>
          </div>
        </Panel>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Panel
          title="24h stats"
          source={asExample('/v1/ohlc', { base: coin.slug, quote: 'fiat:USD' })}
        >
          <dl className="grid grid-cols-2 gap-2 text-sm">
            <Stat label="Volume" value={`$${formatCompact(coin.volume24h)}`} />
            <Stat label="Market cap" value={`$${formatCompact(coin.marketCap)}`} />
            <Stat label="Circulating" value={formatCompact(coin.circulatingSupply)} />
            <Stat label="Total" value={formatCompact(coin.totalSupply)} />
          </dl>
        </Panel>

        <Panel
          title="Source contribution"
          hint="VWAP weighting per source"
          source={asExample(`/v1/price/${coin.slug}/fiat:USD/sources`)}
          panelId="source-donut"
          className="lg:col-span-2"
        >
          <SourceContributionDonut contributions={coin.sources} />
        </Panel>
      </div>

      {coin.issuer && (
        <Panel
          title="Issuer"
          source={asExample(`/v1/issuers/${coin.issuer}`)}
        >
          <dl className="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2">
            <Stat label="G-strkey" mono value={coin.issuer.slice(0, 12) + '…'} />
            {coin.homeDomain && (
              <Stat label="Home domain" mono value={coin.homeDomain} />
            )}
          </dl>
        </Panel>
      )}

      <p className="text-xs text-slate-500">
        v0 of this page renders a static seed.
        Real data plumbs through once <code className="font-mono">/v1/coins/{'{slug}'}</code> ships.
      </p>
    </div>
  );
}

function Tabs({
  slug,
  active,
}: {
  slug: string;
  active: 'overview' | 'chart' | 'markets' | 'history' | 'supply' | 'issuer' | 'liquidity';
}) {
  type Tab = { key: string; label: string; href: string; disabled?: boolean };
  const tabs: Tab[] = [
    { key: 'overview', label: 'Overview', href: `/coins/${slug}` },
    { key: 'chart', label: 'Chart', href: `/coins/${slug}?tab=chart`, disabled: true },
    { key: 'markets', label: 'Markets', href: `/coins/${slug}?tab=markets`, disabled: true },
    { key: 'history', label: 'History', href: `/coins/${slug}?tab=history`, disabled: true },
    { key: 'supply', label: 'Supply', href: `/coins/${slug}?tab=supply`, disabled: true },
    { key: 'issuer', label: 'Issuer', href: `/coins/${slug}?tab=issuer`, disabled: true },
    { key: 'liquidity', label: 'Liquidity', href: `/coins/${slug}?tab=liquidity`, disabled: true },
  ];
  return (
    <nav className="flex gap-1 overflow-x-auto border-b border-slate-200 text-sm dark:border-slate-800">
      {tabs.map((t) =>
        t.disabled ? (
          <span
            key={t.key}
            className="cursor-not-allowed border-b-2 border-transparent px-3 py-2 text-slate-400 dark:text-slate-600"
            title="Coming soon"
          >
            {t.label}
          </span>
        ) : (
          <Link
            key={t.key}
            href={t.href}
            className={`border-b-2 px-3 py-2 ${
              t.key === active
                ? 'border-brand-500 font-medium text-brand-600 dark:text-brand-400'
                : 'border-transparent text-slate-600 hover:text-brand-600 dark:text-slate-300'
            }`}
          >
            {t.label}
          </Link>
        ),
      )}
    </nav>
  );
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
