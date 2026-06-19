'use client';

import { useState } from 'react';
import dynamic from 'next/dynamic';
import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';
import {
  Breadcrumbs,
  EmptyState,
  Skeleton,
  Stat,
  StatCell,
  StatGrid,
  Table,
  TableWrap,
  TBody,
  Td,
  Th,
  THead,
  TR,
} from '@/components/ui';
import { usePools, useSources, isOnChainSource } from '@/api/hooks';
import { DonutChart } from '@/components/charts/DonutChart';
import { formatCompact } from '@/lib/format';
import {
  type Envelope,
  type Ledger,
  type LedgersPage,
  relativeAge,
  stroopsToXlm,
} from '../explorer-shared';

const LineChart = dynamic(
  () => import('@/components/charts/LineChart').then((m) => m.LineChart),
  { ssr: false, loading: () => <div className="h-[320px]" /> },
);

interface NetworkStats {
  volume_24h_usd?: string | null;
  markets_count_24h: number;
  assets_indexed: number;
  latest_ledger: number;
  exchange_sources: number;
  total_sources: number;
}

interface ThroughputBucket {
  day: string;
  ledgers: number;
  txs: number;
  ops: number;
  events: number;
}

interface ThroughputResp {
  window_days: number;
  buckets: ThroughputBucket[];
}

interface OpTypeStat {
  type: string;
  count: number;
}

interface OperationsResp {
  op_type_stats?: OpTypeStat[];
}

type Metric = 'ops' | 'txs' | 'events' | 'ledgers';
const METRICS: { key: Metric; label: string }[] = [
  { key: 'ops', label: 'Operations' },
  { key: 'txs', label: 'Transactions' },
  { key: 'events', label: 'Contract events' },
  { key: 'ledgers', label: 'Ledgers' },
];
const WINDOWS = [30, 90, 365];

export function NetworkView() {
  const [metric, setMetric] = useState<Metric>('ops');
  const [windowDays, setWindowDays] = useState(30);

  const statsQ = useQuery<NetworkStats>({
    queryKey: ['/v1/network/stats'],
    queryFn: async () => (await apiGet<Envelope<NetworkStats>>('/v1/network/stats', {})).data,
    staleTime: 30_000,
  });

  const ledgersQ = useQuery<LedgersPage>({
    queryKey: ['/v1/ledgers', 'network-strip'],
    queryFn: async () => (await apiGet<Envelope<LedgersPage>>('/v1/ledgers', { limit: 12 })).data,
    staleTime: 10_000,
  });

  const opStatsQ = useQuery<OpTypeStat[]>({
    queryKey: ['/v1/operations', 'type-mix'],
    queryFn: async () =>
      (await apiGet<Envelope<OperationsResp>>('/v1/operations', { limit: 1 })).data
        .op_type_stats ?? [],
    staleTime: 60_000,
    retry: false,
  });

  const tpQ = useQuery<ThroughputResp>({
    queryKey: ['/v1/network/throughput', windowDays],
    queryFn: async () =>
      (await apiGet<Envelope<ThroughputResp>>('/v1/network/throughput', { window_days: windowDays })).data,
    staleTime: 60_000,
  });

  const buckets = tpQ.data?.buckets ?? [];
  const points = buckets.map((b) => ({
    time: Math.floor(Date.parse(`${b.day}T00:00:00Z`) / 1000),
    value: b[metric],
  }));
  const total = buckets.reduce((s, b) => s + b[metric], 0);

  const s = statsQ.data;
  const tip = ledgersQ.data?.ledgers?.[0];

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-1">
        <Breadcrumbs items={[{ label: 'Home', href: '/' }, { label: 'Network' }]} />
        <h1 className="text-2xl font-semibold tracking-tight text-ink">Network</h1>
        <p className="max-w-2xl text-sm text-ink-muted">
          A live snapshot of the Stellar network — ledger chain state, throughput
          over time, what the network is doing right now, and the markets and
          sources feeding the lake.
        </p>
      </header>

      <HeroStats stats={s} tip={tip} />

      <Panel
        title="Throughput"
        source={asExample('/v1/network/throughput', { window_days: windowDays })}
        bodyClassName="space-y-4"
      >
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex gap-1">
            {METRICS.map((m) => (
              <button
                key={m.key}
                onClick={() => setMetric(m.key)}
                className={`rounded-md px-2.5 py-1 text-xs ${
                  metric === m.key
                    ? 'bg-brand-600 text-white'
                    : 'border border-line text-ink-body hover:border-brand-500'
                }`}
              >
                {m.label}
              </button>
            ))}
          </div>
          <div className="ml-auto flex gap-1">
            {WINDOWS.map((d) => (
              <button
                key={d}
                onClick={() => setWindowDays(d)}
                className={`rounded-md px-2.5 py-1 text-xs ${
                  windowDays === d
                    ? 'bg-surface-strong text-ink'
                    : 'border border-line text-ink-body hover:border-brand-500'
                }`}
              >
                {d}d
              </button>
            ))}
          </div>
        </div>

        {tpQ.isLoading && <Skeleton className="h-[320px] w-full" />}
        {tpQ.isError && <p className="text-sm text-ink-muted">Throughput is unavailable right now.</p>}
        {tpQ.data && points.length === 0 && (
          <p className="text-sm text-ink-muted">No ledgers in this window yet.</p>
        )}
        {points.length > 0 && (
          <>
            <p className="text-sm text-ink-body">
              <span className="font-mono tabular-nums">{formatCompact(total)}</span>{' '}
              {METRICS.find((m) => m.key === metric)?.label.toLowerCase()} over the last {windowDays} days
            </p>
            <LineChart
              data={points}
              height={320}
              positive
              ariaLabel={`Daily ${metric} on the Stellar network over the last ${windowDays} days`}
            />
          </>
        )}
      </Panel>

      <div className="grid gap-6 lg:grid-cols-2">
        <OperationMix stats={opStatsQ.data} loading={opStatsQ.isLoading} error={opStatsQ.isError} />
        <LatestLedgers ledgers={ledgersQ.data?.ledgers} loading={ledgersQ.isLoading} error={ledgersQ.isError} />
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <TopMarkets />
        <ActiveSources />
      </div>

      <NetworkComposition />

      <DigDeeper />
    </div>
  );
}

