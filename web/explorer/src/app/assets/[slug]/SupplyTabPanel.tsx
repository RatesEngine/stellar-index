'use client';

import dynamic from 'next/dynamic';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';
import { useAsset, useAssetSupply, type AssetSupply } from '@/api/hooks';
import { formatCompact } from '@/lib/format';
import { type Envelope } from '../../explorer-shared';

// Lazy-load the chart (~155 KB lightweight-charts) — only the supply
// tab needs it, and only when there's market-cap history to draw.
const MarketCapLineChart = dynamic(
  () => import('@/components/charts/LineChart').then((m) => m.LineChart),
  { ssr: false, loading: () => <div className="h-[260px]" /> },
);

/**
 * SupplyTabPanel — backs the "Supply" tab on /assets/[slug].
 *
 * Renders the F2 supply-derivation fields per ADR-0011:
 * circulating, total, max supply (smallest-integer-unit decimal
 * strings, divided by 10^decimals at display time), market cap,
 * fully-diluted valuation, and the supply_basis tag identifying
 * which policy produced the numbers. SEP-1 issuance declarations
 * (fixed_number / max_number / is_unlimited) appear when the
 * issuer published them.
 */
export function SupplyTabPanel({ assetID }: { assetID: string }) {
  const asset = useAsset(assetID);
  // Live on-chain supply (ADR-0034 supply_flows). Independent of the F2
  // snapshot below; 404s for unmapped classic assets → section omitted.
  const onchain = useAssetSupply(assetID);

  if (asset.isError) {
    return (
      <Panel
        title="Supply"
        source={asExample('/v1/assets/{asset_id}', { asset_id: assetID })}
        bodyClassName="text-sm text-down-strong"
      >
        Failed to load supply data.
      </Panel>
    );
  }

  if (asset.isLoading) {
    return (
      <Panel
        title="Supply"
        source={asExample('/v1/assets/{asset_id}', { asset_id: assetID })}
        bodyClassName="text-sm text-ink-muted"
      >
        Loading…
      </Panel>
    );
  }

  const a = asset.data;
  if (!a) {
    return (
      <Panel
        title="Supply"
        source={asExample('/v1/assets/{asset_id}', { asset_id: assetID })}
        bodyClassName="text-sm text-ink-muted"
      >
        No asset detail available.
      </Panel>
    );
  }

  const decimals = a.decimals ?? 7;
  const circulating = parseSmallest(a.circulating_supply, decimals);
  const total = parseSmallest(a.total_supply, decimals);
  const max = parseSmallest(a.max_supply, decimals);

  const noSupply =
    a.circulating_supply == null && a.total_supply == null && a.max_supply == null;

  return (
    <Panel
      title="Supply"
      source={asExample('/v1/assets/{asset_id}', { asset_id: assetID })}
      bodyClassName="space-y-4"
    >
      {onchain.data && <OnChainSupply data={onchain.data} decimals={decimals} />}
      {noSupply ? (
        <p className="text-sm text-ink-muted">
          {onchain.data
            ? 'No ADR-0011 circulating/max breakdown for this asset yet — the live on-chain total above is sourced directly from mint/burn flows.'
            : 'No supply snapshot available for this asset. The supply observer may not have backfilled it yet.'}
        </p>
      ) : (
        <>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <Metric
              label="Circulating"
              value={circulating != null ? formatCompact(circulating) : '—'}
              sublabel={`In smallest unit: ${a.circulating_supply ?? '—'}`}
            />
            <Metric
              label="Total"
              value={total != null ? formatCompact(total) : '—'}
              sublabel={a.is_unlimited ? 'Issuer asserts unbounded' : ''}
            />
            <Metric
              label="Max"
              value={max != null ? formatCompact(max) : '—'}
              sublabel={a.is_unlimited === true ? 'Unlimited' : ''}
            />
          </div>

          {(a.market_cap_usd || a.fdv_usd) && (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <Metric
                label="Market cap (USD)"
                value={a.market_cap_usd ? formatUSD(a.market_cap_usd) : '—'}
                sublabel="circulating × USD price"
              />
              <Metric
                label="Fully diluted (USD)"
                value={a.fdv_usd ? formatUSD(a.fdv_usd) : '—'}
                sublabel="max supply × USD price"
              />
            </div>
          )}

          <MarketCapChart assetID={assetID} />


          {a.supply_basis && (
            <p className="text-xs text-ink-muted">
              <span className="font-mono">supply_basis</span>: {a.supply_basis}
              {' — '}
              policy under ADR-0011 that produced these numbers.
            </p>
          )}

          {(a.fixed_number || a.max_number || a.is_unlimited != null) && (
            <div className="rounded-lg border border-line bg-surface-muted p-3 text-xs">
              <h4 className="mb-1 font-semibold uppercase tracking-wider text-ink-muted">
                SEP-1 issuance declarations
              </h4>
              <p className="text-ink-body">
                What the issuer pledged in their <span className="font-mono">stellar.toml</span>
                — distinct from the live-ledger numbers above.
              </p>
              <ul className="mt-2 space-y-1 font-mono">
                {a.fixed_number && (
                  <li>fixed_number = {a.fixed_number}</li>
                )}
                {a.max_number && (
                  <li>max_number = {a.max_number}</li>
                )}
                {a.is_unlimited != null && (
                  <li>is_unlimited = {a.is_unlimited ? 'true' : 'false'}</li>
                )}
              </ul>
            </div>
          )}
        </>
      )}
    </Panel>
  );
}

