'use client';

import { useMemo } from 'react';
import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, ExternalLink } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { apiGet, asExample, API_BASE_URL } from '@/api/client';
import { formatCompact } from '@/lib/format';
import { CopyHash, relativeAge, formatTimestamp } from '../../explorer-shared';
import { categoryTone } from '../registry';

// ─── Wire shapes (mirror internal/api/v1/protocols.go ProtocolDetailView) ───

interface CompletenessView {
  complete: boolean;
  watermark_ledger: number;
}

interface ProtocolContract {
  contract_id: string;
  factory_id?: string;
  first_ledger?: number;
  token0?: string;
  token1?: string;
  kind?: 'factory' | 'instance';
  events?: number;
  last_seen?: string;
}

interface EventTypeCount {
  event_type: string;
  count: number;
}

interface ActivityPoint {
  date: string; // YYYY-MM-DD
  events: number;
}

interface ProtocolDetail {
  name: string;
  category: string;
  description: string;
  genesis_ledger: number;
  factories: string[];
  contract_count: number;
  events_24h: number;
  completeness?: CompletenessView;
  contracts: ProtocolContract[];
  event_kinds: string[];
  verification_page?: string;
  // Lake analytics (omitempty when the lake reader is down).
  event_breakdown?: EventTypeCount[];
  activity_series?: ActivityPoint[];
  activity_window_days?: number;
  events_total?: number;
}

/**
 * ProtocolView — the per-protocol analytics page. Fetches
 * /v1/protocols/{name} and renders header → KPIs → activity chart →
 * event-type breakdown → contract roster → footer. The lake-analytics
 * fields (breakdown / series / window / total) are omitempty on the
 * wire when the lake reader is down; every section degrades gracefully
 * to "analytics unavailable" while the registry + contract roster still
 * render from the always-served halves.
 */
