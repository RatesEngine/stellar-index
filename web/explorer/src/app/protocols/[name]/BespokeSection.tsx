'use client';

import Link from 'next/link';

import { Panel } from '@/components/reveal';
import type { RequestExample } from '@/api/client';
import { formatCompact } from '@/lib/format';
import { CopyHash } from '../../explorer-shared';
import { TimeSeriesChart, type ChartTone } from './TimeSeriesChart';

// ─── Wire shapes (mirror internal/api/v1/protocols.go ProtocolBespoke) ───
//
// A GENERIC rendering container — KPIs + named time-series + named top-N
// tables + notes — filled bespoke per category server-side. The UI renders
// the three shapes generically, so a new category's metrics are a server data
// change, not a UI layout change. Every sub-part is optional and renders only
// when present (graceful degradation).

export interface BespokeKpi {
  label: string;
  value: string; // PRE-FORMATTED server-side (ADR-0003-safe).
  unit?: string;
  hint?: string;
}

export interface BespokeSeriesPt {
  date: string; // YYYY-MM-DD
  value: string; // numeric string — Number() for geometry only.
}

export interface BespokeSeries {
  name: string;
  unit?: string;
  points: BespokeSeriesPt[];
}

export interface BespokeTable {
  title: string;
  columns: string[];
  rows: string[][];
}

export interface Bespoke {
  category: string;
  kpis?: BespokeKpi[];
  series?: BespokeSeries[];
  tables?: BespokeTable[];
  notes?: string[];
}

// category → headline label + chart palette. Falls through to a neutral
// "Protocol analytics" so an unknown server-side category still renders.
const CATEGORY_LABEL: Record<string, string> = {
  dex: 'DEX analytics',
  amm: 'AMM analytics',
  lending: 'Lending analytics',
  vault: 'Vault analytics',
  yield: 'Vault analytics',
  bridge: 'Bridge analytics',
  oracle: 'Oracle analytics',
};

// Rotating chart tones so multiple series on one page read distinctly.
const SERIES_TONES: ChartTone[] = [
  'emerald',
  'brand',
  'violet',
  'amber',
  'indigo',
];

function categoryLabel(category: string): string {
  return CATEGORY_LABEL[category] ?? 'Protocol analytics';
}

/**
 * BespokeSection — the category-specific, Dune-surpassing analytics block.
 * Renders ONLY the sub-parts the server populated; the whole section is
 * omitted upstream when `data.bespoke` is absent. Distinct visual treatment
 * (gradient header band + accent KPI cards) so it reads as the protocol's
 * tailored headline rather than the generic activity panels around it.
 */
