import { twMerge } from 'tailwind-merge';

export type SparklineProps = {
  /** Ordered values, oldest first. */
  values: number[];
  width?: number;
  height?: number;
  /**
   * Sets the line colour scheme. 'auto' colours green/red based on
   * the net change from first to last value; 'neutral' renders slate.
   */
  tone?: 'auto' | 'neutral' | 'up' | 'down';
  className?: string;
};

/**
 * Inline sparkline — pure SVG, no client-side rendering library.
 *
 * Per design-inventory §6.2 these appear next to every entity in
 * every list. The component is small and renders deterministically
 * so it's safe to use at high cardinality (top-100 coin tables,
 * etc.). Returns null on empty input rather than rendering a stub —
 * callers can render their own "no data" treatment.
 */
export function Sparkline({
  values,
  width = 80,
  height = 24,
  tone = 'auto',
  className,
}: SparklineProps) {
  if (values.length < 2) {
    return null;
  }
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  const stepX = width / (values.length - 1);

  const points = values
    .map((v, i) => {
      const x = i * stepX;
      const y = height - ((v - min) / range) * height;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(' ');

  const netDelta = values[values.length - 1]! - values[0]!;
  const resolvedTone =
    tone === 'auto' ? (netDelta >= 0 ? 'up' : 'down') : tone;
  const stroke =
    resolvedTone === 'up'
      ? 'rgb(22 163 74)' // up-DEFAULT
      : resolvedTone === 'down'
        ? 'rgb(220 38 38)' // down-DEFAULT
        : 'rgb(100 116 139)'; // slate-500

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      className={twMerge('inline-block align-middle', className)}
      aria-label={`sparkline: ${values.length} points, ${
        netDelta >= 0 ? 'up' : 'down'
      } overall`}
    >
      <polyline
        points={points}
        fill="none"
        stroke={stroke}
        strokeWidth={1.5}
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}