// HeroStats — the network at-a-glance, blending the aggregate
// /v1/network/stats snapshot with the chain-state fields off the
// freshest ledger header (total XLM, fee pool, protocol version).
function HeroStats({ stats: s, tip }: { stats?: NetworkStats; tip?: Ledger }) {
  // Stellar ON-CHAIN 24h volume (SDEX + Soroban DEXes), summed from the
  // DEX-subclass /v1/sources. /v1/network/stats.volume_24h_usd is the
  // ALL-source total (CEX feeds dominate) — not "Stellar volume".
  // Matches the home strip. (The "Volume by venue type" donut below
  // still shows the full CEX-vs-on-chain split by design.)
  const { data: sources } = useSources(undefined, true);
  const onChain = (sources ?? []).filter(isOnChainSource);
  const stellarVolume = onChain
    .filter((x) => x.subclass === 'dex')
    .reduce((sum, x) => sum + (x.volume_24h_usd ? Number(x.volume_24h_usd) : 0), 0);
  // Stellar markets = active (venue, pair) pools summed across on-chain
  // DEX venues (a pair traded on two DEXes counts as two markets).
  const stellarMarkets = onChain.reduce((sum, x) => sum + (x.markets_count_24h ?? 0), 0);
  const stellarSources = onChain.length;
  return (
    <StatGrid cols={4}>
      <StatCell>
        <Stat
          label="Latest ledger"
          value={s ? `#${s.latest_ledger.toLocaleString()}` : '—'}
          sub={tip ? `${relativeAge(tip.close_time)} · protocol v${tip.protocol_version}` : undefined}
        />
      </StatCell>
      <StatCell>
        <Stat
          label="24h volume"
          value={stellarVolume > 0 ? `$${formatCompact(stellarVolume)}` : '—'}
          sub="Stellar on-chain"
        />
      </StatCell>
      <StatCell>
        <Stat
          label="Stellar markets (24h)"
          value={stellarMarkets > 0 ? formatCompact(stellarMarkets) : '—'}
          sub="across DEX venues"
        />
      </StatCell>
      <StatCell>
        <Stat label="Assets indexed" value={s ? formatCompact(s.assets_indexed) : '—'} />
      </StatCell>
      <StatCell>
        <Stat
          label="Total XLM"
          value={tip?.total_coins ? xlmCompact(tip.total_coins) : '—'}
          sub={tip?.fee_pool ? `${xlmCompact(tip.fee_pool)} XLM in fee pool` : undefined}
        />
      </StatCell>
      <StatCell>
        <Stat
          label="Base fee"
          value={tip?.base_fee != null ? `${tip.base_fee.toLocaleString()}` : '—'}
          sub="stroops / op"
        />
      </StatCell>
      <StatCell>
        <Stat
          label="Stellar sources"
          value={stellarSources > 0 ? String(stellarSources) : '—'}
          sub="on-chain venues"
        />
      </StatCell>
      <StatCell>
        <Stat
          label="Network"
          value="Pubnet"
          sub={tip ? `${formatCompact(tip.tx_count)} tx · ${formatCompact(tip.op_count)} ops last ledger` : 'mainnet'}
        />
      </StatCell>
    </StatGrid>
  );
}

