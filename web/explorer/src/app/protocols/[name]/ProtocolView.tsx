'use client';

import { useMemo, useState } from 'react';
import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, ExternalLink } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { apiGet, asExample, API_BASE_URL } from '@/api/client';
import { formatCompact } from '@/lib/format';
import { CopyHash, relativeAge, formatTimestamp } from '../../explorer-shared';
import { categoryTone } from '../registry';
import { TimeSeriesChart } from './TimeSeriesChart';
import { BespokeSection, type Bespoke } from './BespokeSection';

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
  // Bespoke per-category analytics block (omitempty — absent when no
  // bespoke reader is wired or the category has none yet).
  bespoke?: Bespoke;
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
          bodyClassName="text-sm text-ink-body"
        >
          <p>
            The protocol directory is unreachable right now:{' '}
            {error instanceof Error ? error.message : 'unknown error'}. Retry, or
            check{' '}
            <a
              href="/status"
              target="_blank"
              rel="noopener noreferrer"
              className="underline-offset-2 hover:underline"
            >
              the status page
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
        <Panel title={label} source={source} bodyClassName="text-sm text-ink-muted">
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
      <header className="space-y-3 border-b border-line pb-5">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="text-3xl font-semibold tracking-tight">{label}</h1>
          <CategoryChip category={data.category} />
          <CompletenessBadge completeness={data.completeness} />
        </div>
        <p className="max-w-3xl text-sm text-ink-body">
          {data.description}
        </p>
        <AtAGlance data={data} analyticsAvailable={analyticsAvailable} windowDays={windowDays} />
        <ProtocolCrossLinks name={name} category={data.category} />
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

      {/* ── Bespoke per-category analytics (the headline block) ── */}
      {data.bespoke && <BespokeSection bespoke={data.bespoke} source={source} />}

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
          <TimeSeriesChart
            points={(data.activity_series ?? []).map((p) => ({
              date: p.date,
              value: p.events,
            }))}
            label="Daily on-chain events"
            unit="events"
            tone="emerald"
            gradientId="protoActivityFill"
          />
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

// ─── Cross-links ───────────────────────────────────────────────────────────
// Sources with a dedicated DEX detail page (/dexes/[source]).
const DEX_PAGES = new Set(['soroswap', 'phoenix', 'aquarius', 'sdex', 'comet']);

function ProtocolCrossLinks({ name, category }: { name: string; category: string }) {
  const isDex = category === 'dex' || category === 'amm';
  return (
    <div className="flex flex-wrap gap-3 pt-1 text-xs">
      <Link href={`/sources/${encodeURIComponent(name)}`} className="text-brand-600 hover:underline">
        Source registry →
      </Link>
      {isDex && DEX_PAGES.has(name) && (
        <Link href={`/dexes/${encodeURIComponent(name)}`} className="text-brand-600 hover:underline">
          Pools &amp; chart →
        </Link>
      )}
      {category === 'lending' && (
        <Link href="/lending" className="text-brand-600 hover:underline">
          Lending pools →
        </Link>
      )}
      {category === 'oracle' && (
        <Link href="/oracles" className="text-brand-600 hover:underline">
          Oracle feeds →
        </Link>
      )}
    </div>
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
      <nav className="text-xs text-ink-muted">
        <Link
          href="/protocols"
          className="inline-flex items-center gap-1 hover:text-brand-600"
        >
          <ArrowLeft className="h-3 w-3" aria-hidden />
          All protocols
        </Link>{' '}
        / <span className="text-ink-body">{label || name}</span>
      </nav>
      {children}
    </div>
  );
}

// ─── Header chips ──────────────────────────────────────────────────────────

