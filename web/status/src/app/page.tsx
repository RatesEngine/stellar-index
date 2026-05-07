'use client';

import { useEffect, useMemo, useState } from 'react';
import {
  AlertTriangle,
  CheckCircle2,
  ExternalLink,
  Info,
  XCircle,
} from 'lucide-react';

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net';

// Polled every 30 s — same cadence as Healthchecks.io's hosted
// status pages and well inside the 60-s indexer/aggregator
// heartbeat budget so a real degradation lands within one poll.
const POLL_INTERVAL_MS = 30_000;

type ServiceStatus = 'ok' | 'degraded' | 'down' | 'unknown';

interface ServiceEntry {
  name: string;
  status: ServiceStatus;
  last_seen?: string;
}

interface IncidentEntry {
  name: string;
  severity: 'page' | 'ticket' | 'informational';
  runbook_url?: string;
  description?: string;
}

interface StatusResponse {
  overall: ServiceStatus;
  region: { name: string; deployment: string };
  services: ServiceEntry[];
  latency: {
    p50_ms: number;
    p95_ms: number;
    p99_ms: number;
    window_secs: number;
  };
  freshness: {
    last_aggregator_tick: string;
    active_sources: number;
    total_sources: number;
  };
  incidents: {
    active_count: number;
    page_count: number;
    ticket_count: number;
    informational_count: number;
    active: IncidentEntry[];
  };
}

interface Envelope {
  data: StatusResponse;
  as_of: string;
  flags: { stale?: boolean };
}

// Public-facing endpoints we surface on the status page.
// Not auto-derived from the OpenAPI spec because not every
// endpoint deserves a status row — operator surfaces (`/metrics`,
// `/v1/diagnostics/*`) clutter without adding signal.
//
// `probe` shapes how we hit the endpoint to render a real green/
// amber/red badge:
//   { kind: 'get', path: '…' }   — fetch the path verbatim
//   { kind: 'requires-auth' }    — show "auth req'd", no probe
//   { kind: 'streaming' }        — show "stream", no probe (SSE
//                                  open is heavy + blocks the
//                                  probe pool)
//
// Probe paths use minimal safe parameters where required (e.g.
// `?asset=native`, `?limit=1`) so each fetch returns a small
// payload and 200 means "the codepath is alive end-to-end".
type EndpointProbe =
  | { kind: 'get'; path: string }
  | { kind: 'requires-auth' }
  | { kind: 'streaming' };

interface PublicEndpoint {
  path: string;
  group: string;
  description: string;
  probe: EndpointProbe;
}

