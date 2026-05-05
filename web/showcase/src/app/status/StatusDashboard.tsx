'use client';

import {
  AlertTriangle,
  CheckCircle2,
  Clock,
  Database,
  Globe,
  type LucideIcon,
  Server,
  XCircle,
  Zap,
} from 'lucide-react';
import { useEffect, useState } from 'react';

import { API_BASE_URL, apiGet } from '@/api/client';

const REFRESH_INTERVAL_MS = 10_000;
const FRESHNESS_HARD_LIMIT_S = 30;   // RFP commitment
const FRESHNESS_SOFT_LIMIT_S = 60;   // amber band; everything above is red
const CURSOR_LAG_AMBER_S = 60;
const CURSOR_LAG_RED_S = 600;

type Health = 'up' | 'degraded' | 'down' | 'unknown';

type VersionEnvelope = {
  data: {
    version: string;
    commit: string;
    build_date: string;
    go_version: string;
  };
};

type PriceEnvelope = {
  data: {
    asset_id: string;
    quote: string;
    price: string;
    price_type: string;
    observed_at: string;
    window_seconds?: number;
  };
  flags: {
    stale: boolean;
    reduced_redundancy: boolean;
    triangulated: boolean;
    divergence_warning: boolean;
    frozen?: boolean;
    single_source?: boolean;
  };
  sources?: string[];
};

type Cursor = {
  source: string;
  sub_source?: string;
  last_ledger: number;
  updated_at: string;
};

type CursorsEnvelope = { data: Cursor[] };

type SourcesEnvelope = {
  data: {
    name: string;
    class: string;
    include_in_vwap: boolean;
  }[];
};

type DashboardData = {
  fetchedAt: Date;
  healthz: Health;
  version: VersionEnvelope['data'] | null;
  price: PriceEnvelope | null;
  priceErr: string | null;
  cursors: Cursor[];
  cursorsErr: string | null;
  sources: SourcesEnvelope['data'];
  sourcesErr: string | null;
};

// /v1/status — comprehensive system-health rollup. Backed by local
// Prometheus when configured; the showcase reads it for the SLA
// panel. Wire shape mirrors internal/api/v1/status.go.
type StatusEnvelope = {
  data: {
    overall: 'ok' | 'degraded' | 'down';
    region: { name: string; deployment: string };
    services: { name: string; status: 'ok' | 'down' | 'unknown'; last_seen?: string }[];
    latency: { p50_ms: number; p95_ms: number; p99_ms: number; window_secs: number };
    freshness: { last_aggregator_tick?: string; active_sources: number; total_sources: number };
    incidents: {
      active_count: number;
      page_count: number;
      ticket_count: number;
      informational_count: number;
      active?: {
        name: string;
        severity: 'page' | 'ticket' | 'informational';
        runbook_url?: string;
      }[];
    };
  };
  flags: { stale: boolean };
};

async function fetchAll(): Promise<DashboardData> {
  const fetchedAt = new Date();

  // /v1/healthz returns 200 + tiny body. We treat anything non-200 as down.
  let healthz: Health = 'unknown';
  try {
    const res = await fetch(`${API_BASE_URL}/v1/healthz`, {
      cache: 'no-store',
    });
    healthz = res.ok ? 'up' : 'down';
  } catch {
    healthz = 'down';
  }

  // Each of the others reports its own error so a single endpoint going
  // dark doesn't blank the whole page.
  const [version, price, cursors, sources] = await Promise.all([
    apiGet<VersionEnvelope>('/v1/version').then((v) => v.data).catch(() => null),
    apiGet<PriceEnvelope>('/v1/price', {
      asset: 'native',
      quote: 'fiat:USD',
    }).catch((e) => ({ __err: e instanceof Error ? e.message : 'unknown' })),
    apiGet<CursorsEnvelope>('/v1/diagnostics/cursors')
      .then((v) => v.data)
      .catch((e) => ({ __err: e instanceof Error ? e.message : 'unknown' })),
    apiGet<SourcesEnvelope>('/v1/sources')
      .then((v) => v.data)
      .catch((e) => ({ __err: e instanceof Error ? e.message : 'unknown' })),
  ]);

  return {
    fetchedAt,
    healthz,
    version,
    price: 'data' in (price as PriceEnvelope) ? (price as PriceEnvelope) : null,
    priceErr: '__err' in (price as object) ? (price as { __err: string }).__err : null,
    cursors: Array.isArray(cursors) ? cursors : [],
    cursorsErr:
      '__err' in (cursors as object) ? (cursors as { __err: string }).__err : null,
    sources: Array.isArray(sources) ? sources : [],
    sourcesErr:
      '__err' in (sources as object) ? (sources as { __err: string }).__err : null,
  };
}

