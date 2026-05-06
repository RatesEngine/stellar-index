'use client';

import { useMemo } from 'react';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { useSources, useCursors, type Source } from '@/api/hooks';

/**
 * Live sources directory backed by `/v1/sources`.
 *
 * Groups by `class` (exchange / aggregator / oracle / authority_sanity)
 * — only Class=exchange sources contribute to VWAP by default; the
 * rest are reported alongside but excluded so we don't double-count
 * upstream markets or import their methodology. Per-source flags
 * (`include_in_vwap`, `paid`, `backfill_available`, `backfill_safe`)
 * surface as small pills next to the source name.
 */
export function SourcesTable() {
  const { data, isLoading, isError, error } = useSources();
  const cursors = useCursors();
  const grouped = useMemo(() => groupByClass(data ?? []), [data]);

  // Aggregate the cursors slice by source — one source can have
  // many cursors (live + per-range backfills). We surface the most
  // recently updated row's last_ledger + lag as the "latest tip"
  // per source.
  const latestBySource = useMemo(() => {
    const m = new Map<string, { last_ledger: number; lag_seconds: number; last_updated: string }>();
    for (const c of cursors.data ?? []) {
      const prev = m.get(c.source);
      if (!prev || prev.last_ledger < c.last_ledger) {
        m.set(c.source, {
          last_ledger: c.last_ledger,
          lag_seconds: c.lag_seconds,
          last_updated: c.last_updated,
        });
      }
    }
    return m;
  }, [cursors.data]);

  if (isError) {
    return (
      <Panel
        title="Sources"
        source={asExample('/v1/sources')}
        bodyClassName="text-sm text-down-strong"
      >
        Failed to load sources:{' '}
        {error instanceof Error ? error.message : 'unknown error'}
      </Panel>
    );
  }
  if (isLoading || !data) {
    return (
      <Panel
        title="Sources"
        source={asExample('/v1/sources')}
        bodyClassName="text-sm text-slate-500"
      >
        Loading…
      </Panel>
    );
  }
  if (data.length === 0) {
    return (
      <Panel
        title="Sources"
        source={asExample('/v1/sources')}
        bodyClassName="text-sm text-slate-500"
      >
        No sources registered.
      </Panel>
    );
  }

  return (
    <div className="space-y-4">
      {grouped.map(({ klass, rows }) => (
        <Panel
          key={klass}
          title={titleCase(klass)}
          hint={classHint(klass)}
          source={asExample('/v1/sources', { class: klass })}
          bodyClassName="-mx-4"
        >
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
                  <Th>Source</Th>
                  <Th>Subclass</Th>
                  <Th align="right">Default weight</Th>
                  <Th align="right">Last ingest</Th>
                  <Th align="right">Flags</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                {rows.map((s) => {
                  const cursor = latestBySource.get(s.name);
                  return (
                    <tr
                      key={s.name}
                      className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
                    >
                      <Td>
                        <span className="font-mono">{s.name}</span>
                      </Td>
                      <Td>
                        <span className="text-xs text-slate-500">
                          {s.subclass ?? '—'}
                        </span>
                      </Td>
                      <Td align="right">
                        <span className="font-mono tabular-nums">
                          {s.default_weight}
                        </span>
                      </Td>
                      <Td align="right">
                        <CursorAgo cursor={cursor} />
                      </Td>
                      <Td align="right">
                        <div className="flex flex-wrap justify-end gap-1">
                          {s.include_in_vwap && (
                            <Pill tone="up">in VWAP</Pill>
                          )}
                          {s.paid && <Pill tone="amber">paid</Pill>}
                          {s.backfill_available && !s.backfill_safe && (
                            <Pill tone="amber">backfill unaudited</Pill>
                          )}
                          {s.backfill_safe && (
                            <Pill tone="up">backfill safe</Pill>
                          )}
                          {!s.backfill_available && (
                            <Pill tone="slate">live-only</Pill>
                          )}
                        </div>
                      </Td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </Panel>
      ))}
    </div>
  );
}

function CursorAgo({
  cursor,
}: {
  cursor: { last_ledger: number; lag_seconds: number; last_updated: string } | undefined;
}) {
  if (!cursor) {
    return <span className="text-[11px] text-slate-400">—</span>;
  }
  // Tone bucket: <60s green, <10min amber, else red. Matches the
  // home System health panel's threshold so the two views agree
  // on what "live" means.
  const tone =
    cursor.lag_seconds < 60
      ? 'text-emerald-600 dark:text-emerald-400'
      : cursor.lag_seconds < 600
        ? 'text-amber-600 dark:text-amber-400'
        : 'text-rose-600 dark:text-rose-400';
  return (
    <div className="text-right">
      <div className={`font-mono text-[11px] ${tone}`}>
        {formatLag(cursor.lag_seconds)} ago
      </div>
      <div className="font-mono text-[10px] text-slate-400">
        #{cursor.last_ledger.toLocaleString()}
      </div>
    </div>
  );
}

function formatLag(s: number): string {
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.round(s / 60)}m`;
  if (s < 86400) return `${Math.round(s / 3600)}h`;
  return `${Math.round(s / 86400)}d`;
}

function Pill({
  tone,
  children,
}: {
  tone: 'up' | 'amber' | 'slate';
  children: React.ReactNode;
}) {
  const cls =
    tone === 'up'
      ? 'bg-up-soft text-up-strong'
      : tone === 'amber'
        ? 'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-200'
        : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400';
  return (
    <span
      className={`inline-block rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wider ${cls}`}
    >
      {children}
    </span>
  );
}

function Th({
  children,
  align,
}: {
  children: React.ReactNode;
  align?: 'left' | 'right';
}) {
  return (
    <th
      className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}
      scope="col"
    >
      {children}
    </th>
  );
}

function Td({
  children,
  align,
}: {
  children: React.ReactNode;
  align?: 'left' | 'right';
}) {
  return (
    <td
      className={`px-4 py-3 ${align === 'right' ? 'text-right' : 'text-left'}`}
    >
      {children}
    </td>
  );
}

function groupByClass(rows: Source[]): { klass: Source['class']; rows: Source[] }[] {
  const order: Source['class'][] = [
    'exchange',
    'aggregator',
    'oracle',
    'authority_sanity',
  ];
  const map = new Map<Source['class'], Source[]>();
  for (const r of rows) {
    const arr = map.get(r.class) ?? [];
    arr.push(r);
    map.set(r.class, arr);
  }
  const out: { klass: Source['class']; rows: Source[] }[] = [];
  for (const k of order) {
    const rs = map.get(k);
    if (rs && rs.length > 0) {
      rs.sort((a, b) => a.name.localeCompare(b.name));
      out.push({ klass: k, rows: rs });
    }
  }
  return out;
}

function titleCase(s: string): string {
  return s.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
}

function classHint(k: Source['class']): string {
  switch (k) {
    case 'exchange':
      return 'Contributes to VWAP by default';
    case 'aggregator':
      return 'Reported alongside; excluded from VWAP to avoid double-counting upstream markets';
    case 'oracle':
      return 'Reported alongside; excluded from VWAP to avoid importing their methodology';
    case 'authority_sanity':
      return 'Authority sanity check — divergence reference, never priced into VWAP';
  }
}
