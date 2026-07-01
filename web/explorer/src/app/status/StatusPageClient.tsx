'use client';

import { useEffect, useMemo, useState } from 'react';
import {
  AlertTriangle,
  CheckCircle2,
  ExternalLink,
  Info,
  XCircle,
} from 'lucide-react';

import { Badge, Card, Container, type BadgeTone } from '@/components/ui';

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.stellarindex.io';

// Polled every 30 s — same cadence as Healthchecks.io's hosted
// status pages and well inside the 60-s indexer/aggregator
// heartbeat budget so a real degradation lands within one poll.
const POLL_INTERVAL_MS = 30_000;

// Hot-tier endpoint probes also run at POLL_INTERVAL_MS. Warm-tier
// probes (catalogue listings, history queries) run at WARM_PROBE_MS
// — 2 minutes — because hammering them every 30 s measurably drives
// the API's SLO burn rate without adding incident-detection signal.
const WARM_PROBE_MS = 120_000;

// /v1/ledger/stream (SSE) reconnect interval after a hard failure.
// A 404 from an API binary that predates the endpoint fails the
// EventSource without auto-reconnect (per the SSE spec a non-2xx
// response does not retry); this slow timer lets an already-open tab
// upgrade to streaming once the endpoint ships, without hammering
// the 404. Transient blips are left to EventSource's own retry.
const LEDGER_STREAM_REOPEN_MS = 60_000;