export function ProtocolView({ name, label }: { name: string; label: string }) {
  const { data, isLoading, isError, error } = useQuery<ProtocolDetail>({
    queryKey: ['/v1/protocols/{name}', name],
    retry: false,
    staleTime: 60_000,
    queryFn: async () => {
      const env = await apiGet<{ data: ProtocolDetail }>(
        `/v1/protocols/${encodeURIComponent(name)}`,
      );
      return env.data;
    },
  });

  const source = asExample(`/v1/protocols/${name}`);

  if (isError) {
    return (
      <Shell name={name} label={label}>
        <Panel
          title="Couldn't load this protocol"
          source={source}
          bodyClassName="text-sm text-slate-600 dark:text-slate-400"
        >
          <p>
            The protocol directory is unreachable right now:{' '}
            {error instanceof Error ? error.message : 'unknown error'}. Retry, or
            check{' '}
            <a
              href="https://status.stellarindex.io"
              target="_blank"
              rel="noopener noreferrer"
              className="underline-offset-2 hover:underline"
            >
              status.stellarindex.io
            </a>
            .
          </p>
        </Panel>
      </Shell>
    );
  }

  if (isLoading || !data) {
    return (
      <Shell name={name} label={label}>
        <Panel title={label} source={source} bodyClassName="text-sm text-slate-500">
          Loading on-chain analytics…
        </Panel>
      </Shell>
    );
  }

  const analyticsAvailable =
    data.activity_window_days != null && data.activity_window_days > 0;
  const windowDays = data.activity_window_days ?? 0;

  return (
    <Shell name={name} label={label}>
      {/* ── Header ── */}
      <header className="space-y-3 border-b border-slate-200 pb-5 dark:border-slate-800">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="text-3xl font-semibold tracking-tight">{label}</h1>
          <CategoryChip category={data.category} />
          <CompletenessBadge completeness={data.completeness} />
        </div>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          {data.description}
        </p>
      </header>

      {/* ── KPI row ── */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <Kpi label="Contracts" value={formatCompact(data.contract_count)} />
        <Kpi
          label={`events · last ${windowDays || '—'}d`}
          value={
            analyticsAvailable && data.events_total != null
              ? formatCompact(data.events_total)
              : '—'
          }
        />
        <Kpi label="events · 24h" value={formatCompact(data.events_24h)} />
        <Kpi label="Factories" value={formatCompact(data.factories.length)} />
        <Kpi
          label="Genesis ledger"
          value={`#${data.genesis_ledger.toLocaleString()}`}
          mono
        />
      </div>

      {/* ── Activity chart ── */}
      <Panel
        title={`On-chain activity (events/day, last ${windowDays || ''}d)`}
        hint="Decoded contract events per day across every contract + factory the protocol owns, from the certified lake."
        source={source}
      >
        {!analyticsAvailable ? (
          <AnalyticsUnavailable />
        ) : (data.activity_series?.length ?? 0) === 0 ? (
          <EmptyAnalytics text="No on-chain activity in the window." />
        ) : (
          <ActivityChart series={data.activity_series ?? []} />
        )}
      </Panel>

      {/* ── Event-type breakdown ── */}
      <Panel
        title="Event-type breakdown"
        hint={
          analyticsAvailable
            ? `Every decoded event type and how often it fired, last ${windowDays}d.`
            : undefined
        }
        source={source}
      >
        {!analyticsAvailable ? (
          <AnalyticsUnavailable />
        ) : (data.event_breakdown?.length ?? 0) === 0 ? (
          <EmptyAnalytics text="No decoded events in the window." />
        ) : (
          <EventBreakdown
            breakdown={data.event_breakdown ?? []}
            total={data.events_total ?? 0}
          />
        )}
      </Panel>

      {/* ── Contract roster ── */}
      <ContractRoster
        contracts={data.contracts}
        analyticsAvailable={analyticsAvailable}
        source={source}
      />

      {/* ── Footer ── */}
      <Footer data={data} name={name} />
    </Shell>
  );
}

// ─── Shell ───────────────────────────────────────────────────────────────

function Shell({
  name,
  label,
  children,
}: {
  name: string;
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <nav className="text-xs text-slate-500">
        <Link
          href="/protocols"
          className="inline-flex items-center gap-1 hover:text-brand-600"
        >
          <ArrowLeft className="h-3 w-3" aria-hidden />
          All protocols
        </Link>{' '}
        / <span className="text-slate-700 dark:text-slate-300">{label || name}</span>
      </nav>
      {children}
    </div>
  );
}

// ─── Header chips ──────────────────────────────────────────────────────────

function CategoryChip({ category }: { category: string }) {
  return (
    <span
      className={`rounded px-2 py-0.5 font-mono text-xs uppercase tracking-wider ${categoryTone(category)}`}
    >
      {category}
    </span>
  );
}

function CompletenessBadge({
  completeness,
}: {
  completeness?: CompletenessView;
}) {
  if (!completeness) {
    return (
      <span
        className="rounded bg-slate-100 px-2 py-0.5 text-[11px] uppercase tracking-wider text-slate-500 dark:bg-slate-800 dark:text-slate-400"
        title="No completeness verdict recorded for this source yet."
      >
        Coverage unknown
      </span>
    );
  }
  if (completeness.complete) {
    return (
      <span
        className="rounded bg-emerald-100 px-2 py-0.5 text-[11px] font-medium uppercase tracking-wider text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200"
        title={`Verified complete to ledger #${completeness.watermark_ledger.toLocaleString()} (ADR-0033 substrate + recognition + projection reconcile).`}
      >
        ✓ Verified complete
      </span>
    );
  }
  return (
    <span
      className="rounded bg-amber-100 px-2 py-0.5 text-[11px] font-medium uppercase tracking-wider text-amber-800 dark:bg-amber-900/40 dark:text-amber-200"
      title={`Partial coverage to ledger #${completeness.watermark_ledger.toLocaleString()}.`}
    >
      Partial coverage
    </span>
  );
}

// ─── KPI card ───────────────────────────────────────────────────────────────

function Kpi({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="rounded-lg border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900">
      <div className="text-[10px] uppercase tracking-wider text-slate-500">
        {label}
      </div>
      <div
        className={`mt-1 text-xl tabular-nums ${mono ? 'font-mono text-base' : 'font-semibold'}`}
      >
        {value}
      </div>
    </div>
  );
}

// ─── Activity chart (hand-rolled SVG area chart, no deps) ────────────────────

// Chart geometry — module-level constants so the useMemo dep array can
// reference them without ESLint flagging a recreated object literal.
const CHART_W = 1000;
const CHART_H = 240;
const CHART_PAD = { top: 12, right: 8, bottom: 22, left: 8 };

function ActivityChart({ series }: { series: ActivityPoint[] }) {
  const geom = useMemo(() => {
    const pad = CHART_PAD;
    const n = series.length;
    const max = series.reduce((m, p) => Math.max(m, p.events), 0);
    const innerW = CHART_W - pad.left - pad.right;
    const innerH = CHART_H - pad.top - pad.bottom;
    // x position of point i (center of its slot).
    const x = (i: number) =>
      pad.left + (n <= 1 ? innerW / 2 : (i / (n - 1)) * innerW);
    const y = (v: number) =>
      pad.top + innerH - (max <= 0 ? 0 : (v / max) * innerH);

    const linePts = series.map((p, i) => `${x(i)},${y(p.events)}`).join(' ');
    const areaPath =
      n === 0
        ? ''
        : `M ${x(0)},${pad.top + innerH} ` +
          series.map((p, i) => `L ${x(i)},${y(p.events)}`).join(' ') +
          ` L ${x(n - 1)},${pad.top + innerH} Z`;

    const total = series.reduce((s, p) => s + p.events, 0);
    const avg = n > 0 ? total / n : 0;
    return { max, x, y, linePts, areaPath, total, avg, innerH };
  }, [series]);

  // Sparse x-axis ticks: first, middle, last dates.
  const ticks = useMemo(() => {
    if (series.length === 0) return [] as { i: number; label: string }[];
    const idxs = Array.from(
      new Set([0, Math.floor((series.length - 1) / 2), series.length - 1]),
    );
    return idxs.map((i) => ({ i, label: shortDate(series[i].date) }));
  }, [series]);

  const peak = series.reduce(
    (best, p) => (p.events > best.events ? p : best),
    series[0],
  );
  const ariaLabel = `Daily on-chain event activity: ${series.length} days, ${formatCompact(
    geom.total,
  )} events total, peak ${formatCompact(peak.events)} on ${peak.date}.`;

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1 text-xs text-slate-500">
        <span>
          Peak{' '}
          <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
            {formatCompact(peak.events)}
          </span>{' '}
          on {peak.date}
        </span>
        <span>
          Avg/day{' '}
          <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
            {formatCompact(Math.round(geom.avg))}
          </span>
        </span>
      </div>
      <svg
        viewBox={`0 0 ${CHART_W} ${CHART_H}`}
        preserveAspectRatio="none"
        className="h-56 w-full"
        role="img"
        aria-label={ariaLabel}
      >
        <defs>
          <linearGradient id="protoActivityFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="rgb(16 185 129)" stopOpacity="0.28" />
            <stop offset="100%" stopColor="rgb(16 185 129)" stopOpacity="0" />
          </linearGradient>
        </defs>
        {/* baseline */}
        <line
          x1={CHART_PAD.left}
          y1={CHART_PAD.top + geom.innerH}
          x2={CHART_W - CHART_PAD.right}
          y2={CHART_PAD.top + geom.innerH}
          stroke="rgb(148 163 184 / 0.3)"
          strokeWidth={1}
        />
        {geom.areaPath && <path d={geom.areaPath} fill="url(#protoActivityFill)" />}
        {geom.linePts && (
          <polyline
            points={geom.linePts}
            fill="none"
            stroke="rgb(5 150 105)"
            strokeWidth={2}
            strokeLinejoin="round"
            strokeLinecap="round"
            vectorEffect="non-scaling-stroke"
          />
        )}
        {ticks.map((t) => (
          <text
            key={t.i}
            x={geom.x(t.i)}
            y={CHART_H - 6}
            textAnchor={t.i === 0 ? 'start' : t.i === series.length - 1 ? 'end' : 'middle'}
            className="fill-slate-400"
            style={{ fontSize: 11, fontFamily: 'var(--font-sans)' }}
          >
            {t.label}
          </text>
        ))}
      </svg>
    </div>
  );
}

