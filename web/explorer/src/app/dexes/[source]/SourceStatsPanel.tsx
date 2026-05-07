'use client';

import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';
import { SourceSparkline } from '@/components/SourceSparkline';

interface VolumeBucket {
  hour: string;
  volume_usd: string;
}

interface SourceStats {
  name: string;
  trade_count_24h?: number;
  volume_24h_usd?: string;
  markets_count_24h?: number;
  volume_history_24h?: VolumeBucket[];
}

/**
 * SourceStatsPanel — client-side rendering of the per-source 24h
 * activity strip on /dexes/[source]. Was server-side at build
 * time, but `/v1/sources?include=stats,sparkline` can take 10-25s
 * under cold-cache conditions and the Next build's per-page 60s
 * budget exhausted itself trying to render 5 source pages
 * concurrently. Moving the fetch to the client means the build
 * is free of API dependence; users see "Loading…" briefly on
 * first paint, then the real numbers fill in.
 */
export function SourceStatsPanel({ source }: { source: string }) {
  const { data } = useQuery<SourceStats | null>({
    queryKey: ['/v1/sources', 'stats+sparkline', source],
    queryFn: async () => {
      const env = await apiGet<{ data: SourceStats[] }>('/v1/sources', {
        include: 'stats,sparkline',
      });
      return env.data?.find((r) => r.name === source) ?? null;
    },
    staleTime: 60_000,
  });

  const trades = data?.trade_count_24h ?? 0;
  const volume = data?.volume_24h_usd ? Number(data.volume_24h_usd) : 0;
  const markets = data?.markets_count_24h ?? 0;

  return (
    <Panel
      title="24h activity"
      hint={`Live from /v1/sources?include=stats,sparkline (source=${source})`}
      source={asExample('/v1/sources', { include: 'stats,sparkline' })}
    >
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        <Stat
          label="24h volume"
          value={volume > 0 ? `$${formatCompact(volume)}` : '—'}
        />
        <Stat
          label="24h trades"
          value={trades > 0 ? formatCompact(trades) : '—'}
        />
        <Stat
          label="24h pools"
          value={markets > 0 ? markets.toLocaleString() : '—'}
        />
      </div>
      {data?.volume_history_24h && data.volume_history_24h.length > 0 && (
        <div className="mt-4 border-t border-slate-200 pt-3 dark:border-slate-800">
          <div className="text-[10px] uppercase tracking-wider text-slate-500">
            Volume by hour
          </div>
          <div className="mt-2">
            <SourceSparkline buckets={data.volume_history_24h} width={400} height={48} />
          </div>
        </div>
      )}
    </Panel>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wider text-slate-500">
        {label}
      </div>
      <div className="mt-1 text-2xl font-semibold tabular-nums">{value}</div>
    </div>
  );
}

function formatCompact(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(2)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return n.toLocaleString();
}