function priceFreshness(price: PriceEnvelope | null): {
  health: Health;
  ageS: number | null;
  message: string;
} {
  if (!price) return { health: 'unknown', ageS: null, message: 'no data' };
  const observed = Date.parse(price.data.observed_at);
  if (Number.isNaN(observed)) return { health: 'unknown', ageS: null, message: 'malformed timestamp' };
  const ageMs = Date.now() - observed;
  const ageS = Math.round(ageMs / 1000);
  let health: Health;
  if (ageS <= FRESHNESS_HARD_LIMIT_S) {
    health = 'up';
  } else if (ageS <= FRESHNESS_SOFT_LIMIT_S) {
    health = 'degraded';
  } else {
    health = 'down';
  }
  return {
    health,
    ageS,
    message: `${ageS}s old (RFP target ≤ ${FRESHNESS_HARD_LIMIT_S}s)`,
  };
}

function cursorLag(updatedAt: string): { ageS: number; health: Health } {
  const t = Date.parse(updatedAt);
  if (Number.isNaN(t)) return { ageS: -1, health: 'unknown' };
  const ageS = Math.max(0, Math.round((Date.now() - t) / 1000));
  let health: Health;
  if (ageS <= CURSOR_LAG_AMBER_S) health = 'up';
  else if (ageS <= CURSOR_LAG_RED_S) health = 'degraded';
  else health = 'down';
  return { ageS, health };
}

function aggregate(data: DashboardData): Health {
  const samples: Health[] = [data.healthz];
  const fresh = priceFreshness(data.price);
  samples.push(fresh.health);
  for (const c of data.cursors) samples.push(cursorLag(c.updated_at).health);
  if (samples.includes('down')) return 'down';
  if (samples.includes('degraded')) return 'degraded';
  if (samples.every((s) => s === 'up')) return 'up';
  return 'unknown';
}

const HEALTH_CHIP: Record<
  Health,
  { label: string; classes: string; Icon: LucideIcon }
> = {
  up: {
    label: 'Operational',
    classes:
      'bg-emerald-50 text-emerald-800 border-emerald-200 dark:bg-emerald-900/30 dark:text-emerald-200 dark:border-emerald-900/50',
    Icon: CheckCircle2,
  },
  degraded: {
    label: 'Degraded',
    classes:
      'bg-amber-50 text-amber-800 border-amber-200 dark:bg-amber-900/30 dark:text-amber-200 dark:border-amber-900/50',
    Icon: AlertTriangle,
  },
  down: {
    label: 'Down',
    classes:
      'bg-rose-50 text-rose-800 border-rose-200 dark:bg-rose-900/30 dark:text-rose-200 dark:border-rose-900/50',
    Icon: XCircle,
  },
  unknown: {
    label: 'Unknown',
    classes:
      'bg-slate-50 text-slate-700 border-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:border-slate-700',
    Icon: Clock,
  },
};

