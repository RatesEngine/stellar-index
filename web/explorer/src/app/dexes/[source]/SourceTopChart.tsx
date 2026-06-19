'use client';

import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { Skeleton } from '@/components/ui';
import { MarketChart } from '@/components/charts/MarketChart';
import { apiGet, asExample } from '@/api/client';

interface Market {
  base: string;
  quote: string;
  volume_24h_usd?: string | null;
}

/**
 * SourceTopChart — the source's highest-volume pair rendered as a
 * full OHLC + volume chart, so a DEX detail page leads with price
 * action rather than a bare pool table. Picks the top market via
 * /v1/markets?source=&order_by=volume_24h_usd_desc&limit=1 and hands
 * it to the shared MarketChart (which serves real candles off
 * /v1/ohlc). Renders nothing if the source has no priced pair.
 */
export function SourceTopChart({ source, sourceName }: { source: string; sourceName: string }) {
  const q = useQuery<Market | null>({
    queryKey: ['/v1/markets', source, 'top1'],
    queryFn: async () => {
      const env = await apiGet<{ data: Market[] }>('/v1/markets', {
        source,
        order_by: 'volume_24h_usd_desc',
        limit: 1,
      });
      return env.data?.[0] ?? null;
    },
    staleTime: 60_000,
    retry: false,
  });

  if (q.isLoading) {
    return (
      <Panel title="Top pair">
        <Skeleton className="h-[300px] w-full" />
      </Panel>
    );
  }

  const m = q.data;
  // No priced pair for this source — leave the chart out entirely
  // rather than render an empty frame.
  if (!m) return null;

  const slug = `${m.base}~${m.quote}`;
  const baseLabel = shortAsset(m.base);
  const quoteLabel = shortAsset(m.quote);

  return (
    <Panel
      title={`Top pair — ${baseLabel}/${quoteLabel}`}
      hint={`${sourceName}'s highest-volume pair over the trailing 24h`}
      source={asExample('/v1/markets', { source, order_by: 'volume_24h_usd_desc', limit: 1 })}
    >
      <MarketChart
        base={m.base}
        quote={m.quote}
        baseLabel={baseLabel}
        quoteLabel={quoteLabel}
        height={300}
      />
      <div className="mt-3 text-xs">
        <Link href={`/markets/${encodeURIComponent(slug)}`} className="text-brand-600 hover:underline">
          Full market detail →
        </Link>
      </div>
    </Panel>
  );
}

function shortAsset(canonical: string | undefined | null): string {
  if (!canonical) return '—';
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.replace('fiat:', '');
  if (canonical.startsWith('crypto:')) return canonical.replace('crypto:', '');
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return canonical;
  return canonical.slice(0, dashIx);
}
