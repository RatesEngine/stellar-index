'use client';

import { useMemo, useState } from 'react';
import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { SourceSparkline } from '@/components/SourceSparkline';
import { DonutChart } from '@/components/charts/DonutChart';
import { useSources, useCursors, isOnChainSource, type Source } from '@/api/hooks';

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
  // includeStats=true joins per-source 24h trade counts so the
  // table can show the most-active venues at the top of each
  // class group. The opt-in matches the public docs so any caller
  // using /v1/sources directly sees the same shape.
  const { data, isLoading, isError, error } = useSources(undefined, true, { sparkline: true });
  const cursors = useCursors();
  const [filter, setFilter] = useState('');

  // Stellar-only directory: keep on-chain venues (DEX, on-chain oracles,
  // lending, routers, bridges); drop off-chain reference feeds (CEX /
  // FX / aggregators / Chainlink) — those are the pricing layer, not
  // Stellar network activity, and live on /exchanges + /aggregators.
  const stellar = useMemo(() => (data ?? []).filter(isOnChainSource), [data]);

  const filteredData = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return stellar;
    return stellar.filter((s) => {
      const hay = `${s.name} ${s.class} ${s.subclass ?? ''}`.toLowerCase();
      return hay.includes(q);
    });
  }, [stellar, filter]);

  const grouped = useMemo(() => groupByClass(filteredData), [filteredData]);

  const classMix = useMemo(() => {
    const m = new Map<string, number>();
    for (const s of stellar) m.set(s.class, (m.get(s.class) ?? 0) + 1);
    return Array.from(m, ([label, value]) => ({ label: titleCase(label), value }));
  }, [stellar]);

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
        bodyClassName="text-sm text-ink-muted"
      >
        Loading…
      </Panel>
    );
  }
  if (stellar.length === 0) {
    return (
      <Panel
        title="Sources"
        source={asExample('/v1/sources')}
        bodyClassName="text-sm text-ink-muted"
      >
        No Stellar on-chain sources registered.
      </Panel>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3 text-xs">
        <input
          type="search"
          aria-label="Filter sources by name, class, or subclass"
          placeholder="Filter by source name, class, or subclass…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          className="w-72 rounded-md border border-line bg-surface px-2.5 py-1 text-xs placeholder:text-ink-faint focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
        />
        <span className="font-mono text-[11px] text-ink-muted">
          {filteredData.length} of {stellar.length} sources
          {filter && (
            <button
              type="button"
              onClick={() => setFilter('')}
              className="ml-2 text-brand-600 hover:underline"
            >
              clear
            </button>
          )}
        </span>
      </div>
      {!filter && classMix.length > 1 && (
        <Panel
          title="By class"
          hint="Stellar on-chain source composition — only exchange-class (DEX) contributes to VWAP"
          source={asExample('/v1/sources')}
        >
          <DonutChart data={classMix} centerLabel={String(stellar.length)} centerSub="sources" />
        </Panel>
      )}
      {filter && grouped.length === 0 && (
        <Panel
          title="Sources"
          source={asExample('/v1/sources')}
          bodyClassName="text-sm text-ink-muted"
        >
          No sources match &quot;{filter}&quot;.
        </Panel>
      )}
      {grouped.map(({ klass, rows }) => (
        <Panel
          key={klass}
          title={titleCase(klass)}
          hint={classHint(klass)}
          source={asExample('/v1/sources', { class: klass })}
          bodyClassName="-mx-4"
        >
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-line text-sm">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
                  <Th>Source</Th>
                  <Th>Subclass</Th>
                  <Th align="right">Default weight</Th>
                  <Th align="right">24h trades</Th>
                  <Th>24h chart</Th>
                  <Th align="right">Last ingest</Th>
                  <Th align="right">Flags</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line-subtle">
                {rows.map((s) => {
                  const cursor = latestBySource.get(s.name);
                  return (
                    <tr
                      key={s.name}
                      className="hover:bg-surface-muted"
                    >
                      <Td>
                        <Link
                          href={`/sources/${encodeURIComponent(s.name)}`}
                          className="font-mono hover:text-brand-600 hover:underline"
                        >
                          {s.name}
                        </Link>
                      </Td>
                      <Td>
                        <span className="text-xs text-ink-muted">
                          {s.subclass ?? '—'}
                        </span>
                      </Td>
                      <Td align="right">
                        <span className="font-mono tabular-nums">
                          {s.default_weight}
                        </span>
                      </Td>
                      <Td align="right">
                        {typeof s.trade_count_24h === 'number' &&
                        s.trade_count_24h > 0 ? (
                          <span className="font-mono tabular-nums text-ink-body">
                            {s.trade_count_24h.toLocaleString()}
                          </span>
                        ) : (
                          <span className="text-[11px] text-ink-faint">
                            —
                          </span>
                        )}
                      </Td>
                      <Td>
                        <SourceSparkline buckets={s.volume_history_24h} />
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
    return <span className="text-[11px] text-ink-faint">—</span>;
  }
  // Tone bucket: <60s green, <10min amber, else red. Matches the
  // home System health panel's threshold so the two views agree
  // on what "live" means.
  const tone =
    cursor.lag_seconds < 60
      ? 'text-up'
      : cursor.lag_seconds < 600
        ? 'text-warn-700'
        : 'text-down';
  return (
    <div className="text-right">
      <div className={`font-mono text-[11px] ${tone}`}>
        {formatLag(cursor.lag_seconds)} ago
      </div>
      <div className="font-mono text-[10px] text-ink-faint">
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
        ? 'bg-warn-50 text-warn-700'
        : 'bg-surface-subtle text-ink-body';
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
  // Includes the on-chain non-exchange classes (lending / router /
  // bridge) — without them, blend / cctp / rozo / defindex /
  // soroswap-router fell into the map but were never emitted. The
  // off-chain classes (aggregator / authority_sanity) stay in the
  // order for resilience but are filtered out upstream now.
  const order: Source['class'][] = [
    'exchange',
    'oracle',
    'lending',
    'router',
    'bridge',
    'aggregator',
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
      // 24h trade count desc (most-active venues first), with
      // alpha-by-name as the tiebreaker for venues that have no
      // recent activity. Falls back to alpha-only when the caller
      // didn't request stats.
      rs.sort((a, b) => {
        const ta = a.trade_count_24h ?? 0;
        const tb = b.trade_count_24h ?? 0;
        if (ta !== tb) return tb - ta;
        return a.name.localeCompare(b.name);
      });
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
      return 'On-chain DEX venues — contribute to VWAP by default';
    case 'oracle':
      return 'On-chain price feeds (Reflector / Band / Redstone) — reported alongside, excluded from VWAP';
    case 'lending':
      return 'On-chain lending — auction stress-prices reported alongside, excluded from VWAP';
    case 'router':
      return 'On-chain DEX routers + aggregator vaults — per-tx attribution, excluded from VWAP';
    case 'bridge':
      return 'Cross-chain bridges — flow coverage (no prices), excluded from VWAP';
    case 'aggregator':
      return 'Reported alongside; excluded from VWAP to avoid double-counting upstream markets';
    case 'authority_sanity':
      return 'Authority sanity check — divergence reference, never priced into VWAP';
  }
}
