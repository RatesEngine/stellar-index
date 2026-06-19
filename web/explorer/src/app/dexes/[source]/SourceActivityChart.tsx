'use client';

import { useState } from 'react';
import dynamic from 'next/dynamic';

import { formatCompact } from '@/lib/format';

const LineChart = dynamic(
  () => import('@/components/charts/LineChart').then((m) => m.LineChart),
  { ssr: false, loading: () => <div className="h-[200px]" /> },
);

export interface ActivityBucket {
  hour: string;
  volume_usd: string;
  trade_count?: number;
}

type TF = '24h' | '7d';

/**
 * SourceActivityChart — the source's activity as a two-pane
 * lightweight-charts: trade COUNT per hour as the line (top), USD
 * VOLUME per hour as the histogram (bars) beneath, with a crosshair
 * legend so both are hoverable. A 24h/7d toggle switches the window
 * (both series are passed in so the toggle is instant — no refetch).
 */
export function SourceActivityChart({
  buckets24h,
  buckets7d,
  height = 220,
}: {
  buckets24h: ActivityBucket[];
  buckets7d?: ActivityBucket[];
  height?: number;
}) {
  const has7d = (buckets7d?.length ?? 0) > 0;
  // Default to the wider 7d view when it's available (it carries more
  // signal than a single day); fall back to 24h when 7d wasn't fetched.
  const [tf, setTf] = useState<TF>(has7d ? '7d' : '24h');
  const active = tf === '7d' && has7d ? buckets7d! : buckets24h;

  const data = active.map((b) => ({
    time: Math.floor(new Date(b.hour).getTime() / 1000),
    value: b.trade_count ?? 0, // line: quantity of trades
    volume: Number(b.volume_usd) || 0, // bars: USD volume
  }));

  return (
    <div className="space-y-2">
      {has7d && (
        <div className="flex justify-end gap-1 text-[11px]">
          {(['24h', '7d'] as TF[]).map((o) => (
            <button
              key={o}
              type="button"
              onClick={() => setTf(o)}
              aria-pressed={tf === o}
              className={`rounded px-2 py-0.5 font-mono uppercase tracking-wider ${
                tf === o ? 'bg-brand-600 text-white' : 'text-ink-muted hover:bg-surface-subtle'
              }`}
            >
              {o}
            </button>
          ))}
        </div>
      )}
      <LineChart
        data={data}
        height={height}
        timeVisible
        legend={{
          valueLabel: 'Trades',
          volumeLabel: 'Volume',
          formatValue: (n) => formatCompact(n),
          formatVolume: (n) => `$${formatCompact(n)}`,
        }}
        ariaLabel={`Hourly trade count (line) over USD volume (bars) for the trailing ${tf}`}
      />
    </div>
  );
}
