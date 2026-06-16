'use client';

import { useMemo } from 'react';

import { useCursors, type Cursor } from '@/api/hooks';

/**
 * HealthSummary — top-of-page aggregate health card on
 * /diagnostics. Computes:
 *   - Number of unique live sources (ie. excluding `backfill`)
 *   - Median lag across live cursors (p50)
 *   - Worst lag across live cursors (p99-equivalent on small-N)
 *   - Highest live ledger across all sources
 *
 * Pulls /v1/diagnostics/cursors via useCursors (refreshed every
 * 15s by the hook), so the summary stays in sync with the
 * per-cursor table beneath it.
 */
export function HealthSummary() {
  const { data, isLoading } = useCursors();

  const summary = useMemo(() => computeSummary(data ?? []), [data]);

  if (isLoading || !data) {
    return (
      <section className="rounded-md border border-line bg-surface p-4 text-sm text-ink-muted">
        Loading health summary…
      </section>
    );
  }

  return (
    <section className="grid grid-cols-2 gap-3 md:grid-cols-4">
      <Cell
        label="Live sources"
        value={summary.liveSources.toString()}
        sub={`of ${summary.totalSources} total`}
      />
      <Cell
        label="Median lag"
        value={fmtLag(summary.medianLagSeconds)}
        sub="across live cursors"
        tone={lagTone(summary.medianLagSeconds)}
      />
      <Cell
        label="Worst lag"
        value={fmtLag(summary.worstLagSeconds)}
        sub="slowest live cursor"
        tone={lagTone(summary.worstLagSeconds)}
      />
      <Cell
        label="Live tip"
        value={
          summary.liveTipLedger != null
            ? `#${summary.liveTipLedger.toLocaleString()}`
            : '—'
        }
        sub="highest live ledger"
        mono
      />
    </section>
  );
}

interface Summary {
  totalSources: number;
  liveSources: number;
  medianLagSeconds: number | null;
  worstLagSeconds: number | null;
  liveTipLedger: number | null;
}

function computeSummary(cursors: Cursor[]): Summary {
  const live = cursors.filter((c) => c.source !== 'backfill');
  const lags = live
    .map((c) => c.lag_seconds)
    .filter((n) => Number.isFinite(n) && n >= 0)
    .sort((a, b) => a - b);
  const median = lags.length
    ? lags[Math.floor(lags.length / 2)] ?? null
    : null;
  const worst = lags.length ? (lags[lags.length - 1] ?? null) : null;
  const tip = live.reduce(
    (max, c) => (Number.isFinite(c.last_ledger) && c.last_ledger > max ? c.last_ledger : max),
    -1,
  );
  const uniqueLive = new Set(live.map((c) => c.source)).size;
  const uniqueAll = new Set(cursors.map((c) => c.source)).size;
  return {
    totalSources: uniqueAll,
    liveSources: uniqueLive,
    medianLagSeconds: median,
    worstLagSeconds: worst,
    liveTipLedger: tip >= 0 ? tip : null,
  };
}

function fmtLag(s: number | null): string {
  if (s == null) return '—';
  if (s < 60) return `${s.toFixed(0)}s`;
  if (s < 3600) return `${(s / 60).toFixed(1)}m`;
  return `${(s / 3600).toFixed(1)}h`;
}

type Tone = 'ok' | 'warn' | 'bad' | undefined;

function lagTone(s: number | null): Tone {
  if (s == null) return undefined;
  if (s <= 60) return 'ok';
  if (s <= 600) return 'warn';
  return 'bad';
}

function Cell({
  label,
  value,
  sub,
  tone,
  mono,
}: {
  label: string;
  value: string;
  sub?: string;
  tone?: Tone;
  mono?: boolean;
}) {
  const valueClass =
    tone === 'ok'
      ? 'text-up'
      : tone === 'warn'
        ? 'text-warn-700'
        : tone === 'bad'
          ? 'text-down'
          : '';
  return (
    <div className="rounded-md border border-line bg-surface p-3">
      <div className="text-[10px] uppercase tracking-wider text-ink-muted">
        {label}
      </div>
      <div
        className={`mt-1 truncate text-lg tabular-nums ${mono ? 'font-mono' : 'font-semibold'} ${valueClass}`}
        title={value}
      >
        {value}
      </div>
      {sub && <div className="mt-0.5 text-[11px] text-ink-muted">{sub}</div>}
    </div>
  );
}