// ─── Event-type breakdown (centerpiece) ──────────────────────────────────────

function EventBreakdown({
  breakdown,
  total,
}: {
  breakdown: EventTypeCount[];
  total: number;
}) {
  const max = breakdown.reduce((m, b) => Math.max(m, b.count), 0);
  return (
    <ul className="space-y-2.5">
      {breakdown.map((b) => {
        const pct = total > 0 ? (b.count / total) * 100 : 0;
        const barPct = max > 0 ? (b.count / max) * 100 : 0;
        return (
          <li key={b.event_type}>
            <div className="mb-1 flex items-baseline justify-between gap-3 text-xs">
              <span
                className="truncate font-mono text-slate-700 dark:text-slate-300"
                title={b.event_type}
              >
                {b.event_type}
              </span>
              <span className="shrink-0 tabular-nums text-slate-500">
                <span className="font-mono text-slate-700 dark:text-slate-300">
                  {formatCompact(b.count)}
                </span>{' '}
                · {pct.toFixed(pct >= 10 ? 0 : 1)}%
              </span>
            </div>
            <div
              className="h-2.5 overflow-hidden rounded-full bg-slate-100 dark:bg-slate-800"
              role="img"
              aria-label={`${b.event_type}: ${b.count} events, ${pct.toFixed(1)}% of total`}
            >
              <div
                className="h-full rounded-full bg-brand-500 dark:bg-brand-400"
                style={{ width: `${Math.max(barPct, 1.5)}%` }}
              />
            </div>
          </li>
        );
      })}
    </ul>
  );
}