// MarketCapChart renders the historical market-cap timeline for an
// asset: daily USD price × daily circulating supply (the supply_1d
// CAGG), served by GET /v1/chart?price_type=market_cap. On-chain assets
// (native / classic / Soroban) chart; off-chain crypto:* reference
// assets (no on-chain supply) return an empty series → a concise note.
function MarketCapChart({ assetID }: { assetID: string }) {
  const q = useQuery<{ points: { t: string; p: string }[] }>({
    queryKey: ['/v1/chart', 'market_cap', assetID],
    retry: false,
    queryFn: async () => {
      const env = await apiGet<Envelope<{ points: { t: string; p: string }[] }>>('/v1/chart', {
        asset: assetID,
        quote: 'fiat:USD',
        price_type: 'market_cap',
        timeframe: '1y',
        granularity: '1d',
      });
      return env.data;
    },
    staleTime: 60_000,
  });

  const points = (q.data?.points ?? []).map((pt) => ({
    time: Math.floor(Date.parse(pt.t) / 1000),
    value: Number(pt.p),
  }));

  return (
    <div className="rounded-lg border border-line bg-surface p-4">
      <h4 className="mb-2 font-semibold uppercase tracking-wider text-xs text-ink-muted">
        Market-cap timeline
      </h4>
      {q.isLoading && <div className="h-[260px]" />}
      {!q.isLoading && points.length < 2 && (
        <p className="text-sm text-ink-muted">
          No market-cap history for this asset — it needs both an on-chain
          circulating supply and a USD price track over time.
        </p>
      )}
      {points.length >= 2 && (
        <MarketCapLineChart
          data={points}
          ariaLabel={`Daily USD market cap for ${assetID} over the last year`}
        />
      )}
    </div>
  );
}

// OnChainSupply renders the live decode-at-ingest supply (ADR-0034):
// Σmint − Σburn − Σclawback from the supply_flows lake, current to the
// latest ledger with no rollup refresh. This is the universal supply
// number available for every token (vs the ADR-0011 F2 fields below,
// which only exist for tracked assets). For native XLM the source is the
// ledger header's total_coins (no mint/burn breakdown).
function OnChainSupply({ data, decimals }: { data: AssetSupply; decimals: number }) {
  const native = data.source === 'ledger_total_coins';
  const total = parseSmallest(data.total_supply, decimals);
  const mint = parseSmallest(data.mint_total, decimals);
  const burn = parseSmallest(data.burn_total, decimals);
  const clawback = parseSmallest(data.clawback_total, decimals);
  return (
    <div className="rounded-lg border border-up/30 bg-up-subtle/50 p-3">
      <h4 className="mb-2 text-xs font-semibold uppercase tracking-wider text-up">
        On-chain supply (live)
      </h4>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Metric
          label="Total"
          value={total != null ? formatCompact(total) : '—'}
          sublabel={native ? 'ledger total_coins' : `${data.flow_count.toLocaleString()} flows`}
        />
        {!native && (
          <>
            <Metric label="Minted" value={mint != null ? formatCompact(mint) : '—'} />
            <Metric label="Burned" value={burn != null ? formatCompact(burn) : '—'} />
            <Metric
              label="Clawed back"
              value={clawback != null ? formatCompact(clawback) : '—'}
            />
          </>
        )}
      </div>
      <p className="mt-2 text-[11px] text-up/80">
        {native
          ? 'Native XLM total from the ledger header — current to the latest ledger.'
          : 'Σ mint − burn − clawback from the supply_flows lake (ADR-0034), current to the latest ledger — no refresh lag.'}
      </p>
    </div>
  );
}

function Metric({
  label,
  value,
  sublabel,
}: {
  label: string;
  value: string;
  sublabel?: string;
}) {
  return (
    <div className="rounded-lg border border-line bg-surface p-3">
      <div className="text-xs uppercase tracking-wider text-ink-muted">{label}</div>
      <div className="mt-1 font-mono text-xl font-semibold text-ink">
        {value}
      </div>
      {sublabel && (
        <div className="mt-1 truncate font-mono text-[11px] text-ink-muted">
          {sublabel}
        </div>
      )}
    </div>
  );
}

// parseSmallest converts a smallest-integer-unit decimal string
// (stroops for classic / native; contract-defined for SEP-41) to
// a number for display. Returns null when the string is missing
// or not finite. Display-only — never used for further arithmetic
// (CLAUDE.md invariant #1: precision lives in the string).
function parseSmallest(s: string | null | undefined, decimals: number): number | null {
  if (s == null) return null;
  const n = Number(s);
  if (!Number.isFinite(n)) return null;
  return n / 10 ** decimals;
}

function formatUSD(s: string): string {
  const n = Number(s);
  if (!Number.isFinite(n)) return s;
  return `$${formatCompact(n)}`;
}
