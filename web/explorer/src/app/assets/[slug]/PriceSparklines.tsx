'use client';

import { useState } from 'react';

interface Point {
  t: string;
  p?: string | null;
}

/**
 * PriceSparklines — toggle between 24h and 7d USD-price sparklines.
 * Falls back to whichever series is populated when one window is
 * empty. Hides itself entirely when both are too sparse to plot.
 */
export function PriceSparklines({
  points24h,
  points7d,
}: {
  points24h: Point[];
  points7d: Point[];
}) {
  const has24 = points24h.filter((p) => p.p != null).length >= 2;
  const has7 = points7d.filter((p) => p.p != null).length >= 2;
  const [pref, setPref] = useState<'24h' | '7d'>('24h');
  if (!has24 && !has7) return null;
  const active = !has24 ? '7d' : !has7 ? '24h' : pref;
  const points = active === '24h' ? points24h : points7d;

  return (
    <div className="border-t border-slate-200 pt-3 dark:border-slate-800">
      <div className="mb-1 flex items-center gap-2 text-[10px] uppercase tracking-wider">
        <button
          type="button"
          onClick={() => has24 && setPref('24h')}
          disabled={!has24}
          className={`rounded px-1.5 py-0.5 ${
            active === '24h'
              ? 'bg-brand-600 text-white'
              : 'text-slate-500 hover:text-brand-600 disabled:opacity-40'
          }`}
        >
          24h
        </button>
        <button
          type="button"
          onClick={() => has7 && setPref('7d')}
          disabled={!has7}
          className={`rounded px-1.5 py-0.5 ${
            active === '7d'
              ? 'bg-brand-600 text-white'
              : 'text-slate-500 hover:text-brand-600 disabled:opacity-40'
          }`}
        >
          7d
        </button>
      </div>
      <Sparkline points={points} ariaLabel={`${active} price sparkline`} />
    </div>
  );
}

function Sparkline({ points, ariaLabel }: { points: Point[]; ariaLabel: string }) {
  const values = points.map((pt) => {
    const n = pt.p ? Number(pt.p) : null;
    return n != null && Number.isFinite(n) ? n : null;
  });
  const finite = values.filter((v): v is number => v != null);
  if (finite.length < 2) return null;
  const min = Math.min(...finite);
  const max = Math.max(...finite);
  const range = max - min || 1;
  const W = 600;
  const H = 60;
  const segments: string[] = [];
  let pen = false;
  values.forEach((v, i) => {
    const x = (i / (values.length - 1)) * W;
    if (v == null) {
      pen = false;
      return;
    }
    const y = H - ((v - min) / range) * H;
    segments.push(`${pen ? 'L' : 'M'} ${x.toFixed(1)} ${y.toFixed(1)}`);
    pen = true;
  });
  const last = finite[finite.length - 1];
  const first = finite[0];
  const tone =
    last >= first
      ? 'stroke-emerald-500 dark:stroke-emerald-400'
      : 'stroke-rose-500 dark:stroke-rose-400';
  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      className="h-12 w-full"
      role="img"
      aria-label={ariaLabel}
    >
      <path d={segments.join(' ')} fill="none" strokeWidth="1.5" className={tone} />
    </svg>
  );
}
