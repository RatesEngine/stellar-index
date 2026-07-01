'use client';

import Link from 'next/link';

import { cn } from '@/lib/cn';

export type DonutSlice = {
  label: string;
  value: number;
  /** Optional internal link for the legend row. */
  href?: string;
  /** Optional explicit color; otherwise drawn from the palette. */
  color?: string;
};

// Categorical palette — distinct hues, design-token-aligned where possible.
// Used for composition breakdowns (reserves, source classes, protocol mix).
const PALETTE = [
  '#0284c7', // brand-600
  '#16a34a', // up
  '#d97706', // amber-600
  '#7c3aed', // violet-600
  '#0891b2', // cyan-600
  '#db2777', // pink-600
  '#ea580c', // orange-600
  '#4f46e5', // indigo-600
  '#0d9488', // teal-600
  '#94a3b8', // slate-400 (tail / "other")
];

/**
 * DonutChart — a crisp SVG donut for categorical breakdowns (the "pie"
 * complement to the time-series lightweight-charts). Segments are drawn
 * with stroke-dasharray on stacked circles (sharp at any size), with a
 * center total and a clickable legend. Slices are rendered largest-first;
 * pass pre-sorted data or let it sort by value.
 */
export function DonutChart({
  data,
  size = 160,
  thickness = 22,
  centerLabel,
  centerSub,
  formatValue = (n) => n.toLocaleString(),
  className,
}: {
  data: DonutSlice[];
  size?: number;
  thickness?: number;
  centerLabel?: string;
  centerSub?: string;
  formatValue?: (n: number) => string;
  className?: string;
}) {
  const slices = [...data]
    .filter((s) => Number.isFinite(s.value) && s.value > 0)
    .sort((a, b) => b.value - a.value);
  const total = slices.reduce((sum, s) => sum + s.value, 0);

  if (slices.length === 0 || total <= 0) {
    return (
      <div className={cn('text-sm text-ink-muted', className)}>
        No composition data to chart.
      </div>
    );
  }

  const r = (size - thickness) / 2;
  const c = 2 * Math.PI * r;
  const cx = size / 2;
  const cy = size / 2;

  let acc = 0;
  const segs = slices.map((s, i) => {
    const frac = s.value / total;
    const seg = {
      ...s,
      color: s.color ?? PALETTE[i % PALETTE.length],
      frac,
      dash: frac * c,
      offset: -acc * c,
      pct: frac * 100,
    };
    acc += frac;
    return seg;
  });

  return (
    <div className={cn('flex flex-wrap items-center gap-x-6 gap-y-3', className)}>
      <svg
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        role="img"
        aria-label={`Composition donut: ${segs
          .map((s) => `${s.label} ${s.pct.toFixed(1)}%`)
          .join(', ')}`}
        className="shrink-0"
      >
        <g transform={`rotate(-90 ${cx} ${cy})`}>
          {/* track */}
          <circle
            cx={cx}
            cy={cy}
            r={r}
            fill="none"
            stroke="rgba(148,163,184,0.18)"
            strokeWidth={thickness}
          />
          {segs.map((s) => (
            <circle
              key={s.label}
              cx={cx}
              cy={cy}
              r={r}
              fill="none"
              stroke={s.color}
              strokeWidth={thickness}
              strokeDasharray={`${s.dash} ${c - s.dash}`}
              strokeDashoffset={s.offset}
              strokeLinecap="butt"
            >
              <title>{`${s.label}: ${formatValue(s.value)} (${s.pct.toFixed(1)}%)`}</title>
            </circle>
          ))}
        </g>
        {centerLabel && (
          <text
            x={cx}
            y={centerSub ? cy - 2 : cy + 4}
            textAnchor="middle"
            className="fill-ink font-semibold"
            style={{ fontSize: 18, fontFamily: 'var(--font-sans)' }}
          >
            {centerLabel}
          </text>
        )}
        {centerSub && (
          <text
            x={cx}
            y={cy + 14}
            textAnchor="middle"
            className="fill-ink-muted"
            style={{ fontSize: 10, fontFamily: 'var(--font-sans)' }}
          >
            {centerSub}
          </text>
        )}
      </svg>

      <ul className="min-w-0 flex-1 space-y-1.5 text-sm">
        {segs.map((s) => {
          const row = (
            <span className="flex items-center gap-2">
              <span
                aria-hidden
                className="h-2.5 w-2.5 shrink-0 rounded-xs"
                style={{ backgroundColor: s.color }}
              />
              <span className="min-w-0 flex-1 truncate text-ink-body">{s.label}</span>
              <span className="font-mono tabular-nums text-ink">{formatValue(s.value)}</span>
              <span className="w-12 text-right font-mono tabular-nums text-ink-muted">
                {s.pct.toFixed(1)}%
              </span>
            </span>
          );
          return (
            <li key={s.label}>
              {s.href ? (
                <Link href={s.href} className="block rounded-sm px-1 -mx-1 transition-colors hover:bg-surface-muted hover:text-brand-600">
                  {row}
                </Link>
              ) : (
                <span className="block px-1 -mx-1">{row}</span>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
