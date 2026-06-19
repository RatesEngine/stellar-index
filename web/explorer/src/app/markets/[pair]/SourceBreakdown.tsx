'use client';

import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { DonutChart } from '@/components/charts/DonutChart';
import { apiGet, asExample } from '@/api/client';
import { formatCompact } from '@/lib/format';

interface SourceVolume {
  source: string;
  volume_24h_usd?: string | null;
  trade_count_24h: number;
  share_pct: number;
}
interface MarketSourcesResp {
  base?: string;
  quote?: string;
  asset?: string;
  window_secs: number;
  sources: SourceVolume[];
}

/**
 * SourceBreakdown — volume-by-source donut for a market (pair) or asset,
 * backed by /v1/markets/sources (the trailing-24h per-source aggregate).
 * Each slice is a source's USD volume; the legend links to its source
 * page. Renders nothing when no source has derivable USD volume.
 */
export function SourceBreakdown({
  base,
  quote,
  asset,
}: {
  base?: string;
  quote?: string;
  asset?: string;
}) {
  const params = asset ? { asset } : { base: base ?? '', quote: quote ?? '' };
  const { data, isLoading, isError } = useQuery<MarketSourcesResp>({
    queryKey: ['/v1/markets/sources', asset ?? '', base ?? '', quote ?? ''],
    queryFn: async () =>
      (await apiGet<{ data: MarketSourcesResp }>('/v1/markets/sources', params)).data,
    staleTime: 60_000,
    retry: false,
  });

  const slices = (data?.sources ?? [])
    .filter((s) => s.volume_24h_usd != null && Number(s.volume_24h_usd) > 0)
    .map((s) => ({
      label: s.source,
      value: Number(s.volume_24h_usd),
      href: `/sources/${encodeURIComponent(s.source)}`,
    }));
  const total = slices.reduce((sum, s) => sum + s.value, 0);

  // No priced volume → nothing meaningful to chart; stay quiet rather
  // than render an empty frame.
  if (!isLoading && slices.length === 0) return null;

  return (
    <Panel
      title="Volume by source — 24h"
      hint="Each venue's share of the trailing-24h USD volume on this market."
      source={asExample('/v1/markets/sources', params)}
    >
      {isLoading && <div className="h-40" />}
      {isError && <p className="text-sm text-ink-muted">Source breakdown is unavailable right now.</p>}
      {slices.length > 0 && (
        <DonutChart
          data={slices}
          centerLabel={`$${formatCompact(total)}`}
          centerSub="24h vol"
          formatValue={(n) => `$${formatCompact(n)}`}
        />
      )}
    </Panel>
  );
}
