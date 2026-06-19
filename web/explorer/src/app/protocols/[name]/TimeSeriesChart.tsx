'use client';

import { useMemo } from 'react';
import dynamic from 'next/dynamic';

import { formatCompact } from '@/lib/format';

// TimeSeriesChart — the per-protocol time-series card (on-chain activity +
// every bespoke `series` entry). Now rendered with TradingView Lightweight
// Charts (via the shared LineChart) instead of a hand-rolled SVG, so every
// chart on the explorer shares one charting engine, theme, crosshair, and
// resize behaviour. Keeps the peak / avg / latest summary header.
//
// Inputs are (date, value) points where `value` is a JS number used ONLY for
// chart geometry. The bespoke wire ships values as numeric STRINGS that can
// exceed 2^53 (ADR-0003); the caller parses them with Number() for the shape
// and shows the formatCompact figure in the summary — the precision loss is
// cosmetic (a pixel y-coordinate), never a served amount.

const LineChart = dynamic(
  () => import('@/components/charts/LineChart').then((m) => m.LineChart),
  { ssr: false, loading: () => <div className="h-56 w-full" /> },
);

export interface ChartPoint {
  date: string; // YYYY-MM-DD
  value: number;
}

export type ChartTone = 'emerald' | 'brand' | 'violet' | 'amber' | 'indigo';

export function TimeSeriesChart({
  points,
  label,
  unit,
}: {
  /** The series to plot; `value` is already a JS number (geometry only). */
  points: ChartPoint[];
  /** Human label for the aria summary (e.g. "USD volume", "daily events"). */
  label: string;
  /** Optional unit appended to the peak/avg figures (e.g. "USD", "events"). */
  unit?: string;
  /** Palette key — accepted for call-site compatibility; tone now derives
   *  from the series trend (up green / down red) like every other chart. */
  tone?: ChartTone;
  /** Accepted for call-site compatibility; no longer used (was the SVG
   *  gradient id). */
  gradientId?: string;
}) {
  const linePoints = useMemo(
    () =>
      points.map((p) => ({
        time: Math.floor(Date.parse(`${p.date}T00:00:00Z`) / 1000),
        value: p.value,
      })),
    [points],
  );

  if (points.length === 0) {
    return (
      <p className="py-6 text-center text-sm text-ink-muted">
        No data points in the window.
      </p>
    );
  }

  const unitSuffix = unit ? ` ${unit}` : '';
  const peak = points.reduce((best, p) => (p.value > best.value ? p : best), points[0]);
  const total = points.reduce((s, p) => s + p.value, 0);
  const avg = total / points.length;
  const latest = points[points.length - 1].value;
  const ariaLabel = `${label}: ${points.length} points, peak ${formatCompact(peak.value)}${unitSuffix} on ${peak.date}, average ${formatCompact(Math.round(avg))}${unitSuffix} over the window.`;

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1 text-xs text-ink-muted">
        <span>
          Peak{' '}
          <span className="font-mono tabular-nums text-ink-body">
            {formatCompact(peak.value)}
            {unitSuffix}
          </span>{' '}
          on {peak.date}
        </span>
        <span>
          Avg/point{' '}
          <span className="font-mono tabular-nums text-ink-body">
            {formatCompact(Math.round(avg))}
            {unitSuffix}
          </span>
        </span>
        <span>
          Latest{' '}
          <span className="font-mono tabular-nums text-ink-body">
            {formatCompact(latest)}
            {unitSuffix}
          </span>
        </span>
      </div>
      <LineChart data={linePoints} height={224} ariaLabel={ariaLabel} />
    </div>
  );
}

// shortDate — YYYY-MM-DD → "MMM D" (UTC, no Date parse ambiguity). Shared
// by the per-protocol page's axis/label helpers.
export function shortDate(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
  if (!m) return iso;
  const months = [
    'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
    'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
  ];
  const mon = months[Number(m[2]) - 1] ?? m[2];
  return `${mon} ${Number(m[3])}`;
}