const PUBLIC_ENDPOINTS: PublicEndpoint[] = [
  {
    path: '/v1/healthz',
    group: 'Health',
    description: 'Liveness probe',
    probe: { kind: 'get', path: '/v1/healthz' },
  },
  {
    path: '/v1/readyz',
    group: 'Health',
    description: 'Readiness probe',
    probe: { kind: 'get', path: '/v1/readyz' },
  },
  {
    path: '/v1/price',
    group: 'Pricing',
    description: 'Current VWAP price for one asset',
    probe: { kind: 'get', path: '/v1/price?asset=native&quote=fiat:USD' },
  },
  {
    path: '/v1/price/batch',
    group: 'Pricing',
    description: 'Batch lookup, up to 1000 assets',
    probe: { kind: 'get', path: '/v1/price/batch?assets=native&quote=fiat:USD' },
  },
  {
    path: '/v1/price/tip',
    group: 'Pricing',
    description: 'Rolling-window tip price',
    probe: { kind: 'get', path: '/v1/price/tip?asset=native&quote=fiat:USD' },
  },
  {
    path: '/v1/price/stream',
    group: 'Pricing',
    description: 'Closed-bucket SSE stream',
    probe: { kind: 'streaming' },
  },
  {
    path: '/v1/vwap',
    group: 'Pricing',
    description: 'VWAP over a window',
    probe: { kind: 'get', path: '/v1/vwap?asset=native&quote=fiat:USD&window=1m' },
  },
  {
    path: '/v1/twap',
    group: 'Pricing',
    description: 'TWAP over a window',
    probe: { kind: 'get', path: '/v1/twap?asset=native&quote=fiat:USD&window=1m' },
  },
  {
    path: '/v1/ohlc',
    group: 'Pricing',
    description: 'OHLC bar',
    probe: { kind: 'get', path: '/v1/ohlc?asset=native&quote=fiat:USD&interval=1m' },
  },
  {
    path: '/v1/chart',
    group: 'Pricing',
    description: 'Multi-bar chart series',
    probe: { kind: 'get', path: '/v1/chart?asset=native&quote=fiat:USD&interval=1m&limit=1' },
  },
  {
    path: '/v1/history',
    group: 'Historical',
    description: 'Trade history within a window',
    probe: { kind: 'get', path: '/v1/history?asset=native&quote=fiat:USD&limit=1' },
  },
  {
    path: '/v1/observations',
    group: 'Historical',
    description: 'Per-source latest trade',
    probe: { kind: 'get', path: '/v1/observations?asset=native&quote=fiat:USD' },
  },
  {
    path: '/v1/coins',
    group: 'Catalogue',
    description: 'Asset directory (440K+ classic assets)',
    probe: { kind: 'get', path: '/v1/coins?limit=1' },
  },
  {
    path: '/v1/assets/{id}',
    group: 'Catalogue',
    description: 'Asset detail + supply + market cap',
    probe: { kind: 'get', path: '/v1/assets/native' },
  },
  {
    path: '/v1/markets',
    group: 'Catalogue',
    description: 'Trading pairs',
    probe: { kind: 'get', path: '/v1/markets?limit=1' },
  },
  {
    path: '/v1/issuers',
    group: 'Catalogue',
    description: 'Issuer directory',
    probe: { kind: 'get', path: '/v1/issuers?limit=1' },
  },
  {
    path: '/v1/sources',
    group: 'Catalogue',
    description: 'Per-venue source metadata',
    probe: { kind: 'get', path: '/v1/sources' },
  },
  {
    path: '/v1/oracle/latest',
    group: 'Oracle',
    description: 'Latest oracle readings',
    probe: { kind: 'get', path: '/v1/oracle/latest' },
  },
  {
    path: '/v1/oracle/lastprice',
    group: 'Oracle',
    description: 'SEP-40 lastprice',
    probe: { kind: 'get', path: '/v1/oracle/lastprice?asset=native' },
  },
  {
    path: '/v1/auth/login',
    group: 'Dashboard auth',
    description: 'Magic-link request',
    probe: { kind: 'requires-auth' },
  },
  {
    path: '/v1/auth/callback',
    group: 'Dashboard auth',
    description: 'Magic-link consume',
    probe: { kind: 'requires-auth' },
  },
  {
    path: '/v1/auth/sep10/challenge',
    group: 'API auth',
    description: 'SEP-10 challenge',
    probe: { kind: 'requires-auth' },
  },
];

// 5s probe budget — well under the 30s polling interval. Every
// public endpoint should serve a 200 response within this budget;
// crossing it gets the "slow" tone even on 200.
const PROBE_TIMEOUT_MS = 5_000;
const PROBE_SLOW_MS = 800;

// IncidentHistoryEntry is the shape the IncidentHistory section
// consumes. It's a deliberately UI-flat shape — the API returns
// a richer shape (severity codes, structured timestamps, etc.)
// which IncidentHistory normalises before render.
interface IncidentHistoryEntry {
  slug: string;
  date: string;
  title: string;
  resolved: string;
  summary: string;
  severity: 'major' | 'minor' | 'maintenance';
}

interface IncidentsAPIShape {
  data: {
    incidents: Array<{
      slug: string;
      title: string;
      severity: 'SEV-1' | 'SEV-2' | 'SEV-3';
      status: 'investigating' | 'identified' | 'monitoring' | 'resolved';
      started_at: string;
      resolved_at?: string | null;
      affected_components?: string[];
      body_markdown: string;
    }>;
    count: number;
  };
}