// OperationMix — proportional bars of operation types over the
// trailing ~24h, straight from the /v1/operations type aggregate.
// Far more legible than the raw chip cloud on /operations.
function OperationMix({
  stats,
  loading,
  error,
}: {
  stats?: OpTypeStat[];
  loading: boolean;
  error: boolean;
}) {
  const sorted = [...(stats ?? [])].sort((a, b) => b.count - a.count).slice(0, 10);
  const max = sorted.reduce((m, x) => Math.max(m, x.count), 0) || 1;
  const grand = (stats ?? []).reduce((sum, x) => sum + x.count, 0) || 1;

  return (
    <Panel
      title="Operation mix — trailing 24h"
      source={asExample('/v1/operations', { limit: 1 })}
      bodyClassName="space-y-2.5"
    >
      {loading && <Skeleton className="h-40 w-full" />}
      {error && <p className="text-sm text-ink-muted">Operation stats are unavailable right now.</p>}
      {!loading && !error && sorted.length === 0 && (
        <EmptyState title="No operations in the last 24h." />
      )}
      {sorted.map((st) => {
        const pct = (st.count / grand) * 100;
        return (
          <Link
            key={st.type}
            href="/operations"
            className="group block"
            title={`${st.count.toLocaleString()} ${st.type} ops (${pct.toFixed(1)}%)`}
          >
            <div className="mb-0.5 flex items-baseline justify-between gap-2 text-xs">
              <code className="truncate text-ink-body group-hover:text-brand-600">{st.type}</code>
              <span className="shrink-0 font-mono tabular-nums text-ink-muted">
                {formatCompact(st.count)}
                <span className="ml-1 text-ink-faint">{pct.toFixed(1)}%</span>
              </span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-surface-muted">
              <div
                className="h-full rounded-full bg-brand-500 transition-all group-hover:bg-brand-600"
                style={{ width: `${(st.count / max) * 100}%` }}
              />
            </div>
          </Link>
        );
      })}
    </Panel>
  );
}

// LatestLedgers — the chain tip, newest first. Each row deep-links
// to the per-ledger explorer page.
function LatestLedgers({
  ledgers,
  loading,
  error,
}: {
  ledgers?: Ledger[];
  loading: boolean;
  error: boolean;
}) {
  const rows = (ledgers ?? []).slice(0, 12);
  return (
    <Panel
      title="Latest ledgers"
      source={asExample('/v1/ledgers', { limit: 12 })}
      bodyClassName="-mx-4 -mb-4"
    >
      {loading && <div className="px-4 pb-4"><Skeleton className="h-40 w-full" /></div>}
      {error && <p className="px-4 pb-4 text-sm text-ink-muted">Ledgers are unavailable right now.</p>}
      {!loading && !error && rows.length === 0 && (
        <div className="px-4 pb-4"><EmptyState title="No ledgers yet." /></div>
      )}
      {rows.length > 0 && (
        <TableWrap className="rounded-none border-0">
          <Table>
            <THead>
              <TR className="hover:bg-transparent">
                <Th>Ledger</Th>
                <Th align="right">Txs</Th>
                <Th align="right">Ops</Th>
                <Th align="right">Events</Th>
                <Th align="right">Age</Th>
              </TR>
            </THead>
            <TBody>
              {rows.map((l) => (
                <TR key={l.sequence}>
                  <Td>
                    <Link
                      href={`/ledger?seq=${l.sequence}`}
                      className="font-mono tabular-nums text-brand-600 hover:underline"
                    >
                      #{l.sequence.toLocaleString()}
                    </Link>
                  </Td>
                  <Td align="right">{l.tx_count.toLocaleString()}</Td>
                  <Td align="right">{l.op_count.toLocaleString()}</Td>
                  <Td align="right">{l.soroban_event_count.toLocaleString()}</Td>
                  <Td align="right" className="text-ink-muted">{relativeAge(l.close_time)}</Td>
                </TR>
              ))}
            </TBody>
          </Table>
        </TableWrap>
      )}
      <div className="px-4 pt-3 text-xs">
        <Link href="/ledgers" className="text-brand-600 hover:underline">
          All ledgers →
        </Link>
      </div>
    </Panel>
  );
}

