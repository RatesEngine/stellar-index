'use client';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { useAsset } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

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
        bodyClassName="text-sm text-slate-500"
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
        bodyClassName="text-sm text-slate-500"
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
      {noSupply ? (
        <p className="text-sm text-slate-500">
          No supply snapshot available for this asset. The supply
          observer may not have backfilled it yet.
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

          <MarketCapChartEmpty />


          {a.supply_basis && (
            <p className="text-xs text-slate-500 dark:text-slate-400">
              <span className="font-mono">supply_basis</span>: {a.supply_basis}
              {' — '}
              policy under ADR-0011 that produced these numbers.
            </p>
          )}

          {(a.fixed_number || a.max_number || a.is_unlimited != null) && (
            <div className="rounded-lg border border-slate-200 bg-slate-50 p-3 text-xs dark:border-slate-800 dark:bg-slate-900">
              <h4 className="mb-1 font-semibold uppercase tracking-wider text-slate-500">
                SEP-1 issuance declarations
              </h4>
              <p className="text-slate-600 dark:text-slate-400">
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

// MarketCapChartEmpty surfaces a placeholder where the historical
// market-cap chart will live. The data source for this is a join
// between asset_supply_history (per ADR-0011) and the per-asset
// USD price track — neither is fully backfilled yet for SEP-41
// tokens, so today the panel renders an explanation rather than
// an empty SVG. Once both sides land the chart replaces this body.
function MarketCapChartEmpty() {
  return (
    <div className="rounded-lg border border-dashed border-slate-300 bg-slate-50 p-4 text-sm text-slate-600 dark:border-slate-700 dark:bg-slate-900/50 dark:text-slate-400">
      <h4 className="mb-1 font-semibold uppercase tracking-wider text-xs text-slate-500">
        Market-cap timeline
      </h4>
      <p>
        Coming once the supply-history hypertable backfills join up
        with the per-asset USD price track. Today we surface only the
        latest snapshot above.
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
    <div className="rounded-lg border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900">
      <div className="text-xs uppercase tracking-wider text-slate-500">{label}</div>
      <div className="mt-1 font-mono text-xl font-semibold text-slate-900 dark:text-slate-100">
        {value}
      </div>
      {sublabel && (
        <div className="mt-1 truncate font-mono text-[11px] text-slate-500 dark:text-slate-400">
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