// ─── Contract roster ─────────────────────────────────────────────────────────

function ContractRoster({
  contracts,
  analyticsAvailable,
  source,
}: {
  contracts: ProtocolContract[];
  analyticsAvailable: boolean;
  source: ReturnType<typeof asExample>;
}) {
  const { factories, instances } = useMemo(() => {
    const f = contracts.filter((c) => c.kind === 'factory');
    const i = contracts
      .filter((c) => c.kind !== 'factory')
      .sort((a, b) => (b.events ?? 0) - (a.events ?? 0));
    return { factories: f, instances: i };
  }, [contracts]);

  const hasTokens = contracts.some((c) => c.token0 || c.token1);

  if (contracts.length === 0) {
    return (
      <Panel
        title="Contract roster"
        source={source}
        bodyClassName="text-sm text-slate-500"
      >
        This source has no contract registry — it&apos;s either a classic-protocol
        venue (SDEX), an event-less oracle, or a bridge tracked without a
        factory model.
      </Panel>
    );
  }

  return (
    <Panel
      title={`Contract roster (${contracts.length})`}
      hint={`${factories.length} factories · ${instances.length} instances${analyticsAvailable ? ' · events over the analytics window' : ''}`}
      source={source}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <thead>
            <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
              <th scope="col" className="px-4 py-2">
                Role
              </th>
              <th scope="col" className="px-4 py-2">
                Contract
              </th>
              {hasTokens && (
                <th scope="col" className="px-4 py-2">
                  Pair
                </th>
              )}
              <th scope="col" className="px-4 py-2 text-right">
                Events
              </th>
              <th scope="col" className="px-4 py-2 text-right">
                Last seen
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {[...factories, ...instances].map((c) => (
              <tr
                key={c.contract_id}
                className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
              >
                <td className="px-4 py-2">
                  <RoleChip kind={c.kind} />
                </td>
                <td className="px-4 py-2">
                  <Link
                    href={`/contract?id=${encodeURIComponent(c.contract_id)}`}
                    className="text-brand-600 hover:underline"
                  >
                    <CopyHash value={c.contract_id} head={8} tail={6} />
                  </Link>
                </td>
                {hasTokens && (
                  <td className="px-4 py-2">
                    {c.token0 || c.token1 ? (
                      <span className="font-mono text-[11px] text-slate-500">
                        {shortId(c.token0)} / {shortId(c.token1)}
                      </span>
                    ) : (
                      <span className="text-slate-300 dark:text-slate-700">—</span>
                    )}
                  </td>
                )}
                <td className="px-4 py-2 text-right font-mono tabular-nums text-slate-600 dark:text-slate-400">
                  {c.events != null && c.events > 0 ? (
                    formatCompact(c.events)
                  ) : (
                    <span className="text-slate-300 dark:text-slate-700">
                      {analyticsAvailable ? '0' : '—'}
                    </span>
                  )}
                </td>
                <td className="px-4 py-2 text-right">
                  {c.last_seen ? (
                    <span
                      className="font-mono text-xs text-slate-500"
                      title={formatTimestamp(c.last_seen)}
                    >
                      {relativeAge(c.last_seen)}
                    </span>
                  ) : (
                    <span className="text-slate-300 dark:text-slate-700">—</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

function RoleChip({ kind }: { kind?: string }) {
  if (kind === 'factory') {
    return (
      <span className="rounded bg-amber-100 px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wider text-amber-800 dark:bg-amber-900/40 dark:text-amber-200">
        factory
      </span>
    );
  }
  return (
    <span className="rounded bg-slate-100 px-1.5 py-0.5 text-[9px] uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-400">
      instance
    </span>
  );
}

// ─── Footer ──────────────────────────────────────────────────────────────────

function Footer({ data, name }: { data: ProtocolDetail; name: string }) {
  return (
    <Panel title="Protocol identity" bodyClassName="space-y-4">
      {data.factories.length > 0 && (
        <div>
          <div className="mb-1.5 text-[10px] uppercase tracking-wider text-slate-500">
            Verified factories ({data.factories.length})
          </div>
          <ul className="flex flex-wrap gap-2">
            {data.factories.map((f) => (
              <li key={f}>
                <Link
                  href={`/contract?id=${encodeURIComponent(f)}`}
                  className="inline-flex items-center rounded border border-slate-200 px-2 py-1 font-mono text-[11px] text-brand-600 hover:border-brand-500 hover:underline dark:border-slate-700"
                >
                  {shortId(f)}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}

      {data.event_kinds.length > 0 && (
        <div>
          <div className="mb-1.5 text-[10px] uppercase tracking-wider text-slate-500">
            Decoder event vocabulary
          </div>
          <ul className="flex flex-wrap gap-1.5">
            {data.event_kinds.map((k) => (
              <li
                key={k}
                className="rounded-full bg-slate-100 px-2 py-0.5 font-mono text-[10px] text-slate-600 dark:bg-slate-800 dark:text-slate-300"
              >
                {k}
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="flex flex-wrap gap-x-6 gap-y-2 border-t border-slate-200 pt-3 text-xs dark:border-slate-800">
        {data.verification_page && (
          <a
            href={`https://github.com/StellarIndex/stellar-index/blob/main/${data.verification_page}`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-brand-600 hover:underline"
          >
            Verification write-up
            <ExternalLink className="h-3 w-3" aria-hidden />
          </a>
        )}
        <a
          href={`${API_BASE_URL}/v1/protocols/${encodeURIComponent(name)}`}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 text-slate-500 hover:text-brand-600"
        >
          Raw API (/v1/protocols/{name})
          <ExternalLink className="h-3 w-3" aria-hidden />
        </a>
      </div>
    </Panel>
  );
}

// ─── Empty / unavailable states ──────────────────────────────────────────────

function AnalyticsUnavailable() {
  return (
    <p className="py-6 text-center text-sm text-slate-500">
      Lake analytics unavailable — the certified-lake reader is currently
      unreachable. The contract registry below is served independently and is
      unaffected.
    </p>
  );
}

function EmptyAnalytics({ text }: { text: string }) {
  return <p className="py-6 text-center text-sm text-slate-500">{text}</p>;
}

// ─── helpers ─────────────────────────────────────────────────────────────────

function shortId(id?: string): string {
  if (!id) return '—';
  if (id.length <= 14) return id;
  return `${id.slice(0, 6)}…${id.slice(-4)}`;
}

function shortDate(iso: string): string {
  // YYYY-MM-DD → "MMM D" (UTC, no Date parse ambiguity).
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
  if (!m) return iso;
  const months = [
    'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
    'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
  ];
  const mon = months[Number(m[2]) - 1] ?? m[2];
  return `${mon} ${Number(m[3])}`;
}
