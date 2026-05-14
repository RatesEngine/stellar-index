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

// Hot-tier endpoint probes also run at POLL_INTERVAL_MS. Warm-tier
// probes (catalogue listings, history queries) run at WARM_PROBE_MS
// — 2 minutes — because hammering them every 30 s measurably drives
// the API's SLO burn rate without adding incident-detection signal.
const WARM_PROBE_MS = 120_000;

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

// REGIONS is the deployment fleet the status page queries. One
// entry today (r1); r2/r3 join as their deploys land — just append
// a row and the page renders an extra panel. Each region must
// expose `/v1/diagnostics/ingestion` (any version of the binary
// post-rc.51).
interface RegionDef {
  name: string;
  label: string;
  apiBaseUrl: string;
}

const REGIONS: RegionDef[] = [
  {
    name: 'r1',
    label: 'Hetzner · Frankfurt',
    apiBaseUrl: API_BASE_URL,
  },
];

// IngestionSnapshot mirrors the wire shape returned by
// `/v1/diagnostics/ingestion`. Field-for-field with the Go
// IngestionDiagnostics struct — see
// `internal/api/v1/diagnostics_ingestion.go`.
interface IngestionSnapshot {
  region: { name: string; deployment: string };
  version: {
    version: string;
    build_date: string;
    commit: string;
    dirty: string;
    go_version: string;
  };
  ledger: {
    latest_ledger: number;
    lag_seconds: number;
    volume_24h_usd?: string;
    markets_count_24h: number;
    assets_indexed: number;
  };
  backfill: Array<{
    decoder: string;
    ranges_total: number;
    ranges_complete: number;
    ranges_running: number;
    ranges_stalled: number;
    ranges_active: number;
    oldest_updated_at?: string;
    oldest_lag_seconds: number;
    newest_ledger: number;
  }>;
  backfill_coverage: Array<{
    source: string;
    applies: boolean;
    genesis_ledger?: number;
    earliest_ledger?: number;
    latest_ledger?: number;
    trade_count: number;
    coverage_pct?: number;
    density_pct?: number;
    covered_ledgers?: number;
    expected_ledgers?: number;
  }>;
  backfill_coverage_as_of?: string;
  fx_backfill: {
    earliest_quote?: string;
    latest_quote?: string;
    total_quotes: number;
    currencies_count: number;
  };
  market_cap: {
    entries_count: number;
    oldest_fetched_at?: string;
    newest_fetched_at?: string;
  };
  supply: {
    classic_assets_with_supply: number;
    sep41_assets_with_supply: number;
    last_snapshot_at?: string;
    latest_ledger?: number;
  };
  sources: Array<{
    name: string;
    class: string;
    subclass?: string;
    include_in_vwap: boolean;
    backfill_safe: boolean;
    trade_count_24h: number;
    volume_24h_usd?: string;
    markets_count_24h: number;
  }>;
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

// tier controls poll cadence:
//   - 'hot'  → 30 s. Health checks, network stats, anything cheap
//             enough that 30 s polling doesn't push tail latency.
//   - 'warm' → 2 min. Catalogue listings, history queries, oracle
//             lookups — endpoints whose backing queries are
//             expensive enough that a 30 s probe loop measurably
//             drives the SLO burn rate. Falls back to 2 min default
//             when omitted.
type ProbeTier = 'hot' | 'warm';

interface PublicEndpoint {
  path: string;
  group: string;
  description: string;
  probe: EndpointProbe;
  tier?: ProbeTier;
}

const PUBLIC_ENDPOINTS: PublicEndpoint[] = [
  {
    path: '/v1/healthz',
    group: 'Health',
    description: 'Liveness probe',
    probe: { kind: 'get', path: '/v1/healthz' },
    tier: 'hot',
  },
  {
    path: '/v1/readyz',
    group: 'Health',
    description: 'Readiness probe',
    probe: { kind: 'get', path: '/v1/readyz' },
    tier: 'hot',
  },
  {
    path: '/v1/price',
    group: 'Pricing',
    description: 'Current VWAP price for one asset',
    probe: { kind: 'get', path: '/v1/price?asset=native&quote=fiat:USD' },
    tier: 'hot',
  },
  {
    path: '/v1/price/batch',
    group: 'Pricing',
    description: 'Batch lookup, up to 1000 assets',
    probe: { kind: 'get', path: '/v1/price/batch?asset_ids=native&quote=fiat:USD' },
    tier: 'hot',
  },
  {
    path: '/v1/price/tip',
    group: 'Pricing',
    description: 'Rolling-window tip price',
    probe: { kind: 'get', path: '/v1/price/tip?asset=native&quote=fiat:USD' },
    tier: 'hot',
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
    // Probes that scan the trades hypertable directly need a pair
    // with real on-chain trades. (native, fiat:USD) doesn't exist
    // — XLM's USD price is a stablecoin-proxied USDC quote per the
    // aggregator policy. Use USDC SAC's underlying classic for
    // probe success.
    probe: { kind: 'get', path: '/v1/vwap?base=native&quote=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN' },
  },
  {
    path: '/v1/twap',
    group: 'Pricing',
    description: 'TWAP over a window',
    probe: { kind: 'get', path: '/v1/twap?base=native&quote=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN' },
  },
  {
    path: '/v1/ohlc',
    group: 'Pricing',
    description: 'OHLC bar',
    probe: { kind: 'get', path: '/v1/ohlc?base=native&quote=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN' },
  },
  {
    path: '/v1/chart',
    group: 'Pricing',
    description: 'Multi-bar chart series',
    probe: { kind: 'get', path: '/v1/chart?asset=native&quote=fiat:USD&timeframe=24h&granularity=1h' },
  },
  {
    path: '/v1/history',
    group: 'Historical',
    description: 'Trade history within a window',
    probe: { kind: 'get', path: '/v1/history?base=native&quote=fiat:USD&limit=1' },
  },
  {
    path: '/v1/observations',
    group: 'Historical',
    description: 'Per-source latest trade',
    probe: { kind: 'get', path: '/v1/observations?asset=native&quote=fiat:USD' },
  },
  {
    path: '/v1/network/stats',
    group: 'Catalogue',
    description: 'Consolidated network aggregate (volume, markets, assets)',
    probe: { kind: 'get', path: '/v1/network/stats' },
    tier: 'hot',
  },
  {
    path: '/v1/assets',
    group: 'Catalogue',
    description: 'Asset directory (440K+ classic assets, with coin-overlay fields)',
    probe: { kind: 'get', path: '/v1/assets?limit=1' },
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
    tier: 'hot',
  },
  {
    path: '/v1/oracle/latest',
    group: 'Oracle',
    description: 'Latest oracle readings',
    // SEP-40 endpoints quote in fiat:USD; pick crypto:XLM as the
    // probe asset because Reflector consistently publishes XLM →
    // USD oracle observations. (USDC/USDT lastprice 404s — those
    // are stablecoins quoted in themselves.)
    probe: { kind: 'get', path: '/v1/oracle/latest?asset=crypto:XLM' },
  },
  {
    path: '/v1/oracle/lastprice',
    group: 'Oracle',
    description: 'SEP-40 lastprice',
    probe: { kind: 'get', path: '/v1/oracle/lastprice?asset=crypto:XLM' },
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

  // ingestionByRegion is keyed by REGIONS[].name. Each region
  // polls its own /v1/diagnostics/ingestion at hot cadence and
  // independently — a r2 outage shouldn't block r1's panel from
  // refreshing.
  const [ingestionByRegion, setIngestionByRegion] = useState<
    Record<string, IngestionSnapshot | null>
  >({});

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

  // Per-region ingestion snapshot. One fetch per region per
  // POLL_INTERVAL_MS (the backend response has Cache-Control
  // public, max-age=15 so the underlying load is minimal even
  // across many viewers). Each region polls independently —
  // r2 timing out doesn't stall r1's refresh.
  useEffect(() => {
    let cancelled = false;
    async function pollRegion(region: RegionDef) {
      try {
        const res = await fetch(
          `${region.apiBaseUrl}/v1/diagnostics/ingestion`,
          { cache: 'no-store' },
        );
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const env = (await res.json()) as { data: IngestionSnapshot };
        if (cancelled) return;
        setIngestionByRegion((prev) => ({
          ...prev,
          [region.name]: env.data,
        }));
      } catch {
        if (cancelled) return;
        // Soft-fail: render the previous snapshot if any, else
        // an empty-state card. We don't surface this in the
        // top-level error banner because the /v1/status feed is
        // the canonical "is the region up" signal — ingestion
        // diagnostics being temporarily slow shouldn't paint the
        // whole page red.
        setIngestionByRegion((prev) =>
          region.name in prev ? prev : { ...prev, [region.name]: null },
        );
      }
    }
    for (const r of REGIONS) {
      pollRegion(r);
    }
    const id = setInterval(() => {
      for (const r of REGIONS) {
        pollRegion(r);
      }
    }, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  // Per-endpoint live probe. Endpoints split by tier:
  //   - hot  → POLL_INTERVAL_MS (30 s)
  //   - warm → WARM_PROBE_MS    (2 min)
  // Endpoints marked `requires-auth` or `streaming` keep their
  // static label and never get a fetch fired against them — they
  // populate once at mount.
  useEffect(() => {
    let cancelled = false;

    function runTier(tier: ProbeTier) {
      const tierEps = PUBLIC_ENDPOINTS.filter(
        (e) => e.probe.kind === 'get' && (e.tier ?? 'warm') === tier,
      );
      const probes = tierEps.map((e) => probeEndpoint(e));
      Promise.allSettled(probes.map((p) => p())).then((results) => {
        if (cancelled) return;
        setEndpointHealth((prev) => {
          const next = { ...prev };
          tierEps.forEach((ep, i) => {
            const r = results[i];
            next[ep.path] =
              r && r.status === 'fulfilled'
                ? r.value
                : { kind: 'error', latencyMs: -1 };
          });
          return next;
        });
      });
    }

    // Static labels (auth / streaming) — paint once at mount.
    setEndpointHealth((prev) => {
      const next = { ...prev };
      for (const ep of PUBLIC_ENDPOINTS) {
        if (ep.probe.kind !== 'get') {
          next[ep.path] = { kind: 'static', label: ep.probe.kind };
        }
      }
      return next;
    });

    runTier('hot');
    runTier('warm');
    const hotId = setInterval(() => runTier('hot'), POLL_INTERVAL_MS);
    const warmId = setInterval(() => runTier('warm'), WARM_PROBE_MS);

    return () => {
      cancelled = true;
      clearInterval(hotId);
      clearInterval(warmId);
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
          <IngestionRegions
            regions={REGIONS}
            snapshots={ingestionByRegion}
          />
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

// IngestionRegions renders one RegionPanel per entry in REGIONS.
// Today there's just r1 so the page collapses to a single panel;
// when r2/r3 ship, each gets its own framed block.
function IngestionRegions({
  regions,
  snapshots,
}: {
  regions: RegionDef[];
  snapshots: Record<string, IngestionSnapshot | null>;
}) {
  return (
    <section className="space-y-4">
      <SectionHeader>Ingestion</SectionHeader>
      {regions.map((r) => (
        <RegionPanel key={r.name} region={r} snapshot={snapshots[r.name]} />
      ))}
    </section>
  );
}

function RegionPanel({
  region,
  snapshot,
}: {
  region: RegionDef;
  snapshot: IngestionSnapshot | null | undefined;
}) {
  if (!snapshot) {
    return (
      <div className="rounded-md border border-surface-line bg-surface p-4 text-sm text-ink-faint">
        Waiting for first ingestion snapshot from{' '}
        <span className="font-mono">{region.name}</span>…
      </div>
    );
  }
  return (
    <div className="space-y-3 rounded-md border border-surface-line bg-surface p-5">
      <RegionHeader region={region} snapshot={snapshot} />
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <LedgerCard ledger={snapshot.ledger} />
        <FXBackfillCard fx={snapshot.fx_backfill} />
        <MarketCapCard mc={snapshot.market_cap} />
        <SupplyCard supply={snapshot.supply} />
      </div>
      <BackfillCoverageTable
        rows={snapshot.backfill_coverage}
        asOf={snapshot.backfill_coverage_as_of}
      />
      <SourceHealthTable rows={snapshot.sources} />
    </div>
  );
}

function RegionHeader({
  region,
  snapshot,
}: {
  region: RegionDef;
  snapshot: IngestionSnapshot;
}) {
  const v = snapshot.version;
  const commitShort = v.commit ? v.commit.slice(0, 7) : '—';
  const dirty = v.dirty === 'true';
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-surface-line pb-3">
      <div className="flex items-baseline gap-2">
        <span className="font-mono text-sm font-semibold text-ink">
          {region.name}
        </span>
        <span className="text-xs uppercase tracking-wider text-ink-faint">
          {snapshot.region.deployment}
        </span>
        <span className="text-xs text-ink-muted">· {region.label}</span>
      </div>
      <div
        className="font-mono text-xs text-ink-faint"
        title={`commit ${v.commit}\nbuilt ${v.build_date}\nGo ${v.go_version}`}
      >
        {v.version}{' '}
        <span className="text-ink-muted">
          @ {commitShort}
          {dirty && (
            <span className="ml-1 rounded bg-warn-50 px-1 text-[10px] text-warn-700">
              dirty
            </span>
          )}
        </span>
      </div>
    </div>
  );
}

function LedgerCard({
  ledger,
}: {
  ledger: IngestionSnapshot['ledger'];
}) {
  const lagTone =
    ledger.lag_seconds < 15
      ? 'ok'
      : ledger.lag_seconds < 60
        ? 'warn'
        : ('bad' as const);
  const lagColor = {
    ok: 'text-ok-700',
    warn: 'text-warn-700',
    bad: 'text-bad-700',
  }[lagTone];
  return (
    <Card title="Live ledger">
      <Row label="Latest ledger" value={ledger.latest_ledger.toLocaleString()} mono />
      <Row
        label="Lag from tip"
        value={`${ledger.lag_seconds}s`}
        valueClass={lagColor}
        mono
      />
      <Row
        label="24h volume"
        value={ledger.volume_24h_usd ? formatUSD(ledger.volume_24h_usd) : '—'}
      />
      <Row label="Markets (24h)" value={ledger.markets_count_24h.toLocaleString()} />
      <Row label="Assets indexed" value={ledger.assets_indexed.toLocaleString()} />
    </Card>
  );
}

function FXBackfillCard({
  fx,
}: {
  fx: IngestionSnapshot['fx_backfill'];
}) {
  return (
    <Card title="FX backfill (fx_quotes)">
      <Row
        label="Coverage"
        value={
          fx.earliest_quote && fx.latest_quote
            ? `${fx.earliest_quote} → ${fx.latest_quote}`
            : '—'
        }
        mono
      />
      <Row label="Currencies" value={fx.currencies_count.toLocaleString()} />
      <Row label="Total quotes" value={fx.total_quotes.toLocaleString()} />
    </Card>
  );
}

function MarketCapCard({ mc }: { mc: IngestionSnapshot['market_cap'] }) {
  const ageS = mc.newest_fetched_at
    ? Math.floor((Date.now() - new Date(mc.newest_fetched_at).getTime()) / 1000)
    : null;
  const ageTone =
    ageS == null
      ? 'neutral'
      : ageS < 600
        ? 'ok'
        : ageS < 1800
          ? 'warn'
          : ('bad' as const);
  const ageColor = {
    ok: 'text-ok-700',
    warn: 'text-warn-700',
    bad: 'text-bad-700',
    neutral: 'text-ink-faint',
  }[ageTone];
  return (
    <Card title="Market cap cache (CoinGecko)">
      <Row label="Entries" value={mc.entries_count.toLocaleString()} />
      <Row
        label="Newest fetch"
        value={ageS == null ? '—' : `${formatAge(ageS)} ago`}
        valueClass={ageColor}
        mono
      />
      <Row
        label="Oldest fetch"
        value={
          mc.oldest_fetched_at
            ? `${formatAge(
                Math.floor(
                  (Date.now() - new Date(mc.oldest_fetched_at).getTime()) / 1000,
                ),
              )} ago`
            : '—'
        }
        mono
      />
    </Card>
  );
}

function SupplyCard({ supply }: { supply: IngestionSnapshot['supply'] }) {
  const ageS = supply.last_snapshot_at
    ? Math.floor((Date.now() - new Date(supply.last_snapshot_at).getTime()) / 1000)
    : null;
  return (
    <Card title="Supply observers">
      <Row label="Classic assets" value={supply.classic_assets_with_supply.toLocaleString()} />
      <Row label="SEP-41 assets" value={supply.sep41_assets_with_supply.toLocaleString()} />
      <Row
        label="Latest snapshot"
        value={ageS == null ? '—' : `${formatAge(ageS)} ago`}
        mono
      />
    </Card>
  );
}

function BackfillCoverageTable({
  rows,
  asOf,
}: {
  rows: IngestionSnapshot['backfill_coverage'];
  asOf?: string;
}) {
  if (!rows || rows.length === 0) {
    return (
      <div className="rounded-md border border-warn-500/30 bg-warn-50 p-3 text-xs text-warn-700">
        Coverage snapshot pending — first refresh runs ~30s after process
        start, then every 5 min.
      </div>
    );
  }
  const onChain = rows.filter((r) => r.applies);
  const offChain = rows.filter((r) => !r.applies);
  return (
    <div>
      <div className="mb-2 flex items-baseline justify-between">
        <h3 className="text-xs font-semibold uppercase tracking-wider text-ink-faint">
          Raw-trades coverage — genesis → tip
        </h3>
        {asOf && (
          <span className="text-[10px] text-ink-faint">
            snapshot {timeSince(asOf)} ago
          </span>
        )}
      </div>
      <p className="mb-2 text-[11px] text-ink-faint">
        <strong>Density</strong> = % of ledgers in the source&apos;s
        expected range we&apos;ve actually processed (union of
        completed backfill cursor intervals, clamped to [genesis,
        tip]). Hits 100% only when backfill ranges fully cover the
        interval — sparse sources no longer score 100% just because
        they have endpoint trades far apart with gaps between.
      </p>
      <div className="overflow-hidden rounded-md border border-surface-line">
        <table className="w-full text-xs">
          <thead className="bg-surface-subtle text-ink-faint">
            <tr>
              <th className="px-3 py-2 text-left font-medium">Source</th>
              <th className="px-3 py-2 text-right font-medium">Genesis</th>
              <th className="px-3 py-2 text-right font-medium">Earliest</th>
              <th className="px-3 py-2 text-right font-medium">Latest</th>
              <th className="px-3 py-2 text-right font-medium">Density</th>
              <th className="px-3 py-2 text-right font-medium">Trades</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-surface-line">
            {onChain.map((r) => {
              const pct = (r.density_pct ?? 0) * 100;
              const tone = pct >= 99 ? 'ok' : pct >= 50 ? 'warn' : ('bad' as const);
              const colors = {
                ok: 'bg-ok-500 text-ok-700',
                warn: 'bg-warn-500 text-warn-700',
                bad: 'bg-bad-500 text-bad-700',
              };
              return (
                <tr key={r.source}>
                  <td className="px-3 py-2 font-mono">{r.source}</td>
                  <td className="px-3 py-2 text-right font-mono tabular-nums text-ink-muted">
                    {r.genesis_ledger?.toLocaleString() ?? '—'}
                  </td>
                  <td className="px-3 py-2 text-right font-mono tabular-nums">
                    {r.earliest_ledger?.toLocaleString() ?? '—'}
                  </td>
                  <td className="px-3 py-2 text-right font-mono tabular-nums">
                    {r.latest_ledger?.toLocaleString() ?? '—'}
                  </td>
                  <td
                    className="px-3 py-2 text-right"
                    title={
                      r.covered_ledgers !== undefined && r.expected_ledgers !== undefined
                        ? `${r.covered_ledgers.toLocaleString()} / ${r.expected_ledgers.toLocaleString()} ledgers covered by completed backfill ranges`
                        : undefined
                    }
                  >
                    <div className="inline-flex items-center justify-end gap-2">
                      <div className="h-1.5 w-16 overflow-hidden rounded-full bg-surface-line">
                        <div
                          className={`h-full ${colors[tone].split(' ')[0]}`}
                          style={{ width: `${Math.max(2, pct)}%` }}
                        />
                      </div>
                      <span className={`tabular-nums ${colors[tone].split(' ')[1]}`}>
                        {pct.toFixed(1)}%
                      </span>
                    </div>
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums text-ink-muted">
                    {r.trade_count.toLocaleString()}
                  </td>
                </tr>
              );
            })}
            {offChain.map((r) => (
              <tr key={r.source} className="text-ink-muted">
                <td className="px-3 py-2 font-mono">{r.source}</td>
                <td className="px-3 py-2 text-right text-[10px] italic" colSpan={4}>
                  off-chain — no Stellar ledger context
                </td>
                <td className="px-3 py-2 text-right tabular-nums">
                  {r.trade_count.toLocaleString()}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function SourceHealthTable({
  rows,
}: {
  rows: IngestionSnapshot['sources'];
}) {
  // Defensive shape-handling — Go marshals nil slices as `null`,
  // not `[]`, so a typed-as-array field can still arrive as null.
  const safeRows = rows ?? [];
  if (safeRows.length === 0) return null;
  return (
    <div>
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-ink-faint">
        Sources — {safeRows.length} registered
      </h3>
      <div className="overflow-hidden rounded-md border border-surface-line">
        <table className="w-full text-xs">
          <thead className="bg-surface-subtle text-ink-faint">
            <tr>
              <th className="px-3 py-2 text-left font-medium">Source</th>
              <th className="px-3 py-2 text-left font-medium">Class</th>
              <th className="px-3 py-2 text-right font-medium">Trades 24h</th>
              <th className="px-3 py-2 text-right font-medium">Volume 24h</th>
              <th className="px-3 py-2 text-right font-medium">Markets</th>
              <th className="px-3 py-2 text-center font-medium">VWAP</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-surface-line">
            {safeRows.map((r) => {
              const classLabel = r.subclass ? `${r.class}/${r.subclass}` : r.class;
              const silent = r.include_in_vwap && r.trade_count_24h === 0;
              return (
                <tr key={r.name}>
                  <td className="px-3 py-2 font-mono">{r.name}</td>
                  <td className="px-3 py-2 text-ink-muted">{classLabel}</td>
                  <td
                    className={`px-3 py-2 text-right tabular-nums ${
                      silent ? 'text-bad-700' : ''
                    }`}
                  >
                    {r.trade_count_24h.toLocaleString()}
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums text-ink-muted">
                    {r.volume_24h_usd ? formatUSD(r.volume_24h_usd) : '—'}
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums text-ink-muted">
                    {r.markets_count_24h.toLocaleString()}
                  </td>
                  <td className="px-3 py-2 text-center">
                    {r.include_in_vwap ? (
                      <span className="text-ok-700">✓</span>
                    ) : (
                      <span className="text-ink-faint">—</span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Card({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-md border border-surface-line bg-surface-subtle p-4">
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-ink-faint">
        {title}
      </h3>
      <dl className="space-y-1.5 text-sm">{children}</dl>
    </div>
  );
}

function Row({
  label,
  value,
  mono,
  valueClass,
}: {
  label: string;
  value: string;
  mono?: boolean;
  valueClass?: string;
}) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <dt className="text-xs text-ink-muted">{label}</dt>
      <dd
        className={`tabular-nums ${mono ? 'font-mono text-xs' : ''} ${
          valueClass ?? ''
        }`}
      >
        {value}
      </dd>
    </div>
  );
}

// formatUSD renders a backend-shaped decimal string (e.g.
// "4382354579.48040914") as a compact human form ("$4.38B").
// The backend keeps full precision via ADR-0003 stringified
// numerics; the UI rounds for display.
function formatUSD(s: string): string {
  const n = Number(s);
  if (!Number.isFinite(n) || n === 0) return '—';
  if (n >= 1e9) return `$${(n / 1e9).toFixed(2)}B`;
  if (n >= 1e6) return `$${(n / 1e6).toFixed(2)}M`;
  if (n >= 1e3) return `$${(n / 1e3).toFixed(2)}K`;
  return `$${n.toFixed(2)}`;
}

// formatAge turns seconds into "12s" / "5m" / "3h" / "2d".
function formatAge(s: number): string {
  if (!Number.isFinite(s) || s < 0) return '—';
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  const d = Math.floor(h / 24);
  return `${d}d`;
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