export function BespokeSection({
  bespoke,
  source,
}: {
  bespoke: Bespoke;
  source: RequestExample;
}) {
  const hasKpis = (bespoke.kpis?.length ?? 0) > 0;
  const hasSeries = (bespoke.series?.length ?? 0) > 0;
  const hasTables = (bespoke.tables?.length ?? 0) > 0;
  const hasNotes = (bespoke.notes?.length ?? 0) > 0;

  // Nothing to show beyond the heading → don't render an empty band.
  if (!hasKpis && !hasSeries && !hasTables && !hasNotes) return null;

  return (
    <section
      aria-labelledby="bespoke-heading"
      className="space-y-4 rounded-xl border border-brand-100 bg-gradient-to-b from-brand-50/60 to-transparent p-4 dark:border-brand-900/40 dark:from-brand-900/10"
    >
      {/* ── Category chip + heading ── */}
      <div className="flex flex-wrap items-center gap-2">
        <span className="rounded bg-brand-100 px-2 py-0.5 font-mono text-[11px] uppercase tracking-wider text-brand-700 dark:bg-brand-900/40 dark:text-brand-200">
          {bespoke.category}
        </span>
        <h2
          id="bespoke-heading"
          className="text-lg font-semibold tracking-tight"
        >
          {categoryLabel(bespoke.category)}
        </h2>
        <span className="text-xs text-slate-500">
          tailored on-chain metrics for this protocol
        </span>
      </div>

      {/* ── Bespoke KPI cards ── */}
      {hasKpis && (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
          {bespoke.kpis!.map((k, i) => (
            <BespokeKpiCard key={`${k.label}-${i}`} kpi={k} />
          ))}
        </div>
      )}

      {/* ── Bespoke charts (one Panel per named series) ── */}
      {hasSeries &&
        bespoke.series!.map((s, i) => (
          <Panel
            key={`${s.name}-${i}`}
            title={s.name}
            hint={s.unit ? `Units: ${s.unit}` : undefined}
            source={source}
          >
            <TimeSeriesChart
              points={s.points.map((p) => ({
                date: p.date,
                value: toNumber(p.value),
              }))}
              label={s.name}
              unit={s.unit}
              tone={SERIES_TONES[i % SERIES_TONES.length]}
              gradientId={`bespokeSeries-${i}`}
            />
          </Panel>
        ))}

      {/* ── Bespoke tables (one Panel per named top-N table) ── */}
      {hasTables &&
        bespoke.tables!.map((t, i) => (
          <BespokeTablePanel key={`${t.title}-${i}`} table={t} source={source} />
        ))}

      {/* ── Notes / caveats ── */}
      {hasNotes && (
        <ul className="space-y-1 px-1 text-xs text-slate-500 dark:text-slate-400">
          {bespoke.notes!.map((n, i) => (
            <li key={i} className="flex gap-1.5">
              <span aria-hidden className="select-none text-slate-400">
                ·
              </span>
              <span>{n}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// ─── Bespoke KPI card — accent styling, distinct from the generic KPI row ──

function BespokeKpiCard({ kpi }: { kpi: BespokeKpi }) {
  return (
    <div className="rounded-lg border border-brand-200/70 bg-white/80 p-3 shadow-sm dark:border-brand-900/40 dark:bg-slate-900/60">
      <div
        className="flex items-center gap-1 text-[10px] uppercase tracking-wider text-slate-500"
        title={kpi.hint || undefined}
      >
        <span className="truncate">{kpi.label}</span>
        {kpi.hint && (
          <span
            aria-hidden
            className="cursor-help text-slate-400"
            title={kpi.hint}
          >
            ⓘ
          </span>
        )}
      </div>
      <div className="mt-1 flex items-baseline gap-1">
        <span className="text-2xl font-semibold tabular-nums text-brand-700 dark:text-brand-200">
          {kpi.value}
        </span>
        {kpi.unit && (
          <span className="text-xs font-medium text-slate-500">{kpi.unit}</span>
        )}
      </div>
      {kpi.hint && (
        <p className="mt-1 line-clamp-2 text-[11px] leading-snug text-slate-400">
          {kpi.hint}
        </p>
      )}
    </div>
  );
}

// ─── Bespoke table — scannable, mono+linked contract ids, right-aligned nums ─

function BespokeTablePanel({
  table,
  source,
}: {
  table: BespokeTable;
  source: RequestExample;
}) {
  // Decide alignment per column: right-align a column if every non-empty cell
  // in it looks numeric (so $-formatted / count columns sit flush-right).
  const numericCols = table.columns.map((_, ci) =>
    table.rows.every((r) => {
      const cell = r[ci];
      return cell == null || cell === '' || looksNumeric(cell);
    }),
  );

  return (
    <Panel title={table.title} source={source} bodyClassName="-mx-4">
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <thead>
            <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
              {table.columns.map((c, ci) => (
                <th
                  key={ci}
                  scope="col"
                  className={`px-4 py-2 ${numericCols[ci] ? 'text-right' : ''}`}
                >
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {table.rows.length === 0 ? (
              <tr>
                <td
                  colSpan={table.columns.length}
                  className="px-4 py-6 text-center text-sm text-slate-500"
                >
                  No rows in the window.
                </td>
              </tr>
            ) : (
              table.rows.map((row, ri) => (
                <tr
                  key={ri}
                  className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
                >
                  {table.columns.map((_, ci) => (
                    <td
                      key={ci}
                      className={`px-4 py-2 ${
                        numericCols[ci]
                          ? 'text-right font-mono tabular-nums text-slate-600 dark:text-slate-400'
                          : ''
                      }`}
                    >
                      <Cell value={row[ci] ?? ''} />
                    </td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

// Cell — renders entity ids as mono, copyable, click-through links into the
// explorer (contracts → /contract, accounts → /accounts); shortens any other
// long id (e.g. a CODE-ISSUER canonical asset) copyably; everything else plain.
// The store ships RAW ids — all shortening/linking is presentation, done here.
function Cell({ value }: { value: string }) {
  if (value === '' || value === '—') {
    return <span className="text-slate-300 dark:text-slate-700">—</span>;
  }
  if (isContractId(value)) {
    return (
      <Link
        href={`/contract?id=${encodeURIComponent(value)}`}
        className="text-brand-600 hover:underline"
      >
        <CopyHash value={value} head={8} tail={6} />
      </Link>
    );
  }
  if (isAccountId(value)) {
    return (
      <Link
        href={`/accounts?id=${encodeURIComponent(value)}`}
        className="text-brand-600 hover:underline"
      >
        <CopyHash value={value} head={8} tail={6} />
      </Link>
    );
  }
  // Long non-strkey id (e.g. "USDC-G…" canonical asset) — shorten + copy, no
  // link (no asset-by-id route). Short labels ("native", codes) pass through.
  if (value.length > 28) {
    return <CopyHash value={value} head={10} tail={6} />;
  }
  return <span>{value}</span>;
}

// ─── helpers ─────────────────────────────────────────────────────────────

// toNumber — parse a bespoke series string to a JS number for chart geometry
// only. Strips $ / , / % decoration the server may include so the shape still
// plots; non-finite (a label, an em-dash) → 0 so the chart stays continuous.
function toNumber(v: string): number {
  if (!v) return 0;
  const cleaned = v.replace(/[$,%\s]/g, '').replace(/[a-zA-Z]+$/, '');
  const n = Number(cleaned);
  return Number.isFinite(n) ? n : 0;
}

// isContractId — a Soroban C-strkey: starts with 'C', 56 chars, base32 body.
function isContractId(v: string): boolean {
  return v.length === 56 && v[0] === 'C' && /^[A-Z2-7]+$/.test(v);
}

// isAccountId — a Stellar G-strkey account address: 'G', 56 chars, base32.
function isAccountId(v: string): boolean {
  return v.length === 56 && v[0] === 'G' && /^[A-Z2-7]+$/.test(v);
}

// looksNumeric — a cell is "numeric" for alignment if, once common money/count
// decoration is stripped, it parses as a finite number. Keeps "$1.2M", "1,234",
// "12.5%", "—" all classed sensibly.
function looksNumeric(v: string): boolean {
  if (v === '—') return true;
  const cleaned = v.replace(/[$,%\s]/g, '');
  if (cleaned === '') return false;
  // Allow a compact-suffix figure ("1.2M", "3.4K") to count as numeric too.
  const compact = cleaned.replace(/[KMBTkmbt]$/, '');
  return Number.isFinite(Number(compact));
}

// Re-export so the host page can format the at-a-glance line consistently.
export { formatCompact };
