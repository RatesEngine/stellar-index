import type { Metadata } from 'next';
import Link from 'next/link';
import { notFound } from 'next/navigation';
import { ExternalLink } from 'lucide-react';

import { Breadcrumbs } from '@/components/ui';
import { SITE_OG_IMAGES, SITE_TWITTER_IMAGES, serializeJsonLd } from '@/lib/seo';
import { PoolsTable } from './PoolsTable';
import { SourceStatsPanel } from './SourceStatsPanel';
import { SourceTopChart } from './SourceTopChart';

// Curated list of DEX sources with friendly names + audit links.
// Mirrors the 5 cards on /dexes; per-DEX detail pages are
// statically pre-rendered for these slugs only. New DEXes added
// here automatically get a /dexes/<source> page.
const DEX_INFO: Record<
  string,
  { name: string; type: string; status: string; contractsUrl?: string; blurb: string }
> = {
  soroswap: {
    name: 'Soroswap',
    type: 'Uniswap V2 clone (Soroban)',
    status: 'live',
    contractsUrl: 'https://github.com/soroswap/core',
    blurb:
      'Constant-product AMM. Each pool below is a SoroswapPair contract. Click a pool to drill into its trade history and live VWAP.',
  },
  phoenix: {
    name: 'Phoenix',
    type: 'AMM (Soroban)',
    status: 'live',
    blurb:
      'Soroban AMM with per-field event split. Each pool below is one Phoenix pair contract.',
  },
  aquarius: {
    name: 'Aquarius',
    type: 'AMM with gauges (Soroban)',
    status: 'live',
    blurb:
      'Curve-style AMM with bribe/gauge layer. Constant-product and stableswap pools render uniformly.',
  },
  sdex: {
    name: 'SDEX',
    type: 'Native order book (classic)',
    status: 'native',
    blurb:
      'Stellar-native on-chain order book. Each row below is a (base, quote) classic-asset pair that traded on SDEX in the recency window.',
  },
  comet: {
    name: 'Comet',
    type: 'Balancer V1 fork (Soroban)',
    status: 'experimental',
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
  const canonical = `https://stellarindex.io/dexes/${encodeURIComponent(source)}`;
  const title = `${info.name} — every pool, live`;
  const description = `All ${info.name} pools observed in the last 14 days, with per-pool 24h trade count + last trade. Source: /v1/markets?source=${source}.`;
  return {
    title,
    description,
    alternates: { canonical },
    openGraph: { title, description, url: canonical, type: 'website', images: SITE_OG_IMAGES },
    twitter: { card: 'summary_large_image', title, description, images: SITE_TWITTER_IMAGES },
  };
}

export default async function SourceDetailPage({
  params,
}: {
  params: Params;
}) {
  const { source } = await params;
  const info = DEX_INFO[source];
  if (!info) notFound();

  // Schema.org BreadcrumbList — Home → DEXes → <name>.
  const breadcrumbLD = {
    '@context': 'https://schema.org',
    '@type': 'BreadcrumbList',
    itemListElement: [
      { '@type': 'ListItem', position: 1, name: 'Home', item: 'https://stellarindex.io' },
      { '@type': 'ListItem', position: 2, name: 'DEXes', item: 'https://stellarindex.io/dexes' },
      { '@type': 'ListItem', position: 3, name: info.name, item: `https://stellarindex.io/dexes/${source}` },
    ],
  };

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: serializeJsonLd(breadcrumbLD) }}
      />
      <Breadcrumbs
        items={[
          { label: 'Home', href: '/' },
          { label: 'DEXes', href: '/dexes' },
          { label: info.name },
        ]}
      />

      <header className="space-y-2 border-b border-line pb-4">
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="text-3xl font-semibold tracking-tight">
            {info.name}
          </h1>
          <span className="rounded-sm bg-surface-subtle px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-ink-body">
            {info.type}
          </span>
        </div>
        <p className="max-w-3xl text-sm text-ink-body">
          {info.blurb}
        </p>
      </header>

      <SourceStatsPanel source={source} />

      <SourceTopChart source={source} sourceName={info.name} />

      <PoolsTable source={source} sourceName={info.name} />

      <div className="flex flex-wrap gap-3 text-xs">
        <Link
          href={`/sources/${source}`}
          className="inline-flex items-center gap-1 text-ink-muted hover:text-brand-600"
        >
          Source registry detail →
        </Link>
        {info.contractsUrl && (
          <a
            href={info.contractsUrl}
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex items-center gap-1 text-ink-muted hover:underline"
          >
            Contracts source
            <ExternalLink className="h-3 w-3" />
          </a>
        )}
      </div>
    </div>
  );
}