function CategoryChip({ category }: { category: string }) {
  return (
    <span
      className={`rounded-sm px-2 py-0.5 font-mono text-xs uppercase tracking-wider ${categoryTone(category)}`}
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
        className="rounded-sm bg-surface-subtle px-2 py-0.5 text-[11px] uppercase tracking-wider text-ink-muted"
        title="No completeness verdict recorded for this source yet."
      >
        Coverage unknown
      </span>
    );
  }
  if (completeness.complete) {
    return (
      <span
        className="rounded-sm bg-up-subtle px-2 py-0.5 text-[11px] font-medium uppercase tracking-wider text-up-strong"
        title={`Verified complete to ledger #${completeness.watermark_ledger.toLocaleString()} (ADR-0033 substrate + recognition + projection reconcile).`}
      >
        ✓ Verified complete
      </span>
    );
  }
  return (
    <span
      className="rounded-sm bg-warn-50 px-2 py-0.5 text-[11px] font-medium uppercase tracking-wider text-warn-700"
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
    <div className="rounded-lg border border-line bg-surface p-3">
      <div className="text-[10px] uppercase tracking-wider text-ink-muted">
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

// ─── At-a-glance summary line ────────────────────────────────────────────────

// A compact one-liner above the KPI cards that reads like a sentence — the
// fastest scannable read of "what is this protocol doing right now."
function AtAGlance({
  data,
  analyticsAvailable,
  windowDays,
}: {
  data: ProtocolDetail;
  analyticsAvailable: boolean;
  windowDays: number;
}) {
  const topEvent = useMemo(() => {
    if (!data.event_breakdown || data.event_breakdown.length === 0) return null;
    return data.event_breakdown.reduce((best, b) =>
      b.count > best.count ? b : best,
    );
  }, [data.event_breakdown]);

  const bits: React.ReactNode[] = [];
  bits.push(
    <Glance key="contracts" label={formatCompact(data.contract_count)} unit="contracts" />,
  );
  if (data.factories.length > 0) {
    bits.push(
      <Glance key="factories" label={String(data.factories.length)} unit={data.factories.length === 1 ? 'factory' : 'factories'} />,
    );
  }
  if (analyticsAvailable && data.events_total != null) {
    bits.push(
      <Glance key="events" label={formatCompact(data.events_total)} unit={`events · ${windowDays}d`} />,
    );
  }
  bits.push(
    <Glance key="e24" label={formatCompact(data.events_24h)} unit="events · 24h" />,
  );
  if (topEvent) {
    bits.push(
      <Glance key="top" label={topEvent.event_type} unit="busiest event" mono />,
    );
  }

  return (
    <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-ink-muted">
      {bits.map((b, i) => (
        <span key={i} className="flex items-center gap-x-3">
          {i > 0 && (
            <span aria-hidden className="text-ink-faint">
              ·
            </span>
          )}
          {b}
        </span>
      ))}
    </p>
  );
}

function Glance({
  label,
  unit,
  mono,
}: {
  label: string;
  unit: string;
  mono?: boolean;
}) {
  return (
    <span>
      <span
        className={`tabular-nums text-ink-body ${mono ? 'font-mono' : 'font-semibold'}`}
      >
        {label}
      </span>{' '}
      {unit}
    </span>
  );
}

// ─── Event-type breakdown (centerpiece, top-N + expander) ────────────────────

// How many bars to show before collapsing behind a "+N more" expander.
const EVENT_BREAKDOWN_TOP_N = 8;

function EventBreakdown({
  breakdown,
  total,
}: {
  breakdown: EventTypeCount[];
  total: number;
}) {
  const [expanded, setExpanded] = useState(false);
  const max = breakdown.reduce((m, b) => Math.max(m, b.count), 0);
  const overflow = breakdown.length - EVENT_BREAKDOWN_TOP_N;
  const visible =
    expanded || overflow <= 0
      ? breakdown
      : breakdown.slice(0, EVENT_BREAKDOWN_TOP_N);

  return (
    <div className="space-y-3">
      <ul className="space-y-2.5">
        {visible.map((b) => {
          const pct = total > 0 ? (b.count / total) * 100 : 0;
          const barPct = max > 0 ? (b.count / max) * 100 : 0;
          return (
            <li key={b.event_type}>
              <div className="mb-1 flex items-baseline justify-between gap-3 text-xs">
                <span
                  className="truncate font-mono text-ink-body"
                  title={b.event_type}
                >
                  {b.event_type}
                </span>
                <span className="shrink-0 tabular-nums text-ink-muted">
                  <span className="font-mono text-ink-body">
                    {formatCompact(b.count)}
                  </span>{' '}
                  · {pct.toFixed(pct >= 10 ? 0 : 1)}%
                </span>
              </div>
              <div
                className="h-2.5 overflow-hidden rounded-full bg-surface-subtle"
                role="img"
                aria-label={`${b.event_type}: ${b.count} events, ${pct.toFixed(1)}% of total`}
              >
                <div
                  className="h-full rounded-full bg-brand-500 motion-safe:transition-[width]"
                  style={{ width: `${Math.max(barPct, 1.5)}%` }}
                />
              </div>
            </li>
          );
        })}
      </ul>
      {overflow > 0 && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          aria-expanded={expanded}
          className="rounded-sm text-xs font-medium text-brand-600 hover:underline focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-brand-500/60"
        >
          {expanded ? 'Show fewer' : `+${overflow} more event ${overflow === 1 ? 'type' : 'types'}`}
        </button>
      )}
    </div>
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
  const [sortKey, setSortKey] = useState<'events' | 'last_seen'>('events');
  const [expanded, setExpanded] = useState(false);

  const { factories, instances } = useMemo(() => {
    const f = contracts.filter((c) => c.kind === 'factory');
    const cmp =
      sortKey === 'events'
        ? (a: ProtocolContract, b: ProtocolContract) =>
            (b.events ?? 0) - (a.events ?? 0)
        : (a: ProtocolContract, b: ProtocolContract) =>
            (b.last_seen ?? '').localeCompare(a.last_seen ?? '');
    const i = contracts.filter((c) => c.kind !== 'factory').slice().sort(cmp);
    return { factories: f, instances: i };
  }, [contracts, sortKey]);

  const hasTokens = contracts.some((c) => c.token0 || c.token1);

  // Long instance lists collapse to the top slice behind a "+N more" expander.
  // Factories always render (there are few and they anchor the protocol).
  const visibleInstances =
    expanded || instances.length <= ROSTER_TOP_N
      ? instances
      : instances.slice(0, ROSTER_TOP_N);
  const overflow = instances.length - visibleInstances.length;

  if (contracts.length === 0) {
    return (
      <Panel
        title="Contract roster"
        source={source}
        bodyClassName="text-sm text-ink-muted"
      >
        This source has no contract registry — it&apos;s either a classic-protocol
        venue (SDEX), an event-less oracle, or a bridge tracked without a
        factory model.
      </Panel>
    );
  }

  // Header cell for a sortable column — a real <button> with aria-sort on the
  // <th>, so screen readers announce the active sort + the toggle is operable.
  const SortHeader = ({
    label,
    keyName,
  }: {
    label: string;
    keyName: 'events' | 'last_seen';
  }) => {
    const active = sortKey === keyName;
    return (
      <th
        scope="col"
        className="px-4 py-2 text-right"
        aria-sort={active ? 'descending' : 'none'}
      >
        <button
          type="button"
          onClick={() => setSortKey(keyName)}
          className={`ml-auto flex items-center gap-1 rounded-sm uppercase tracking-wider focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-brand-500/60 ${active ? 'text-brand-600' : 'hover:text-ink-body'}`}
        >
          {label}
          <span aria-hidden className="text-[8px]">
            {active ? '▼' : '↕'}
          </span>
        </button>
      </th>
    );
  };

  return (
    <Panel
      title={`Contract roster (${contracts.length})`}
      hint={`${factories.length} ${factories.length === 1 ? 'factory' : 'factories'} · ${instances.length} instances${analyticsAvailable ? ' · events over the analytics window' : ''}`}
      source={source}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[10px] uppercase tracking-wider text-ink-muted">
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
              <SortHeader label="Events" keyName="events" />
              <SortHeader label="Last seen" keyName="last_seen" />
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {[...factories, ...visibleInstances].map((c) => (
              <tr
                key={c.contract_id}
                className="hover:bg-surface-muted"
              >
                <td className="px-4 py-2">
                  <RoleChip kind={c.kind} />
                </td>
                <td className="px-4 py-2">
                  <Link
                    href={`/contracts/${encodeURIComponent(c.contract_id)}/`}
                    className="text-brand-600 hover:underline"
                  >
                    <CopyHash value={c.contract_id} head={8} tail={6} />
                  </Link>
                </td>
                {hasTokens && (
                  <td className="px-4 py-2">
                    {c.token0 || c.token1 ? (
                      <span className="font-mono text-[11px] text-ink-muted">
                        {shortId(c.token0)} / {shortId(c.token1)}
                      </span>
                    ) : (
                      <span className="text-ink-faint">—</span>
                    )}
                  </td>
                )}
                <td className="px-4 py-2 text-right font-mono tabular-nums text-ink-body">
                  {c.events != null && c.events > 0 ? (
                    formatCompact(c.events)
                  ) : (
                    <span className="text-ink-faint">
                      {analyticsAvailable ? '0' : '—'}
                    </span>
                  )}
                </td>
                <td className="px-4 py-2 text-right">
                  {c.last_seen ? (
                    <span
                      className="font-mono text-xs text-ink-muted"
                      title={formatTimestamp(c.last_seen)}
                    >
                      {relativeAge(c.last_seen)}
                    </span>
                  ) : (
                    <span className="text-ink-faint">—</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {(overflow > 0 || expanded) && instances.length > ROSTER_TOP_N && (
        <div className="px-4 pt-3">
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            aria-expanded={expanded}
            className="rounded-sm text-xs font-medium text-brand-600 hover:underline focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-brand-500/60"
          >
            {expanded
              ? 'Show fewer'
              : `+${overflow} more ${overflow === 1 ? 'instance' : 'instances'}`}
          </button>
        </div>
      )}
    </Panel>
  );
}

// Contract-roster instances shown before the "+N more" expander collapses
// the long tail.
const ROSTER_TOP_N = 25;

function RoleChip({ kind }: { kind?: string }) {
  if (kind === 'factory') {
    return (
      <span className="rounded-sm bg-warn-50 px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wider text-warn-700">
        factory
      </span>
    );
  }
  return (
    <span className="rounded-sm bg-surface-subtle px-1.5 py-0.5 text-[9px] uppercase tracking-wider text-ink-body">
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
          <div className="mb-1.5 text-[10px] uppercase tracking-wider text-ink-muted">
            Verified factories ({data.factories.length})
          </div>
          <ul className="flex flex-wrap gap-2">
            {data.factories.map((f) => (
              <li key={f}>
                <Link
                  href={`/contracts/${encodeURIComponent(f)}/`}
                  className="inline-flex items-center rounded-sm border border-line px-2 py-1 font-mono text-[11px] text-brand-600 hover:border-brand-500 hover:underline"
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
          <div className="mb-1.5 text-[10px] uppercase tracking-wider text-ink-muted">
            Decoder event vocabulary
          </div>
          <ul className="flex flex-wrap gap-1.5">
            {data.event_kinds.map((k) => (
              <li
                key={k}
                className="rounded-full bg-surface-subtle px-2 py-0.5 font-mono text-[10px] text-ink-body"
              >
                {k}
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="flex flex-wrap gap-x-6 gap-y-2 border-t border-line pt-3 text-xs">
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
          className="inline-flex items-center gap-1 text-ink-muted hover:text-brand-600"
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
    <p className="py-6 text-center text-sm text-ink-muted">
      Lake analytics unavailable — the certified-lake reader is currently
      unreachable. The contract registry below is served independently and is
      unaffected.
    </p>
  );
}

function EmptyAnalytics({ text }: { text: string }) {
  return <p className="py-6 text-center text-sm text-ink-muted">{text}</p>;
}

// ─── helpers ─────────────────────────────────────────────────────────────────

function shortId(id?: string): string {
  if (!id) return '—';
  if (id.length <= 14) return id;
  return `${id.slice(0, 6)}…${id.slice(-4)}`;
}