export default function StatusPage() {
  const [status, setStatus] = useState<StatusResponse | null>(null);
  const [asOf, setAsOf] = useState<string>('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [endpointHealth, setEndpointHealth] = useState<
    Record<string, EndpointProbeResult>
  >({});
  const [incidentHistory, setIncidentHistory] = useState<
    IncidentHistoryEntry[]
  >([]);

  useEffect(() => {
    let cancelled = false;
    async function poll() {
      try {
        const res = await fetch(`${API_BASE_URL}/v1/status`, {
          cache: 'no-store',
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const env: Envelope = await res.json();
        if (cancelled) return;
        setStatus(env.data);
        setAsOf(env.as_of);
        setLoading(false);
        setError(null);
      } catch (err) {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : 'Network error');
        setLoading(false);
      }
    }
    poll();
    const id = setInterval(poll, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  // Per-endpoint live probe. Fires every endpoint with a `get`
  // probe in parallel on mount and on each /v1/status poll, so
  // the matrix stays current with the rest of the page. Endpoints
  // marked `requires-auth` or `streaming` keep their static label
  // and never get a fetch fired against them.
  useEffect(() => {
    let cancelled = false;
    const probes = PUBLIC_ENDPOINTS.filter((e) => e.probe.kind === 'get').map(
      (e) => probeEndpoint(e),
    );
    function runOnce() {
      Promise.allSettled(probes.map((p) => p())).then((results) => {
        if (cancelled) return;
        const next: Record<string, EndpointProbeResult> = {};
        let i = 0;
        for (const ep of PUBLIC_ENDPOINTS) {
          if (ep.probe.kind === 'get') {
            const r = results[i++];
            next[ep.path] =
              r.status === 'fulfilled'
                ? r.value
                : { kind: 'error', latencyMs: -1 };
          } else {
            next[ep.path] = { kind: 'static', label: ep.probe.kind };
          }
        }
        setEndpointHealth(next);
      });
    }
    runOnce();
    const id = setInterval(runOnce, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  // Incident history is fetched once at mount from /v1/incidents.
  // The corpus is small (a handful of posts per year of operation)
  // and changes only on redeploy, so polling would be wasted work.
  useEffect(() => {
    let cancelled = false;
    fetch(`${API_BASE_URL}/v1/incidents`, { cache: 'no-store' })
      .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
      .then((env: IncidentsAPIShape) => {
        if (cancelled) return;
        const mapped = (env.data?.incidents ?? []).map(normaliseIncident);
        setIncidentHistory(mapped);
      })
      .catch(() => {
        // Silent failure — the panel renders the empty state.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const overallTone = useMemo(() => toneFor(status?.overall), [status]);

  return (
    <div className="mx-auto max-w-5xl space-y-8 px-6 py-10">
      <Header />
      <OverallBanner status={status?.overall ?? 'unknown'} tone={overallTone} />
      {error && (
        <div className="rounded-md border border-bad-500/30 bg-bad-50 px-4 py-3 text-sm text-bad-700">
          Status feed unreachable: {error}. Showing the last known snapshot.
        </div>
      )}
      {loading && !status && (
        <div className="rounded-md border border-surface-line bg-surface px-4 py-8 text-center text-sm text-ink-faint">
          Loading status…
        </div>
      )}
      {status && (
        <>
          <ServiceGrid services={status.services} />
          <LatencyStrip latency={status.latency} />
          <FreshnessRow freshness={status.freshness} />
          <ActiveIncidents incidents={status.incidents.active} />
          <EndpointMatrix
            endpoints={PUBLIC_ENDPOINTS}
            health={endpointHealth}
          />
          <IncidentHistory entries={incidentHistory} />
          <Footer asOf={asOf} region={status.region} />
        </>
      )}
    </div>
  );
}

function Header() {
  return (
    <header className="flex items-center justify-between">
      <div>
        <a href="https://ratesengine.net" className="text-sm text-ink-muted">
          ← Rates Engine
        </a>
        <h1 className="mt-2 text-3xl font-semibold tracking-tight">
          System status
        </h1>
      </div>
      <div className="hidden items-center gap-2 text-sm text-ink-muted sm:flex">
        <span className="live-dot inline-block h-2 w-2 rounded-full bg-ok-500" />
        Live · refreshed every 30 s
      </div>
    </header>
  );
}

function OverallBanner({
  status,
  tone,
}: {
  status: ServiceStatus;
  tone: ReturnType<typeof toneFor>;
}) {
  const headlines: Record<ServiceStatus, string> = {
    ok: 'All systems operational',
    degraded: 'Degraded performance',
    down: 'Major outage',
    unknown: 'Status unknown',
  };
  const subtitles: Record<ServiceStatus, string> = {
    ok: 'Every service is reporting healthy.',
    degraded:
      'One or more services are reporting degraded performance. The API is still serving but customers may notice slower responses or stale data.',
    down: 'A major component is down. Pricing endpoints are likely returning errors. We are investigating.',
    unknown:
      'We can’t reach the status feed. The page below is a last-known snapshot.',
  };
  const Icon = tone.icon;
  return (
    <section
      className={`flex items-start gap-4 rounded-lg border p-6 ${tone.bg} ${tone.border}`}
    >
      <Icon className={`h-8 w-8 flex-shrink-0 ${tone.fg}`} />
      <div>
        <h2 className={`text-xl font-semibold ${tone.fg}`}>
          {headlines[status]}
        </h2>
        <p className="mt-1 text-sm text-ink-muted">{subtitles[status]}</p>
      </div>
    </section>
  );
}

function ServiceGrid({ services }: { services: ServiceEntry[] }) {
  return (
    <section>
      <SectionHeader>Services</SectionHeader>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {services.map((svc) => (
          <ServiceCard key={svc.name} service={svc} />
        ))}
      </div>
    </section>
  );
}

function ServiceCard({ service }: { service: ServiceEntry }) {
  const tone = toneFor(service.status);
  const Icon = tone.icon;
  return (
    <div
      className={`flex items-start justify-between rounded-md border bg-surface p-4 ${tone.border}`}
    >
      <div>
        <div className="font-medium capitalize text-ink">{service.name}</div>
        {service.last_seen && (
          <div className="mt-1 text-xs text-ink-faint">
            Last seen {timeSince(service.last_seen)} ago
          </div>
        )}
      </div>
      <Icon className={`h-5 w-5 ${tone.fg}`} />
    </div>
  );
}

function LatencyStrip({
  latency,
}: {
  latency: StatusResponse['latency'];
}) {
  return (
    <section>
      <SectionHeader>
        Request latency{' '}
        <span className="text-xs font-normal text-ink-faint">
          {' '}
          · {Math.round(latency.window_secs / 60)}-min window
        </span>
      </SectionHeader>
      <div className="grid grid-cols-3 gap-3">
        <LatencyCell label="p50" value={latency.p50_ms} target={50} />
        <LatencyCell label="p95" value={latency.p95_ms} target={200} />
        <LatencyCell label="p99" value={latency.p99_ms} target={500} />
      </div>
    </section>
  );
}

function LatencyCell({
  label,
  value,
  target,
}: {
  label: string;
  value: number;
  target: number;
}) {
  const pct = Math.min(100, (value / target) * 100);
  const tone =
    pct < 60 ? 'ok' : pct < 100 ? 'warn' : ('bad' as const);
  const colorClasses = {
    ok: 'text-ok-700 bg-ok-500',
    warn: 'text-warn-700 bg-warn-500',
    bad: 'text-bad-700 bg-bad-500',
  };
  return (
    <div className="rounded-md border border-surface-line bg-surface p-4">
      <div className="text-[11px] uppercase tracking-wider text-ink-faint">
        {label}
      </div>
      <div className="mt-1 flex items-baseline gap-2">
        <span className={`text-2xl font-semibold tabular-nums ${colorClasses[tone].split(' ')[0]}`}>
          {value.toFixed(1)}
        </span>
        <span className="text-xs text-ink-muted">ms</span>
        <span className="ml-auto text-xs text-ink-faint">
          target {target}
        </span>
      </div>
      <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-surface-line">
        <div
          className={`h-full ${colorClasses[tone].split(' ')[1]}`}
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  );
}

function FreshnessRow({
  freshness,
}: {
  freshness: StatusResponse['freshness'];
}) {
  const sourcePct =
    freshness.total_sources > 0
      ? (freshness.active_sources / freshness.total_sources) * 100
      : 0;
  return (
    <section>
      <SectionHeader>Ingest freshness</SectionHeader>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="rounded-md border border-surface-line bg-surface p-4">
          <div className="text-[11px] uppercase tracking-wider text-ink-faint">
            Last aggregator tick
          </div>
          <div className="mt-1 font-mono text-sm">
            {timeSince(freshness.last_aggregator_tick)} ago
          </div>
        </div>
        <div className="rounded-md border border-surface-line bg-surface p-4">
          <div className="text-[11px] uppercase tracking-wider text-ink-faint">
            Active sources
          </div>
          <div className="mt-1 flex items-baseline gap-2">
            <span className="text-2xl font-semibold tabular-nums">
              {freshness.active_sources}
            </span>
            <span className="text-sm text-ink-muted">
              / {freshness.total_sources}
            </span>
          </div>
          <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-surface-line">
            <div
              className="h-full bg-brand-500"
              style={{ width: `${sourcePct}%` }}
            />
          </div>
        </div>
      </div>
    </section>
  );
}

function ActiveIncidents({ incidents }: { incidents: IncidentEntry[] }) {
  return (
    <section>
      <SectionHeader>Active incidents</SectionHeader>
      {incidents.length === 0 ? (
        <div className="rounded-md border border-surface-line bg-surface px-4 py-6 text-center text-sm text-ink-faint">
          No active incidents.
        </div>
      ) : (
        <ul className="space-y-2">
          {incidents.map((inc) => {
            const tone =
              inc.severity === 'page'
                ? 'bad'
                : inc.severity === 'ticket'
                  ? 'warn'
                  : ('ok' as const);
            const colors = {
              ok: 'border-ok-500/30 bg-ok-50',
              warn: 'border-warn-500/30 bg-warn-50',
              bad: 'border-bad-500/30 bg-bad-50',
            };
            return (
              <li
                key={inc.name}
                className={`flex items-start justify-between rounded-md border px-4 py-3 ${colors[tone]}`}
              >
                <div>
                  <div className="font-mono text-sm font-medium">
                    {inc.name}
                  </div>
                  <div className="mt-1 text-xs uppercase tracking-wider text-ink-faint">
                    {inc.severity}
                  </div>
                </div>
                {inc.runbook_url && (
                  <a
                    href={inc.runbook_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="ml-4 flex items-center gap-1 text-xs text-ink-muted hover:text-brand-600"
                  >
                    Runbook
                    <ExternalLink className="h-3 w-3" />
                  </a>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

function EndpointMatrix({
  endpoints,
  health,
}: {
  endpoints: typeof PUBLIC_ENDPOINTS;
  health: Record<string, EndpointProbeResult>;
}) {
  const grouped = useMemo(() => {
    const out: Record<string, typeof endpoints> = {};
    for (const ep of endpoints) {
      out[ep.group] = out[ep.group] ?? [];
      out[ep.group]!.push(ep);
    }
    return Object.entries(out);
  }, [endpoints]);

  return (
    <section>
      <SectionHeader>Endpoints</SectionHeader>
      <div className="space-y-4">
        {grouped.map(([group, eps]) => (
          <div key={group}>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-ink-faint">
              {group}
            </h3>
            <div className="overflow-hidden rounded-md border border-surface-line bg-surface">
              <table className="w-full text-sm">
                <tbody className="divide-y divide-surface-line">
                  {eps.map((ep) => {
                    const probe = health[ep.path];
                    return (
                      <tr key={ep.path}>
                        <td className="px-4 py-2 font-mono text-xs">
                          {ep.path}
                        </td>
                        <td className="px-4 py-2 text-xs text-ink-muted">
                          {ep.description}
                        </td>
                        <td className="px-4 py-2 text-right">
                          <EndpointBadge probe={probe} />
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}

// EndpointProbeResult is the union of states the matrix renders.
//   - 'fast' / 'slow' / 'down' come from a real fetch
//   - 'error' is a fetch that threw (network, abort, TLS)
//   - 'static' is a non-probed endpoint (auth-gated, streaming);
//     `label` is what to show in the badge ("auth req'd",
//     "stream"). These never animate or change colour because
//     the page can't observe them without escalating to a paid
//     synthetic-monitor.
type EndpointProbeResult =
  | { kind: 'fast'; latencyMs: number }
  | { kind: 'slow'; latencyMs: number }
  | { kind: 'down'; latencyMs: number; status: number }
  | { kind: 'error'; latencyMs: number }
  | { kind: 'static'; label: 'requires-auth' | 'streaming' };

// probeEndpoint returns a closure so the same-shape probe runs
// every poll without re-allocating the URL.
function probeEndpoint(
  ep: PublicEndpoint,
): () => Promise<EndpointProbeResult> {
  if (ep.probe.kind === 'requires-auth' || ep.probe.kind === 'streaming') {
    const label = ep.probe.kind;
    return () => Promise.resolve({ kind: 'static', label });
  }
  const url = `${API_BASE_URL}${ep.probe.path}`;
  return async () => {
    const start = performance.now();
    try {
      const res = await fetch(url, {
        signal: AbortSignal.timeout(PROBE_TIMEOUT_MS),
        cache: 'no-store',
      });
      const latencyMs = performance.now() - start;
      if (!res.ok) {
        return { kind: 'down', latencyMs, status: res.status };
      }
      return latencyMs < PROBE_SLOW_MS
        ? { kind: 'fast', latencyMs }
        : { kind: 'slow', latencyMs };
    } catch {
      const latencyMs = performance.now() - start;
      return { kind: 'error', latencyMs };
    }
  };
}

function EndpointBadge({ probe }: { probe?: EndpointProbeResult }) {
  if (!probe) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-surface-subtle px-2 py-0.5 text-[10px] uppercase tracking-wider text-ink-faint">
        —
      </span>
    );
  }
  if (probe.kind === 'static') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-surface-subtle px-2 py-0.5 text-[10px] uppercase tracking-wider text-ink-faint">
        {probe.label === 'requires-auth' ? "auth req'd" : 'stream'}
      </span>
    );
  }
  if (probe.kind === 'fast') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-ok-50 px-2 py-0.5 text-[10px] uppercase tracking-wider text-ok-700">
        <CheckCircle2 className="h-3 w-3" />
        {Math.round(probe.latencyMs)}ms
      </span>
    );
  }
  if (probe.kind === 'slow') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-warn-50 px-2 py-0.5 text-[10px] uppercase tracking-wider text-warn-700">
        <AlertTriangle className="h-3 w-3" />
        {Math.round(probe.latencyMs)}ms
      </span>
    );
  }
  if (probe.kind === 'down') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-bad-50 px-2 py-0.5 text-[10px] uppercase tracking-wider text-bad-700">
        <XCircle className="h-3 w-3" />
        {probe.status}
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-bad-50 px-2 py-0.5 text-[10px] uppercase tracking-wider text-bad-700">
      <XCircle className="h-3 w-3" />
      err
    </span>
  );
}

// normaliseIncident projects the API's structured shape onto the
// flat one IncidentHistory renders. Severity codes (SEV-1..3) map
// onto the UI's tone names (major / minor / maintenance);
// `summary` is the first paragraph of the markdown body so the
// panel doesn't render the entire post.
function normaliseIncident(
  raw: IncidentsAPIShape['data']['incidents'][number],
): IncidentHistoryEntry {
  const severity =
    raw.severity === 'SEV-1'
      ? 'major'
      : raw.severity === 'SEV-2'
        ? 'minor'
        : 'maintenance';
  const summary = (raw.body_markdown || '')
    .split(/\n## /m)[0]
    ?.replace(/^[#\s]+[^\n]*\n+/, '')
    .replace(/^<!--[\s\S]*?-->\s*/m, '')
    .trim()
    .slice(0, 400);
  const date = raw.started_at ? raw.started_at.slice(0, 10) : '';
  const resolved = raw.resolved_at
    ? `${raw.resolved_at.slice(0, 10)} ${raw.resolved_at.slice(11, 16)} UTC`
    : raw.status;
  return {
    slug: raw.slug,
    date,
    title: raw.title || raw.slug,
    resolved,
    severity,
    summary: summary || raw.title || raw.slug,
  };
}

function IncidentHistory({
  entries,
}: {
  entries: IncidentHistoryEntry[];
}) {
  return (
    <section>
      <div className="mb-3 flex items-baseline justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-wider text-ink-muted">
          Incident history
        </h2>
        <a
          href={`${API_BASE_URL}/v1/incidents.atom`}
          target="_blank"
          rel="noreferrer noopener"
          className="text-xs text-ink-faint hover:text-brand-600"
          title="Atom feed — subscribe in Feedly, Slack RSS bot, etc."
        >
          Subscribe (Atom) ↗
        </a>
      </div>
      {entries.length === 0 ? (
        <div className="rounded-md border border-surface-line bg-surface px-4 py-6 text-center text-sm text-ink-faint">
          No past incidents recorded yet. Resolved incidents will appear
          here once they post-mortem.
        </div>
      ) : (
        <ul className="space-y-3">
          {entries.map((e) => (
            <li
              key={e.slug || e.date + e.title}
              className="rounded-md border border-surface-line bg-surface p-4 transition hover:border-brand-300"
            >
              <div className="flex items-center justify-between">
                {e.slug ? (
                  <a
                    href={`/incident/${e.slug}/`}
                    className="font-medium text-ink hover:text-brand-600"
                  >
                    {e.title}
                  </a>
                ) : (
                  <span className="font-medium">{e.title}</span>
                )}
                <span className="text-xs text-ink-faint">{e.date}</span>
              </div>
              <p className="mt-2 text-sm text-ink-muted">{e.summary}</p>
              <p className="mt-1 text-xs text-ink-faint">
                Resolved: {e.resolved}
              </p>
              {e.slug && (
                <a
                  href={`/incident/${e.slug}/`}
                  className="mt-2 inline-block text-xs text-brand-600 hover:underline"
                >
                  Read full postmortem →
                </a>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function Footer({
  asOf,
  region,
}: {
  asOf: string;
  region: { name: string; deployment: string };
}) {
  return (
    <footer className="border-t border-surface-line pt-4 text-xs text-ink-faint">
      Region: <span className="font-mono">{region.name}</span> ·{' '}
      <span className="font-mono">{region.deployment}</span> · Last update:{' '}
      <span className="font-mono">
        {asOf ? new Date(asOf).toISOString() : '—'}
      </span>
    </footer>
  );
}

function SectionHeader({ children }: { children: React.ReactNode }) {
  return (
    <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-ink-muted">
      {children}
    </h2>
  );
}

function toneFor(status?: ServiceStatus) {
  switch (status) {
    case 'ok':
      return {
        icon: CheckCircle2,
        fg: 'text-ok-700',
        bg: 'bg-ok-50',
        border: 'border-ok-500/30',
      };
    case 'degraded':
      return {
        icon: AlertTriangle,
        fg: 'text-warn-700',
        bg: 'bg-warn-50',
        border: 'border-warn-500/30',
      };
    case 'down':
      return {
        icon: XCircle,
        fg: 'text-bad-700',
        bg: 'bg-bad-50',
        border: 'border-bad-500/30',
      };
    default:
      return {
        icon: Info,
        fg: 'text-ink-muted',
        bg: 'bg-surface-subtle',
        border: 'border-surface-line',
      };
  }
}

function timeSince(iso: string): string {
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return '—';
  const sec = Math.floor((Date.now() - then) / 1000);
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h`;
  const day = Math.floor(hr / 24);
  return `${day}d`;
}
