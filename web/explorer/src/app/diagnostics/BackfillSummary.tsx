'use client';

import { useMemo } from 'react';

import { useCursors, type Cursor } from '@/api/hooks';

/**
 * BackfillSummary — sibling card to HealthSummary, surfacing
 * backfill workers separately. Live cursors track current ingest;
 * backfill cursors track historical replay. Mixing them in the
 * same panel was confusing — this splits the two.
 *
 * Backfill cursors have `source === 'backfill'` and a
 * `sub_source` of `<from>-<to>:<source-list>`. We surface:
 *   - Active workers (lag < 1h — "still progressing")
 *   - Total tracked ranges
 *   - Slowest active worker (max lag in active set)
 *   - Furthest-along active worker (max last_ledger)
 *
 * The same /v1/diagnostics/cursors call powers this and
 * HealthSummary, so adding this card costs no extra round trip.
 */
export function BackfillSummary() {
  const { data, isLoading } = useCursors();

  const summary = useMemo(() => computeSummary(data ?? []), [data]);

  if (isLoading || !data) {
    return (
      <section className="rounded-md border border-line bg-surface p-4 text-sm text-ink-muted">
        Loading backfill summary…
      </section>
    );
  }

  if (summary.totalWorkers === 0) {
    return (
      <section className="rounded-md border border-line bg-surface p-4 text-sm text-ink-muted">
        No backfill cursors recorded.
      </section>
    );
  }

  return (
    <section className="grid grid-cols-2 gap-3 md:grid-cols-4">
      <Cell
        label="Active workers"
        value={summary.activeWorkers.toString()}
        sub={`of ${summary.totalWorkers} tracked`}
        tone={summary.activeWorkers > 0 ? 'ok' : undefined}
      />
      <Cell
        label="Slowest active"
        value={fmtLag(summary.slowestActiveLag)}
        sub="max lag in active set"
      />
      <Cell
        label="Furthest along"
        value={
          summary.furthestLedger != null
            ? `#${summary.furthestLedger.toLocaleString()}`
            : '—'
        }
        sub="max ledger reached"
        mono
      />
      <Cell
        label="Backfill ranges"
        value={summary.uniqueRanges.toString()}
        sub="distinct shards"
      />
    </section>
  );
}

interface Summary {
  totalWorkers: number;
  activeWorkers: number;
  slowestActiveLag: number | null;
  furthestLedger: number | null;
  uniqueRanges: number;
}

function computeSummary(cursors: Cursor[]): Summary {
  const bf = cursors.filter((c) => c.source === 'backfill');
  const active = bf.filter(
    (c) => Number.isFinite(c.lag_seconds) && c.lag_seconds < 3600,
  );
  const slowest = active.length
    ? active.reduce((m, c) => (c.lag_seconds > m ? c.lag_seconds : m), 0)
    : null;
  const furthest = bf.reduce(
    (m, c) =>
      Number.isFinite(c.last_ledger) && c.last_ledger > m ? c.last_ledger : m,
    -1,
  );
  // Distinct shards: parse the `<from>-<to>` prefix off sub_source.
  const ranges = new Set<string>();
  for (const c of bf) {
    if (!c.sub_source) continue;
    const ix = c.sub_source.indexOf(':');
    ranges.add(ix === -1 ? c.sub_source : c.sub_source.slice(0, ix));
  }
  return {
    totalWorkers: bf.length,
    activeWorkers: active.length,
    slowestActiveLag: slowest,
    furthestLedger: furthest >= 0 ? furthest : null,
    uniqueRanges: ranges.size,
  };
}

function fmtLag(s: number | null): string {
  if (s == null) return '—';
  if (s < 60) return `${s.toFixed(0)}s`;
  if (s < 3600) return `${(s / 60).toFixed(1)}m`;
  return `${(s / 3600).toFixed(1)}h`;
}

type Tone = 'ok' | 'warn' | 'bad' | undefined;

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