// The "live" SSE badge is only trustworthy while events keep
// arriving. If the last ledger_update is older than this, the
// stream has gone quiet (origin death, half-open connection the
// browser hasn't noticed) and we fall back to the 30s snapshot —
// 2× the network's ~5s ledger cadence, generous enough not to
// flap on a single missed close. (WB-04)
const LEDGER_STREAM_STALE_MS = 60_000;

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
    entries: number;
    coverage_pct?: number;
    density_pct?: number;
    gap_free_pct?: number;
    covered_ledgers?: number;
    expected_ledgers?: number;
    // ADR-0033 Phase 6: watermark-based completeness (substrate +
    // projection verified, no sparsity threshold). Preferred headline
    // when present; falls back to gap_free coverage otherwise.
    completeness_pct?: number;
    completeness_watermark?: number;
    completeness_complete?: boolean;
  }>;
  backfill_coverage_as_of?: string;
  fx_backfill: {
    earliest_quote?: string;
    latest_quote?: string;
    total_quotes: number;
    currencies_count: number;
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
    // entries_24h: universal trailing-24h per-source event count
    // (stellarindex_source_events_total). Non-zero for every active
    // source, unlike trade_count_24h (trades-table only).
    entries_24h: number;
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
    probe: {
      kind: 'get',
      path: '/v1/price/batch?asset_ids=native&quote=fiat:USD',
    },
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
    probe: {
      kind: 'get',
      path: '/v1/vwap?base=native&quote=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
    },
  },
  {
    path: '/v1/twap',
    group: 'Pricing',
    description: 'TWAP over a window',
    probe: {
      kind: 'get',
      path: '/v1/twap?base=native&quote=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
    },
  },
  {
    path: '/v1/ohlc',
    group: 'Pricing',
    description: 'OHLC bar',
    probe: {
      kind: 'get',
      path: '/v1/ohlc?base=native&quote=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
    },
  },
  {
    path: '/v1/chart',
    group: 'Pricing',
    description: 'Multi-bar chart series',
    probe: {
      kind: 'get',
      path: '/v1/chart?asset=native&quote=fiat:USD&timeframe=24h&granularity=1h',
    },
  },
  {
    path: '/v1/history',
    group: 'Historical',
    description: 'Trade history within a window',
    probe: {
      kind: 'get',
      path: '/v1/history?base=native&quote=fiat:USD&limit=1',
    },
  },
  {
    path: '/v1/observations',
    group: 'Historical',
    description: 'Per-source latest trade',
    probe: {
      kind: 'get',
      path: '/v1/observations?asset=native&quote=fiat:USD',
    },
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
    description:
      'Asset directory (440K+ classic assets, with coin-overlay fields)',
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
// which IncidentHistory normalises before render. The build-time
// corpus is projected into the same shape by the server wrapper.
export interface IncidentHistoryEntry {
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

// Live-feed fetch outcome — distinguishes "the request failed"
// (we're showing only the build-time corpus) from "succeeded but
// empty" (genuinely no incidents) so the empty-state copy can be
// honest. (WB-02c)
type IncidentFeedState = 'loading' | 'ok' | 'error';

export default function StatusPageClient({
  seedIncidents,
}: {
  // Build-time incident corpus, pre-projected to the UI-flat shape
  // by the server wrapper. Rendered immediately so past incidents
  // are visible even when the live API is fully down (WB-02b).
  seedIncidents: IncidentHistoryEntry[];
}) {
  const [status, setStatus] = useState<StatusResponse | null>(null);
  const [asOf, setAsOf] = useState<string>('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [endpointHealth, setEndpointHealth] = useState<
    Record<string, EndpointProbeResult>
  >({});
  // Seed from the build-time corpus; the live feed overlays it on
  // a successful fetch.
  const [incidentHistory, setIncidentHistory] =
    useState<IncidentHistoryEntry[]>(seedIncidents);
  const [incidentFeed, setIncidentFeed] =
    useState<IncidentFeedState>('loading');

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

  // Incident history is fetched once at mount from /v1/incidents and
  // overlaid on the build-time seed. The corpus is small (a handful
  // of posts per year of operation) and changes only on redeploy, so
  // polling would be wasted work. On a fetch failure we keep the seed
  // — the panel never collapses to "no incidents" during an outage.
  useEffect(() => {
    let cancelled = false;
    fetch(`${API_BASE_URL}/v1/incidents`, { cache: 'no-store' })
      .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
      .then((env: IncidentsAPIShape) => {
        if (cancelled) return;
        const live = (env.data?.incidents ?? []).map(normaliseIncident);
        setIncidentHistory(mergeIncidents(seedIncidents, live));
        setIncidentFeed('ok');
      })
      .catch(() => {
        if (cancelled) return;
        // Keep the build-time seed; flag the feed as errored so the
        // empty-state copy distinguishes "fetch failed" from
        // "genuinely no incidents".
        setIncidentFeed('error');
      });
    return () => {
      cancelled = true;
    };
  }, [seedIncidents]);

  const overallTone = useMemo(() => toneFor(status?.overall), [status]);

  return (
    <Container className="max-w-5xl space-y-8 py-10">
      <PageHead />
      <OverallBanner status={status?.overall ?? 'unknown'} tone={overallTone} />
      {error && (
        <Card className="border-bad-300 bg-bad-50 px-4 py-3 text-sm text-bad-700">
          Status feed unreachable: {error}.{' '}
          {status
            ? 'Showing the last known snapshot below.'
            : 'No snapshot has been received yet — independent endpoint probes below still show live results, and past incidents are loaded from the build-time corpus.'}
        </Card>
      )}
      {loading && !status && !error && (
        <Card className="px-4 py-8 text-center text-sm text-ink-faint">
          Loading status…
        </Card>
      )}
      {status && (
        <>
          <ServiceGrid services={status.services} />
          <LatencyStrip latency={status.latency} />
          <FreshnessRow freshness={status.freshness} />
          <IngestionRegions regions={REGIONS} snapshots={ingestionByRegion} />
          <ActiveIncidents incidents={status.incidents.active} />
        </>
      )}
      {/* EndpointMatrix and IncidentHistory render UNCONDITIONALLY —
              they don't depend on the /v1/status feed. The matrix runs
              its own independent probes (so red badges show during an
              outage), and the history is seeded from the build-time
              corpus (so past incidents survive a full API outage). WB-02 */}
      <EndpointMatrix endpoints={PUBLIC_ENDPOINTS} health={endpointHealth} />
      <IncidentHistory entries={incidentHistory} feed={incidentFeed} />
      {status && <RegionMeta asOf={asOf} region={status.region} />}
    </Container>
  );
}

function PageHead() {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div>
        <div className="mb-1.5 text-xs font-medium uppercase tracking-wider text-brand-600">
          System status
        </div>
        <h1 className="text-h1 font-semibold text-ink">Stellar Index status</h1>
        <p className="mt-2 max-w-prose text-[15px] leading-relaxed text-ink-muted">
          Live service health, request latency, ingest freshness, and the full
          public-endpoint matrix — probed independently from your browser.
        </p>
      </div>
      <div className="flex items-center gap-2 whitespace-nowrap text-sm text-ink-muted">
        <span className="inline-block h-2 w-2 animate-pulse-dot rounded-full bg-ok-500" />
        Live · refreshed every 30 s
      </div>
    </div>
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
      'We can’t reach the status feed. The endpoint probes and incident history below are independent and remain live.',
  };
  const badgeLabels: Record<ServiceStatus, string> = {
    ok: 'Operational',
    degraded: 'Degraded',
    down: 'Outage',
    unknown: 'Unknown',
  };
  const Icon = tone.icon;
  return (
    <Card className={`overflow-hidden border ${tone.cardBorder}`}>
      <div className={`flex items-start gap-4 p-6 ${tone.cardBg}`}>
        <div
          className={`flex h-12 w-12 shrink-0 items-center justify-center rounded-card bg-surface ${tone.fg} ring-1 ${tone.ring}`}
        >
          <Icon className="h-6 w-6" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-3">
            <h2 className="text-h3 font-semibold text-ink">
              {headlines[status]}
            </h2>
            <Badge tone={tone.badge} dot>
              {badgeLabels[status]}
            </Badge>
          </div>
          <p className="mt-1.5 text-sm leading-relaxed text-ink-muted">
            {subtitles[status]}
          </p>
        </div>
      </div>
    </Card>
  );
}

function ServiceGrid({ services }: { services: ServiceEntry[] }) {
  return (
    <section>
      <SectionHead>Services</SectionHead>
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
    <Card className="flex items-start justify-between p-4">
      <div className="min-w-0">
        <div className="font-medium capitalize text-ink">{service.name}</div>
        {service.last_seen && (
          <div className="mt-1 text-xs text-ink-faint">
            Last seen {timeSince(service.last_seen)} ago
          </div>
        )}
      </div>
      <Icon className={`h-5 w-5 shrink-0 ${tone.fg}`} />
    </Card>
  );
}

function LatencyStrip({ latency }: { latency: StatusResponse['latency'] }) {
  return (
    <section>
      <SectionHead aside={`${Math.round(latency.window_secs / 60)}-min window`}>
        Request latency
      </SectionHead>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
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
  const tone = pct < 60 ? 'ok' : pct < 100 ? 'warn' : ('bad' as const);
  const fg = {
    ok: 'text-ok-700',
    warn: 'text-warn-700',
    bad: 'text-bad-700',
  }[tone];
  const bar = {
    ok: 'bg-ok-500',
    warn: 'bg-warn-500',
    bad: 'bg-bad-500',
  }[tone];
  return (
    <Card className="p-4">
      <div className="text-[11px] font-medium uppercase tracking-wider text-ink-faint">
        {label}
      </div>
      <div className="mt-1 flex items-baseline gap-2">
        <span className={`tnum text-2xl font-semibold ${fg}`}>
          {value.toFixed(1)}
        </span>
        <span className="text-xs text-ink-muted">ms</span>
        <span className="ml-auto text-xs text-ink-faint">target {target}</span>
      </div>
      <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-surface-subtle">
        <div className={`h-full ${bar}`} style={{ width: `${pct}%` }} />
      </div>
    </Card>
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
      <SectionHead>Ingest freshness</SectionHead>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Card className="p-4">
          <div className="text-[11px] font-medium uppercase tracking-wider text-ink-faint">
            Last aggregator tick
          </div>
          <div className="mt-1 font-mono text-sm text-ink">
            {timeSince(freshness.last_aggregator_tick)} ago
          </div>
        </Card>
        <Card className="p-4">
          <div className="text-[11px] font-medium uppercase tracking-wider text-ink-faint">
            Active sources
          </div>
          <div className="mt-1 flex items-baseline gap-2">
            <span className="tnum text-2xl font-semibold text-ink">
              {freshness.active_sources}
            </span>
            <span className="text-sm text-ink-muted">
              / {freshness.total_sources}
            </span>
          </div>
          <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-surface-subtle">
            <div
              className="h-full bg-brand-500"
              style={{ width: `${sourcePct}%` }}
            />
          </div>
        </Card>
      </div>
    </section>
  );
}

function ActiveIncidents({ incidents }: { incidents: IncidentEntry[] }) {
  return (
    <section>
      <SectionHead>Active incidents</SectionHead>
      {incidents.length === 0 ? (
        <Card className="px-4 py-6 text-center text-sm text-ink-faint">
          No active incidents.
        </Card>
      ) : (
        <ul className="space-y-2">
          {incidents.map((inc) => {
            const tone: BadgeTone =
              inc.severity === 'page'
                ? 'bad'
                : inc.severity === 'ticket'
                  ? 'warn'
                  : 'ok';
            return (
              <li key={inc.name}>
                <Card className="flex items-start justify-between p-4">
                  <div className="min-w-0">
                    <div className="font-mono text-sm font-medium text-ink">
                      {inc.name}
                    </div>
                    <div className="mt-1.5">
                      <Badge tone={tone} dot>
                        {inc.severity}
                      </Badge>
                    </div>
                  </div>
                  {inc.runbook_url && (
                    <a
                      href={inc.runbook_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="ml-4 flex shrink-0 items-center gap-1 text-xs text-ink-muted hover:text-brand-600"
                    >
                      Runbook
                      <ExternalLink className="h-3 w-3" />
                    </a>
                  )}
                </Card>
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
      <SectionHead>Endpoints</SectionHead>
      <div className="space-y-5">
        {grouped.map(([group, eps]) => (
          <div key={group}>
            <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-ink-faint">
              {group}
            </h3>
            <Card flat className="overflow-hidden">
              <table className="w-full text-sm">
                <tbody className="divide-y divide-line">
                  {eps.map((ep) => {
                    const probe = health[ep.path];
                    return (
                      <tr key={ep.path}>
                        <td className="px-4 py-2.5 font-mono text-xs text-ink-body">
                          {ep.path}
                        </td>
                        <td className="hidden px-4 py-2.5 text-xs text-ink-muted sm:table-cell">
                          {ep.description}
                        </td>
                        <td className="px-4 py-2.5 text-right">
                          <EndpointBadge probe={probe} />
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </Card>
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
function probeEndpoint(ep: PublicEndpoint): () => Promise<EndpointProbeResult> {
  if (ep.probe.kind === 'requires-auth' || ep.probe.kind === 'streaming') {
    const label = ep.probe.kind;
    return () => Promise.resolve({ kind: 'static', label });
  }
  const url = `${API_BASE_URL}${ep.probe.path}`;
  return async () => {
    // Two-shot probe. The status page polls every 30 s (hot tier)
    // or 2 min (warm tier); between polls Cloudflare lets the
    // edge→origin connection pool go cold, so a single probe's
    // FIRST request pays a full CF↔origin TCP+TLS setup (~2-3 s
    // measured) that has nothing to do with API latency — the API
    // itself serves cached asset detail in <10 ms. The first fetch
    // below is a throwaway warm-up; the second, on the now-warm
    // connection, is the latency a returning user actually
    // experiences and the one we report. cache:'no-store' on both
    // so neither the browser nor the CDN serves a stale body — we
    // always measure a real round trip, just not a cold-pool one.
    try {
      const warm = await fetch(url, {
        signal: AbortSignal.timeout(PROBE_TIMEOUT_MS),
        cache: 'no-store',
      });
      // A non-2xx is a real status — report it without spending a
      // second request (and a second timeout) re-confirming it.
      if (!warm.ok) {
        return { kind: 'down', latencyMs: -1, status: warm.status };
      }
    } catch {
      // Warm-up threw (network / abort / TLS). No point timing a
      // second doomed request — report the error now.
      return { kind: 'error', latencyMs: -1 };
    }
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
      <Badge tone="neutral" className="font-mono text-[10px]">
        —
      </Badge>
    );
  }
  if (probe.kind === 'static') {
    return (
      <Badge tone="neutral" className="text-[10px]">
        {probe.label === 'requires-auth' ? "auth req'd" : 'stream'}
      </Badge>
    );
  }
  if (probe.kind === 'fast') {
    return (
      <Badge tone="ok" className="text-[10px]">
        <CheckCircle2 className="h-3 w-3" />
        <span className="tnum">{Math.round(probe.latencyMs)}ms</span>
      </Badge>
    );
  }
  if (probe.kind === 'slow') {
    return (
      <Badge tone="warn" className="text-[10px]">
        <AlertTriangle className="h-3 w-3" />
        <span className="tnum">{Math.round(probe.latencyMs)}ms</span>
      </Badge>
    );
  }
  if (probe.kind === 'down') {
    return (
      <Badge tone="bad" className="text-[10px]">
        <XCircle className="h-3 w-3" />
        <span className="tnum">{probe.status}</span>
      </Badge>
    );
  }
  return (
    <Badge tone="bad" className="text-[10px]">
      <XCircle className="h-3 w-3" />
      err
    </Badge>
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

// mergeIncidents overlays the live feed on the build-time seed,
// preferring the live entry for any slug present in both (it's
// fresher — a build-time seed of an unresolved incident can be
// stale). Slugless seed entries (shouldn't happen, the corpus is
// file-named) are kept as-is. Newest-first by date.
function mergeIncidents(
  seed: IncidentHistoryEntry[],
  live: IncidentHistoryEntry[],
): IncidentHistoryEntry[] {
  const bySlug = new Map<string, IncidentHistoryEntry>();
  for (const e of seed) bySlug.set(e.slug || `${e.date}|${e.title}`, e);
  for (const e of live) bySlug.set(e.slug || `${e.date}|${e.title}`, e);
  return Array.from(bySlug.values()).sort((a, b) =>
    a.date < b.date ? 1 : a.date > b.date ? -1 : 0,
  );
}

function severityBadgeTone(s: IncidentHistoryEntry['severity']): BadgeTone {
  return s === 'major' ? 'bad' : s === 'minor' ? 'warn' : 'ok';
}

function IncidentHistory({
  entries,
  feed,
}: {
  entries: IncidentHistoryEntry[];
  feed: IncidentFeedState;
}) {
  return (
    <section>
      <SectionHead
        action={
          <a
            href={`${API_BASE_URL}/v1/incidents.atom`}
            target="_blank"
            rel="noreferrer noopener"
            className="text-xs text-ink-faint hover:text-brand-600"
            title="Atom feed — subscribe in Feedly, Slack RSS bot, etc."
          >
            Subscribe (Atom) ↗
          </a>
        }
      >
        Incident history
      </SectionHead>
      {entries.length === 0 ? (
        <Card className="px-4 py-6 text-center text-sm text-ink-faint">
          {feed === 'error'
            ? 'Incident feed unreachable — and no postmortems are bundled in this build. Past incidents will appear here once the feed is reachable again.'
            : 'No past incidents recorded yet. Resolved incidents will appear here once they post-mortem.'}
        </Card>
      ) : (
        <ul className="space-y-3">
          {entries.map((e) => (
            <li key={e.slug || e.date + e.title}>
              <Card interactive className="p-4">
                <div className="flex items-center justify-between gap-3">
                  <div className="flex min-w-0 items-center gap-2">
                    <Badge tone={severityBadgeTone(e.severity)}>
                      {e.severity}
                    </Badge>
                    {e.slug ? (
                      <a
                        href={`/status/incident/${e.slug}/`}
                        className="truncate font-medium text-ink hover:text-brand-600"
                      >
                        {e.title}
                      </a>
                    ) : (
                      <span className="truncate font-medium text-ink">
                        {e.title}
                      </span>
                    )}
                  </div>
                  <span className="shrink-0 font-mono text-xs text-ink-faint">
                    {e.date}
                  </span>
                </div>
                <p className="mt-2 text-sm leading-relaxed text-ink-muted">
                  {e.summary}
                </p>
                <p className="mt-1 text-xs text-ink-faint">
                  Resolved: {e.resolved}
                </p>
                {e.slug && (
                  <a
                    href={`/status/incident/${e.slug}/`}
                    className="mt-2 inline-block text-xs font-medium text-brand-600 hover:underline"
                  >
                    Read full postmortem →
                  </a>
                )}
              </Card>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function RegionMeta({
  asOf,
  region,
}: {
  asOf: string;
  region: { name: string; deployment: string };
}) {
  return (
    <div className="border-t border-line pt-4 text-xs text-ink-faint">
      Region: <span className="font-mono">{region.name}</span> ·{' '}
      <span className="font-mono">{region.deployment}</span> · Last update:{' '}
      <span className="font-mono">
        {asOf ? new Date(asOf).toISOString() : '—'}
      </span>
    </div>
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
      <SectionHead>Ingestion</SectionHead>
      {regions.map((r) => (
        <RegionPanel key={r.name} region={r} snapshot={snapshots[r.name]} />
      ))}
    </section>
  );
}

// LiveLedger is the data payload of a /v1/ledger/stream
// `ledger_update` event (and the data field of /v1/ledger/tip).
interface LiveLedger {
  latest_ledger: number;
  ingested_at: string;
  lag_seconds: number;
}

// useLedgerStream subscribes to /v1/ledger/stream (SSE) and returns
// the most-recent `ledger_update` payload together with the
// wall-clock time it arrived, or null when the stream is
// unavailable / has gone stale. The status page deploys (Cloudflare
// Pages) ahead of the API release, so when an older API binary 404s
// the endpoint this hook stays null and the caller MUST fall back to
// the 30s /v1/diagnostics/ingestion snapshot.
//
// The `receivedAt` timestamp lets the caller drop the "live" badge
// when the stream has silently gone quiet (half-open TCP the browser
// hasn't surfaced as an error): a value older than
// LEDGER_STREAM_STALE_MS is no longer "live". (WB-04)
function useLedgerStream(
  apiBaseUrl: string,
): { ledger: LiveLedger; receivedAt: number } | null {
  const [live, setLive] = useState<{
    ledger: LiveLedger;
    receivedAt: number;
  } | null>(null);

  useEffect(() => {
    let es: EventSource | null = null;
    let reopenTimer: ReturnType<typeof setTimeout> | null = null;
    let closed = false;

    function connect() {
      if (closed) return;
      es = new EventSource(`${apiBaseUrl}/v1/ledger/stream`);
      es.addEventListener('ledger_update', (ev) => {
        try {
          const payload = JSON.parse((ev as MessageEvent).data) as {
            data: LiveLedger;
          };
          setLive({ ledger: payload.data, receivedAt: Date.now() });
        } catch {
          // Malformed frame — keep the last good value.
        }
      });
      es.onerror = () => {
        // readyState CLOSED = a hard failure (e.g. a 404 from an API
        // binary predating the endpoint); the browser will NOT
        // auto-reconnect, so schedule a slow reopen. readyState
        // CONNECTING = a transient blip the browser is already
        // retrying — leave it alone.
        if (es && es.readyState === EventSource.CLOSED) {
          es = null;
          if (!closed) {
            reopenTimer = setTimeout(connect, LEDGER_STREAM_REOPEN_MS);
          }
        }
      };
    }
    connect();

    return () => {
      closed = true;
      if (reopenTimer) clearTimeout(reopenTimer);
      es?.close();
    };
  }, [apiBaseUrl]);

  return live;
}

function RegionPanel({
  region,
  snapshot,
}: {
  region: RegionDef;
  snapshot: IngestionSnapshot | null | undefined;
}) {
  // Subscribe unconditionally (hooks can't be conditional) — the
  // result is null until the first SSE event lands, and LedgerCard
  // falls back to the snapshot while it is.
  const liveLedger = useLedgerStream(region.apiBaseUrl);

  // A `now` tick so the LedgerCard re-evaluates the stream-staleness
  // window even when no new SSE event arrives — without it, a stream
  // that dies right after one event would keep the "live" badge
  // forever. (WB-04)
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, []);

  // Treat the stream as live only while events are still arriving.
  const liveFresh =
    liveLedger != null && now - liveLedger.receivedAt < LEDGER_STREAM_STALE_MS
      ? liveLedger.ledger
      : null;

  if (!snapshot) {
    return (
      <Card className="p-4 text-sm text-ink-faint">
        Waiting for first ingestion snapshot from{' '}
        <span className="font-mono">{region.name}</span>…
      </Card>
    );
  }
  return (
    <Card className="space-y-3 p-5">
      <RegionHeader region={region} snapshot={snapshot} />
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <LedgerCard ledger={snapshot.ledger} live={liveFresh} />
        <FXBackfillCard fx={snapshot.fx_backfill} />
        <SupplyCard supply={snapshot.supply} />
      </div>
      <BackfillCoverageTable
        rows={snapshot.backfill_coverage}
        asOf={snapshot.backfill_coverage_as_of}
      />
      <SourceHealthTable rows={snapshot.sources} />
    </Card>
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
    <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-line pb-3">
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
            <span className="ml-1 rounded-sm bg-warn-50 px-1 text-[10px] text-warn-700">
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
  live,
}: {
  ledger: IngestionSnapshot['ledger'];
  live: LiveLedger | null;
}) {
  // The SSE stream (when connected AND fresh) carries a fresher tip
  // than the 30s snapshot — prefer it for the ledger number + lag,
  // but keep the snapshot for volume / markets / assets, which the
  // stream does not carry. Falls back to the snapshot whenever the
  // stream is unavailable, disconnected, OR has gone stale (the
  // caller passes null once the last event ages past the staleness
  // window).
  const latestLedger = live?.latest_ledger ?? ledger.latest_ledger;
  const lagSeconds = live?.lag_seconds ?? ledger.lag_seconds;
  const lagTone =
    lagSeconds < 15 ? 'ok' : lagSeconds < 60 ? 'warn' : ('bad' as const);
  const lagColor = {
    ok: 'text-ok-700',
    warn: 'text-warn-700',
    bad: 'text-bad-700',
  }[lagTone];
  return (
    <Panel
      title="Live ledger"
      accessory={
        live ? (
          <span className="flex items-center gap-1 text-[10px] font-medium text-ok-700">
            <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-ok-700" />
            live
          </span>
        ) : null
      }
    >
      <Row label="Latest ledger" value={latestLedger.toLocaleString()} mono />
      <Row
        label="Lag from tip"
        value={`${lagSeconds}s`}
        valueClass={lagColor}
        mono
      />
      <Row
        label="24h volume"
        value={ledger.volume_24h_usd ? formatUSD(ledger.volume_24h_usd) : '—'}
      />
      <Row
        label="Markets (24h)"
        value={ledger.markets_count_24h.toLocaleString()}
      />
      <Row
        label="Assets indexed"
        value={ledger.assets_indexed.toLocaleString()}
      />
    </Panel>
  );
}

function FXBackfillCard({ fx }: { fx: IngestionSnapshot['fx_backfill'] }) {
  return (
    <Panel title="FX backfill (fx_quotes)">
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
    </Panel>
  );
}

function SupplyCard({ supply }: { supply: IngestionSnapshot['supply'] }) {
  const ageS = supply.last_snapshot_at
    ? Math.floor(
        (Date.now() - new Date(supply.last_snapshot_at).getTime()) / 1000,
      )
    : null;
  return (
    <Panel title="Supply observers">
      <Row
        label="Classic assets"
        value={supply.classic_assets_with_supply.toLocaleString()}
      />
      <Row
        label="SEP-41 assets"
        value={supply.sep41_assets_with_supply.toLocaleString()}
      />
      <Row
        label="Latest snapshot"
        value={ageS == null ? '—' : `${formatAge(ageS)} ago`}
        mono
      />
    </Panel>
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
      <div className="rounded-lg border border-warn-300 bg-warn-50 p-3 text-xs text-warn-700">
        Coverage snapshot pending — first refresh runs ~30s after process start,
        then every 5 min.
      </div>
    );
  }
  const onChain = rows.filter((r) => r.applies);
  const offChain = rows.filter((r) => !r.applies);
  return (
    <div>
      <div className="mb-2 flex items-baseline justify-between">
        <h3 className="text-[11px] font-semibold uppercase tracking-wider text-ink-faint">
          Ingest coverage — genesis → tip
        </h3>
        {asOf && (
          <span className="text-[10px] text-ink-faint">
            snapshot {timeSince(asOf)} ago
          </span>
        )}
      </div>
      <p className="mb-2 text-[11px] text-ink-faint">
        <strong>Coverage</strong> = verified completeness (ADR-0033). A green %
        is <strong>fully verified</strong>: the lake is hash-chained to the tip
        (substrate), every event shape is recognized, AND the served tier
        reconciles to the lake (Δ=0). <em>reconciling</em> (amber) = data is
        captured in the lake but the served tier hasn&apos;t reconciled yet —{' '}
        <em>captured, not yet verified</em>; the % shown is capture, not the
        verdict. <em>unverified</em> = only a gap-free liveness signal exists
        (the verifier hasn&apos;t run), which can read ~100% for sparse or
        partially-indexed sources.
      </p>
      <div className="overflow-hidden rounded-lg border border-line">
        <table className="w-full text-xs">
          <thead className="bg-surface-muted text-ink-faint">
            <tr>
              <th className="px-3 py-2 text-left font-medium">Source</th>
              <th className="px-3 py-2 text-right font-medium">Genesis</th>
              <th className="px-3 py-2 text-right font-medium">Earliest</th>
              <th className="px-3 py-2 text-right font-medium">Latest</th>
              <th className="px-3 py-2 text-right font-medium">Coverage</th>
              <th className="px-3 py-2 text-right font-medium">Entries</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {onChain.map((r) => {
              // ADR-0033 truthfulness: a source's coverage is only TRUSTWORTHY
              // once its completeness watermark (completeness_pct) is computed —
              // that's the substrate+projection-verified signal. Until then the
              // only other number is gap_free_pct, a LIVENESS proxy ("no large
              // gap detected"), which is NOT completeness: it reads ~100% for
              // sources that are merely sparse OR only recently/partially
              // indexed (e.g. 18 of 11.3M ledgers). Crucially we cannot tell
              // "sparse-but-complete" from "incomplete" without the watermark,
              // so we never dress an unverified figure up as a trustworthy
              // coverage bar — it's shown muted + tagged "unverified".
              // `ran` = the ADR-0033 verifier has computed a watermark for this
              // source. `reconciled` = the FULL verdict (substrate ∧ recognition
              // ∧ the served-tier projection reconciled to Δ=0) — the `complete`
              // flag. completeness_pct alone is DATA CAPTURE (the lake is
              // hash-chained to the tip); it can read 100% while the served tier
              // is still short, so the green "verified" bar is gated on
              // `reconciled`, NOT on the percentage.
              const ran = r.completeness_pct != null;
              const reconciled = r.completeness_complete === true;
              const pct =
                (ran
                  ? (r.completeness_pct as number)
                  : (r.coverage_pct ?? r.gap_free_pct ?? r.density_pct ?? 0)) *
                100;
              const tone = !ran
                ? ('pending' as const)
                : !reconciled
                  ? ('warn' as const)
                  : pct >= 99
                    ? 'ok'
                    : pct >= 50
                      ? 'warn'
                      : ('bad' as const);
              const colors = {
                ok: 'bg-ok-500 text-ok-700',
                warn: 'bg-warn-500 text-warn-700',
                bad: 'bg-bad-500 text-bad-700',
                pending: 'bg-line text-ink-muted',
              };
              return (
                <tr key={r.source}>
                  <td className="px-3 py-2 font-mono text-ink-body">
                    {r.source}
                  </td>
                  <td className="tnum px-3 py-2 text-right font-mono text-ink-muted">
                    {r.genesis_ledger?.toLocaleString() ?? '—'}
                  </td>
                  <td className="tnum px-3 py-2 text-right font-mono text-ink-body">
                    {r.earliest_ledger?.toLocaleString() ?? '—'}
                  </td>
                  <td className="tnum px-3 py-2 text-right font-mono text-ink-body">
                    {r.latest_ledger?.toLocaleString() ?? '—'}
                  </td>
                  <td
                    className="px-3 py-2 text-right"
                    title={
                      r.covered_ledgers !== undefined &&
                      r.expected_ledgers !== undefined
                        ? `${r.covered_ledgers.toLocaleString()} / ${r.expected_ledgers.toLocaleString()} ledgers covered by completed backfill ranges`
                        : undefined
                    }
                  >
                    {ran && reconciled ? (
                      <div className="inline-flex items-center justify-end gap-2">
                        <div className="h-1.5 w-16 overflow-hidden rounded-full bg-surface-subtle">
                          <div
                            className={`h-full ${colors[tone].split(' ')[0]}`}
                            style={{ width: `${Math.max(2, pct)}%` }}
                          />
                        </div>
                        <span className={`tnum ${colors[tone].split(' ')[1]}`}>
                          {pct.toFixed(1)}%
                        </span>
                      </div>
                    ) : ran ? (
                      <span
                        className="inline-flex items-center justify-end gap-1.5 text-warn-700"
                        title="Captured, not yet verified. The certified lake holds 100% of this source's data (substrate hash-chained to the tip), but the served tier has not reconciled to the lake yet (ADR-0033 complete=false) — so this is NOT yet fully verified. The hourly completeness verify + lake re-derive close the gap."
                      >
                        <span className="rounded-sm bg-line px-1 py-0.5 text-[10px] uppercase tracking-wide text-warn-700">
                          reconciling
                        </span>
                        <span className="tnum">{pct.toFixed(1)}% captured</span>
                      </span>
                    ) : (
                      <span
                        className="inline-flex items-center justify-end gap-1.5 text-ink-muted"
                        title="Completeness not yet verified (ADR-0033). The figure is a gap-free liveness signal — no large gap detected — which can read ~100% for sparse or only-partially-indexed sources. Verified completeness is pending the data-recovery backfills."
                      >
                        <span className="rounded-sm bg-line px-1 py-0.5 text-[10px] uppercase tracking-wide">
                          unverified
                        </span>
                        <span className="tnum">{pct.toFixed(1)}% gap-free</span>
                      </span>
                    )}
                  </td>
                  <td className="tnum px-3 py-2 text-right text-ink-muted">
                    {r.entries.toLocaleString()}
                  </td>
                </tr>
              );
            })}
            {offChain.map((r) => (
              <tr key={r.source} className="text-ink-muted">
                <td className="px-3 py-2 font-mono">{r.source}</td>
                <td
                  className="px-3 py-2 text-right text-[10px] italic"
                  colSpan={4}
                >
                  off-chain — no Stellar ledger context
                </td>
                <td className="tnum px-3 py-2 text-right">
                  {r.entries.toLocaleString()}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function SourceHealthTable({ rows }: { rows: IngestionSnapshot['sources'] }) {
  // Defensive shape-handling — Go marshals nil slices as `null`,
  // not `[]`, so a typed-as-array field can still arrive as null.
  const safeRows = rows ?? [];
  if (safeRows.length === 0) return null;
  return (
    <div>
      <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-ink-faint">
        Sources — {safeRows.length} registered
      </h3>
      <div className="overflow-hidden rounded-lg border border-line">
        <table className="w-full text-xs">
          <thead className="bg-surface-muted text-ink-faint">
            <tr>
              <th className="px-3 py-2 text-left font-medium">Source</th>
              <th className="px-3 py-2 text-left font-medium">Class</th>
              <th className="px-3 py-2 text-right font-medium">Entries 24h</th>
              <th className="px-3 py-2 text-right font-medium">Volume 24h</th>
              <th className="px-3 py-2 text-right font-medium">Markets</th>
              <th className="px-3 py-2 text-center font-medium">VWAP</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {safeRows.map((r) => {
              const classLabel = r.subclass
                ? `${r.class}/${r.subclass}`
                : r.class;
              const silent = r.include_in_vwap && r.entries_24h === 0;
              return (
                <tr key={r.name}>
                  <td className="px-3 py-2 font-mono text-ink-body">
                    {r.name}
                  </td>
                  <td className="px-3 py-2 text-ink-muted">{classLabel}</td>
                  <td
                    className={`tnum px-3 py-2 text-right ${
                      silent ? 'text-bad-700' : 'text-ink-body'
                    }`}
                  >
                    {r.entries_24h.toLocaleString()}
                  </td>
                  <td className="tnum px-3 py-2 text-right text-ink-muted">
                    {r.volume_24h_usd ? formatUSD(r.volume_24h_usd) : '—'}
                  </td>
                  <td className="tnum px-3 py-2 text-right text-ink-muted">
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

// Panel is a recessed sub-card used inside a RegionPanel for the
// metric well groups (live ledger, FX, market-cap, supply).
function Panel({
  title,
  accessory,
  children,
}: {
  title: string;
  accessory?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-lg border border-line bg-surface-muted p-4">
      <h3 className="mb-2 flex items-center justify-between gap-2 text-[11px] font-semibold uppercase tracking-wider text-ink-faint">
        <span>{title}</span>
        {accessory}
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
        className={`tnum text-ink ${mono ? 'font-mono text-xs' : ''} ${
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

// SectionHead is the page's between-block heading — an uppercase
// kicker with an optional right-aligned aside (window label) or
// action (a link). Mirrors the explorer's SectionHeader rhythm.
function SectionHead({
  children,
  aside,
  action,
}: {
  children: React.ReactNode;
  aside?: React.ReactNode;
  action?: React.ReactNode;
}) {
  return (
    <div className="mb-3 flex items-baseline justify-between gap-3">
      <h2 className="text-sm font-semibold uppercase tracking-wider text-ink-muted">
        {children}
        {aside && (
          <span className="ml-2 text-xs font-normal normal-case tracking-normal text-ink-faint">
            · {aside}
          </span>
        )}
      </h2>
      {action}
    </div>
  );
}

function toneFor(status?: ServiceStatus): {
  icon: typeof CheckCircle2;
  fg: string;
  ring: string;
  cardBg: string;
  cardBorder: string;
  badge: BadgeTone;
} {
  switch (status) {
    case 'ok':
      return {
        icon: CheckCircle2,
        fg: 'text-ok-700',
        ring: 'ring-ok-300/60',
        cardBg: 'bg-ok-50',
        cardBorder: 'border-ok-300',
        badge: 'ok',
      };
    case 'degraded':
      return {
        icon: AlertTriangle,
        fg: 'text-warn-700',
        ring: 'ring-warn-300/60',
        cardBg: 'bg-warn-50',
        cardBorder: 'border-warn-300',
        badge: 'warn',
      };
    case 'down':
      return {
        icon: XCircle,
        fg: 'text-bad-700',
        ring: 'ring-bad-300/60',
        cardBg: 'bg-bad-50',
        cardBorder: 'border-bad-300',
        badge: 'bad',
      };
    default:
      return {
        icon: Info,
        fg: 'text-ink-muted',
        ring: 'ring-line',
        cardBg: 'bg-surface-muted',
        cardBorder: 'border-line',
        badge: 'neutral',
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