function HealthChip({ health, large = false }: { health: Health; large?: boolean }) {
  const chip = HEALTH_CHIP[health];
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full border ${chip.classes} ${
        large ? 'px-3 py-1 text-sm font-semibold' : 'px-2 py-0.5 text-xs font-medium'
      }`}
    >
      <chip.Icon className={large ? 'h-4 w-4' : 'h-3.5 w-3.5'} />
      {chip.label}
    </span>
  );
}

export function StatusDashboard() {
  const [data, setData] = useState<DashboardData | null>(null);
  const [statusEnv, setStatusEnv] = useState<StatusEnvelope | null>(null);

  useEffect(() => {
    let cancelled = false;
    const tick = async () => {
      const d = await fetchAll();
      if (!cancelled) setData(d);
      try {
        const s = await apiGet<StatusEnvelope>('/v1/status');
        if (!cancelled) setStatusEnv(s);
      } catch {
        if (!cancelled) setStatusEnv(null);
      }
    };
    void tick();
    const timer = setInterval(tick, REFRESH_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, []);

  if (!data) {
    return (
      <div className="rounded-xl border border-slate-200 bg-white p-8 text-center text-sm text-slate-500 dark:border-slate-800 dark:bg-slate-900">
        Loading status…
      </div>
    );
  }

  const overall = aggregate(data);
  const fresh = priceFreshness(data.price);

  return (
    <div className="space-y-8">
      {/* Top-line: green / amber / red banner */}
      <header
        className={`rounded-xl border px-6 py-5 ${HEALTH_CHIP[overall].classes}`}
      >
        <div className="flex items-center justify-between gap-4">
          <div>
            <h1 className="text-2xl font-bold tracking-tight sm:text-3xl">
              {overall === 'up' && 'All systems operational'}
              {overall === 'degraded' && 'Degraded performance'}
              {overall === 'down' && 'Service disruption'}
              {overall === 'unknown' && 'Status unknown'}
            </h1>
            <p className="mt-1 text-sm opacity-90">
              Last checked {data.fetchedAt.toLocaleTimeString()}; refreshes every 10s
            </p>
          </div>
          <HealthChip health={overall} large />
        </div>
      </header>

      {/* Region grid */}
      <section>
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-slate-500">
          Regions
        </h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <RegionCard
            name="R1"
            location="Hetzner FSN1 (Falkenstein, DE)"
            health={data.healthz === 'up' && fresh.health !== 'down' ? 'up' : 'degraded'}
            description="Production. Galexie + indexer + aggregator + API."
          />
          <RegionCard
            name="R2"
            location="AWS us-east-1 (planned)"
            health="unknown"
            description="Provisioning blocked on L4.14. Will hybrid-mirror via aws-public-blockchain S3."
          />
          <RegionCard
            name="R3"
            location="Vultr Singapore (planned)"
            health="unknown"
            description="Provisioning blocked on L4.15. Will hybrid via Vultr Object Storage."
          />
        </div>
      </section>

      {/* SLA + live metrics from /v1/status. Hidden when the
          backend isn't wired (returns flags.stale=true). */}
      {statusEnv && !statusEnv.flags.stale && (
        <SLAMetricsPanel env={statusEnv} />
      )}

      {/* Subsystem panels */}
      <section>
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-slate-500">
          Subsystems
        </h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <SubsystemCard
            Icon={Globe}
            name="API"
            health={data.healthz}
            detail={
              data.healthz === 'up'
                ? `Serving from ${API_BASE_URL}`
                : 'Health probe failed'
            }
            extra={
              data.version
                ? `${data.version.version} (${data.version.commit.slice(0, 8)}, ${data.version.go_version})`
                : null
            }
          />
          <SubsystemCard
            Icon={Zap}
            name="Aggregator"
            health={fresh.health}
            detail={
              data.price
                ? `Latest XLM/USD: $${Number(data.price.data.price).toFixed(4)} from ${data.price.sources?.length ?? 0} source${data.price.sources?.length === 1 ? '' : 's'}`
                : data.priceErr ?? 'no data'
            }
            extra={fresh.message}
          />
          <SubsystemCard
            Icon={Database}
            name="Storage (TimescaleDB)"
            health={data.cursorsErr ? 'down' : 'up'}
            detail={
              data.cursorsErr
                ? `Diagnostics endpoint unreachable: ${data.cursorsErr}`
                : `${data.cursors.length} ingest cursors active`
            }
          />
          <SubsystemCard
            Icon={Server}
            name="Source registry"
            health={data.sourcesErr ? 'down' : 'up'}
            detail={
              data.sourcesErr
                ? data.sourcesErr
                : `${data.sources.length} sources registered, ${
                    data.sources.filter((s) => s.include_in_vwap).length
                  } contributing to VWAP`
            }
          />
        </div>
      </section>

      {/* Cursor lag table */}
      {data.cursors.length > 0 && (
        <section>
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-slate-500">
            Per-source ingest lag
          </h2>
          <div className="overflow-hidden rounded-xl border border-slate-200 dark:border-slate-800">
            <table className="min-w-full divide-y divide-slate-200 dark:divide-slate-800">
              <thead className="bg-slate-50 dark:bg-slate-800/50">
                <tr>
                  <th className="px-4 py-2 text-left text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
                    Source
                  </th>
                  <th className="px-4 py-2 text-left text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
                    Last ledger
                  </th>
                  <th className="px-4 py-2 text-left text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
                    Lag
                  </th>
                  <th className="px-4 py-2 text-left text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
                    Health
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-200 bg-white dark:divide-slate-800 dark:bg-slate-900">
                {data.cursors
                  .slice()
                  .sort((a, b) => a.source.localeCompare(b.source))
                  .map((c) => {
                    const lag = cursorLag(c.updated_at);
                    return (
                      <tr key={`${c.source}-${c.sub_source ?? ''}`}>
                        <td className="whitespace-nowrap px-4 py-2 text-sm font-medium text-slate-900 dark:text-slate-100">
                          {c.source}
                          {c.sub_source && (
                            <span className="ml-1 text-xs text-slate-500">
                              / {c.sub_source}
                            </span>
                          )}
                        </td>
                        <td className="whitespace-nowrap px-4 py-2 font-mono text-sm text-slate-700 dark:text-slate-300">
                          {c.last_ledger.toLocaleString()}
                        </td>
                        <td className="whitespace-nowrap px-4 py-2 font-mono text-sm text-slate-700 dark:text-slate-300">
                          {lag.ageS}s
                        </td>
                        <td className="whitespace-nowrap px-4 py-2">
                          <HealthChip health={lag.health} />
                        </td>
                      </tr>
                    );
                  })}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {/* Latency placeholder until /v1/status aggregates Prometheus */}
      <section className="rounded-xl border border-slate-200 bg-slate-50 p-5 text-sm text-slate-700 dark:border-slate-800 dark:bg-slate-800/50 dark:text-slate-300">
        <p className="font-semibold text-slate-900 dark:text-slate-100">
          Coming next
        </p>
        <ul className="mt-2 list-inside list-disc space-y-1">
          <li>p95 / p99 API latency strips (last 1h, 24h, 7d) — pending the Prometheus-fed <code className="font-mono">/v1/status</code> aggregator.</li>
          <li>Active alerts feed from Alertmanager.</li>
          <li>R2 / R3 cross-region consistency check (depends on multi-region bringup).</li>
          <li>Public RFP-SLA tracker (≥99.99% availability, p95 ≤ 200ms, p99 ≤ 500ms).</li>
        </ul>
      </section>

      <p className="text-xs text-slate-500 dark:text-slate-500">
        For incident history see the{' '}
        <a className="underline" href="https://github.com/RatesEngine/rates-engine/tree/main/docs/operations/incidents">
          incidents directory
        </a>{' '}
        on GitHub. SEV-1 / SEV-2 declarations also post to this page in
        real time once Alertmanager is wired (see <code className="font-mono">docs/architecture/status-page-hosting-comparison.md</code>).
      </p>
    </div>
  );
}

function RegionCard({
  name,
  location,
  health,
  description,
}: {
  name: string;
  location: string;
  health: Health;
  description: string;
}) {
  return (
    <div className="rounded-lg border border-slate-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900">
      <div className="mb-2 flex items-center justify-between gap-2">
        <h3 className="font-semibold text-slate-900 dark:text-slate-100">
          {name}
        </h3>
        <HealthChip health={health} />
      </div>
      <p className="text-xs text-slate-500 dark:text-slate-400">{location}</p>
      <p className="mt-2 text-sm text-slate-700 dark:text-slate-300">{description}</p>
    </div>
  );
}

function SubsystemCard({
  Icon,
  name,
  health,
  detail,
  extra,
}: {
  Icon: LucideIcon;
  name: string;
  health: Health;
  detail: string;
  extra?: string | null;
}) {
  return (
    <div className="rounded-lg border border-slate-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900">
      <div className="mb-2 flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Icon className="h-4 w-4 text-slate-500" />
          <h3 className="font-semibold text-slate-900 dark:text-slate-100">
            {name}
          </h3>
        </div>
        <HealthChip health={health} />
      </div>
      <p className="text-sm text-slate-700 dark:text-slate-300">{detail}</p>
      {extra && (
        <p className="mt-1 font-mono text-xs text-slate-500 dark:text-slate-400">
          {extra}
        </p>
      )}
    </div>
  );
}

function SLAMetricsPanel({ env }: { env: StatusEnvelope }) {
  const { latency, freshness, incidents } = env.data;

  // Freighter RFP § Performance SLAs:
  //   - p95 ≤ 200 ms (page on > 500)
  //   - p99 ≤ 500 ms (page on > 2000)
  // Match the per-window thresholds in deploy/monitoring/rules/api.yml.
  const p95Health: Health =
    latency.p95_ms === 0
      ? 'unknown'
      : latency.p95_ms <= 200
        ? 'up'
        : latency.p95_ms <= 500
          ? 'degraded'
          : 'down';

  const p99Health: Health =
    latency.p99_ms === 0
      ? 'unknown'
      : latency.p99_ms <= 500
        ? 'up'
        : latency.p99_ms <= 2000
          ? 'degraded'
          : 'down';

  const sourcesHealth: Health =
    freshness.total_sources === 0
      ? 'unknown'
      : freshness.active_sources >= freshness.total_sources
        ? 'up'
        : freshness.active_sources >= freshness.total_sources - 2
          ? 'degraded'
          : 'down';

  return (
    <section>
      <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-slate-500">
        SLA &amp; live metrics
      </h2>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <MetricCard
          label="API p50 latency"
          value={`${latency.p50_ms.toFixed(1)} ms`}
          sublabel="last 5 min"
          health="up"
        />
        <MetricCard
          label="API p95 latency"
          value={`${latency.p95_ms.toFixed(1)} ms`}
          sublabel="RFP target ≤ 200 ms"
          health={p95Health}
        />
        <MetricCard
          label="API p99 latency"
          value={`${latency.p99_ms.toFixed(1)} ms`}
          sublabel="RFP target ≤ 500 ms"
          health={p99Health}
        />
        <MetricCard
          label="Active ingest sources"
          value={`${freshness.active_sources} / ${freshness.total_sources}`}
          sublabel="emitting events in last 10 min"
          health={sourcesHealth}
        />
      </div>

      {incidents.active_count > 0 && (
        <div className="mt-3 rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900 dark:border-amber-900/40 dark:bg-amber-950/40 dark:text-amber-200">
          <div>
            <strong>{incidents.active_count} active incident{incidents.active_count > 1 ? 's' : ''}</strong>
            {' — '}
            {[
              incidents.page_count > 0 && `${incidents.page_count} page`,
              incidents.ticket_count > 0 && `${incidents.ticket_count} ticket`,
              incidents.informational_count > 0 && `${incidents.informational_count} informational`,
            ]
              .filter(Boolean)
              .join(', ')}
            .
          </div>
          {incidents.active && incidents.active.length > 0 && (
            <ul className="mt-2 space-y-1 font-mono text-xs">
              {incidents.active.map((a) => (
                <li key={a.name} className="flex items-center gap-2">
                  <span className={`inline-block h-2 w-2 rounded-full ${
                    a.severity === 'page'
                      ? 'bg-rose-500'
                      : a.severity === 'ticket'
                        ? 'bg-amber-500'
                        : 'bg-slate-400'
                  }`} />
                  <span>{a.name}</span>
                  <span className="text-amber-700 dark:text-amber-300">[{a.severity}]</span>
                  {a.runbook_url && (
                    <a
                      href={a.runbook_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="ml-auto text-amber-700 underline-offset-2 hover:underline dark:text-amber-300"
                    >
                      runbook →
                    </a>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  );
}

function MetricCard({
  label,
  value,
  sublabel,
  health,
}: {
  label: string;
  value: string;
  sublabel: string;
  health: Health;
}) {
  return (
    <div className="rounded-lg border border-slate-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-xs uppercase tracking-wider text-slate-500">
          {label}
        </span>
        <HealthChip health={health} />
      </div>
      <div className="font-mono text-2xl font-semibold text-slate-900 dark:text-slate-100">
        {value}
      </div>
      <div className="mt-1 text-xs text-slate-500 dark:text-slate-400">
        {sublabel}
      </div>
    </div>
  );
}