// TopMarkets — top Stellar on-chain pools by trailing-24h USD volume.
// Sourced from /v1/pools (DEX-subclass only, server-side scoped), NOT
// /v1/markets — the latter aggregates across off-chain CEX reference
// feeds (BTC/USDT etc.) that aren't Stellar markets at all.
function TopMarkets() {
  const { data, isLoading, isError } = usePools(8, 'volume_24h_usd_desc');
  const rows = (data ?? []).slice(0, 8);
  return (
    <Panel
      title="Top Stellar markets"
      hint="On-chain DEX pools by trailing-24h volume — SDEX + Soroban DEXes only."
      source={asExample('/v1/pools', { limit: 8, order_by: 'volume_24h_usd_desc' })}
      bodyClassName="-mx-4 -mb-4"
    >
      {isLoading && <div className="px-4 pb-4"><Skeleton className="h-40 w-full" /></div>}
      {isError && <p className="px-4 pb-4 text-sm text-ink-muted">Markets are unavailable right now.</p>}
      {!isLoading && !isError && rows.length === 0 && (
        <div className="px-4 pb-4"><EmptyState title="No Stellar markets returned." /></div>
      )}
      {rows.length > 0 && (
        <TableWrap className="rounded-none border-0">
          <Table>
            <THead>
              <TR className="hover:bg-transparent">
                <Th>Pair</Th>
                <Th>Venue</Th>
                <Th align="right">24h volume</Th>
              </TR>
            </THead>
            <TBody>
              {rows.map((m) => {
                const slug = `${m.base}~${m.quote}`;
                return (
                  <TR key={`${m.source}:${slug}`}>
                    <Td>
                      <Link
                        href={`/markets/${encodeURIComponent(slug)}`}
                        className="font-medium text-ink hover:text-brand-600"
                      >
                        {shortAsset(m.base)}
                        <span className="mx-1 text-ink-faint">/</span>
                        {shortAsset(m.quote)}
                      </Link>
                    </Td>
                    <Td>
                      <Link
                        href={`/dexes/${encodeURIComponent(m.source)}`}
                        className="text-ink-muted hover:text-brand-600"
                      >
                        {m.source}
                      </Link>
                    </Td>
                    <Td align="right" className="font-mono">
                      {m.volume_24h_usd ? `$${formatCompact(Number(m.volume_24h_usd))}` : '—'}
                    </Td>
                  </TR>
                );
              })}
            </TBody>
          </Table>
        </TableWrap>
      )}
      <div className="px-4 pt-3 text-xs">
        <Link href="/dexes" className="text-brand-600 hover:underline">
          All DEX pools →
        </Link>
      </div>
    </Panel>
  );
}

// ActiveSources — Stellar on-chain sources ranked by trailing-24h USD
// volume. Off-chain reference feeds (CEX / FX / aggregators) are
// excluded — they dominate raw volume but aren't Stellar activity;
// see /exchanges for those.
function ActiveSources() {
  const { data, isLoading, isError } = useSources(undefined, true);
  const rows = [...(data ?? [])]
    .filter(isOnChainSource)
    .sort((a, b) => Number(b.volume_24h_usd ?? 0) - Number(a.volume_24h_usd ?? 0))
    .slice(0, 8);
  return (
    <Panel
      title="Most active Stellar sources"
      hint="On-chain venues only — CEX / aggregator / FX feeds live on /exchanges."
      source={asExample('/v1/sources', { include: 'stats' })}
      bodyClassName="-mx-4 -mb-4"
    >
      {isLoading && <div className="px-4 pb-4"><Skeleton className="h-40 w-full" /></div>}
      {isError && <p className="px-4 pb-4 text-sm text-ink-muted">Sources are unavailable right now.</p>}
      {!isLoading && !isError && rows.length === 0 && (
        <div className="px-4 pb-4"><EmptyState title="No sources reporting volume." /></div>
      )}
      {rows.length > 0 && (
        <TableWrap className="rounded-none border-0">
          <Table>
            <THead>
              <TR className="hover:bg-transparent">
                <Th>Source</Th>
                <Th>Class</Th>
                <Th align="right">24h volume</Th>
              </TR>
            </THead>
            <TBody>
              {rows.map((src) => (
                <TR key={src.name}>
                  <Td>
                    <Link
                      href={`/sources/${encodeURIComponent(src.name)}`}
                      className="font-medium text-ink hover:text-brand-600"
                    >
                      {src.name}
                    </Link>
                  </Td>
                  <Td className="text-ink-muted">{src.class}</Td>
                  <Td align="right" className="font-mono">
                    {src.volume_24h_usd ? `$${formatCompact(Number(src.volume_24h_usd))}` : '—'}
                  </Td>
                </TR>
              ))}
            </TBody>
          </Table>
        </TableWrap>
      )}
      <div className="px-4 pt-3 text-xs">
        <Link href="/sources" className="text-brand-600 hover:underline">
          All sources →
        </Link>
      </div>
    </Panel>
  );
}

