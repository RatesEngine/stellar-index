'use client';

interface Bucket {
  hour: string;
  volume_usd: string;
}

/**
 * SourceSparkline — mini 24h bar chart for /v1/sources?include=stats,sparkline
 * volume_history_24h. 24 vertical bars, height proportional to that
 * hour's USD volume. No labels — pure visual hint of trend.
 */
export function SourceSparkline({ buckets, width = 80, height = 24 }: { buckets?: Bucket[]; width?: number; height?: number }) {
  if (!buckets || buckets.length === 0) {
    return <span className="font-mono text-[10px] text-slate-300 dark:text-slate-700">—</span>;
  }
  const values = buckets.map((b) => Number(b.volume_usd) || 0);
  const max = Math.max(...values);
  if (max === 0) {
    return <span className="font-mono text-[10px] text-slate-300 dark:text-slate-700">no vol</span>;
  }
  const barWidth = width / buckets.length;
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} className="inline-block">
      {values.map((v, i) => {
        const h = (v / max) * height;
        const x = i * barWidth;
        const y = height - h;
        return (
          <rect
            key={i}
            x={x + 0.5}
            y={y}
            width={Math.max(0.5, barWidth - 1)}
            height={Math.max(0.5, h)}
            className="fill-brand-500/70 dark:fill-brand-400/70"
          />
        );
      })}
    </svg>
  );
}