// NetworkComposition — trailing-24h on-chain USD volume split by
// Stellar venue (sdex / aquarius / phoenix / soroswap / comet).
// Off-chain feeds are excluded (this is the Stellar page); with CEX
// gone the old venue-TYPE split collapses to a single "On-chain DEX"
// slice, so we break down by venue NAME instead — the useful cut.
// Derived from the same /v1/sources?include=stats the directory uses.
function NetworkComposition() {
  const { data, isLoading, isError } = useSources(undefined, true);
  const slices = (data ?? [])
    .filter(isOnChainSource)
    .map((s) => ({ label: s.name, value: Number(s.volume_24h_usd ?? 0) }))
    .filter((x) => Number.isFinite(x.value) && x.value > 0)
    .sort((a, b) => b.value - a.value);
  const total = slices.reduce((sum, s) => sum + s.value, 0);

  return (
    <Panel
      title="Volume by Stellar venue — 24h"
      hint="Share of trailing-24h on-chain USD volume across Stellar DEX venues."
      source={asExample('/v1/sources', { include: 'stats' })}
    >
      {isLoading && <Skeleton className="h-40 w-full" />}
      {isError && <p className="text-sm text-ink-muted">Composition is unavailable right now.</p>}
      {!isLoading && !isError && slices.length === 0 && (
        <EmptyState title="No on-chain volume in the last 24h." />
      )}
      {slices.length > 0 && (
        <DonutChart
          data={slices}
          centerLabel={`$${formatCompact(total)}`}
          centerSub="24h vol"
          formatValue={(n) => `$${formatCompact(n)}`}
        />
      )}
    </Panel>
  );
}

// DigDeeper — a navigation grid into the analytical surfaces that
// hang off the network: the explorer entities + the price-quality
// feeds. Makes the cross-page structure discoverable rather than
// buried in the header nav.
const DEEPER: { href: string; title: string; blurb: string }[] = [
  { href: '/operations', title: 'Operations', blurb: 'Every decoded op, newest first' },
  { href: '/transactions', title: 'Transactions', blurb: 'Recent transactions across the chain' },
  { href: '/ledgers', title: 'Ledgers', blurb: 'The full ledger chain to genesis' },
  { href: '/accounts', title: 'Accounts', blurb: 'Account directory + activity' },
  { href: '/contracts', title: 'Contracts', blurb: 'Soroban contracts + their events' },
  { href: '/protocols', title: 'Protocols', blurb: 'Per-protocol verified coverage' },
  { href: '/dexes', title: 'DEXes', blurb: 'On-chain pools + AMMs' },
  { href: '/lending', title: 'Lending', blurb: 'Blend pools — TVL, utilization, APY' },
  { href: '/oracles', title: 'Oracles', blurb: 'Reflector / Band / Redstone feeds' },
  { href: '/mev', title: 'MEV', blurb: 'Detected arbitrage cycles' },
  { href: '/anomalies', title: 'Anomalies', blurb: 'Price-freeze + outlier events' },
  { href: '/divergences', title: 'Divergences', blurb: 'Cross-checks vs reference venues' },
];

function DigDeeper() {
  return (
    <Panel title="Dig deeper" bodyClassName="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4">
      {DEEPER.map((d) => (
        <Link
          key={d.href}
          href={d.href}
          className="group rounded-card border border-line bg-surface p-3 transition-colors hover:border-brand-500 hover:bg-surface-muted"
        >
          <div className="text-sm font-medium text-ink group-hover:text-brand-600">{d.title}</div>
          <div className="mt-0.5 text-xs text-ink-muted">{d.blurb}</div>
        </Link>
      ))}
    </Panel>
  );
}

// xlmCompact renders a stroop integer string as a compact XLM figure
// (e.g. "50.5B"). total_coins is ~117× past 2^53, so divide as BigInt
// first (truncating sub-XLM), then the quotient (~5e10) is safely
// inside the float range for compact display. (ADR-0003.)
function xlmCompact(stroops: string): string {
  const t = stroops.trim();
  if (!/^-?\d+$/.test(t)) return stroopsToXlm(stroops);
  return formatCompact(Number(BigInt(t) / 10_000_000n));
}

function shortAsset(canonical: string | undefined | null): string {
  if (!canonical) return '—';
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.replace('fiat:', '');
  if (canonical.startsWith('crypto:')) return canonical.replace('crypto:', '');
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return canonical;
  return canonical.slice(0, dashIx);
}
