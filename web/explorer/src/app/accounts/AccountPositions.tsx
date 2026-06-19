'use client';

import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { AssetLink } from '@/components/AssetLink';
import { DonutChart } from '@/components/charts/DonutChart';
import {
  Stat,
  StatGrid,
  StatCell,
  Table,
  TableWrap,
  TBody,
  Td,
  Th,
  THead,
  TR,
} from '@/components/ui';
import { apiGet, asExample } from '@/api/client';
import { formatCompact } from '@/lib/format';

// Mirror of the slice of AccountStateResp we need (kept local so this
// reads from the SAME React Query cache key the AccountView state panel
// populates — no extra round trip).
interface AccountStateResp {
  account_id: string;
  exists: boolean;
  balance?: string;
  trustlines?: { asset: string; balance: string }[];
}

interface BatchPrice {
  asset_id: string;
  price: string | null;
}

interface Holding {
  asset: string;
  amount: number; // display units (stroops → ÷1e7)
  priceUSD: number | null;
  valueUSD: number | null;
}

const usdFmt = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  maximumFractionDigits: 2,
});

/**
 * AccountPositions — the account's portfolio: native XLM + every
 * trustline balance, valued in USD via /v1/price/batch (the same VWAP
 * the rest of the site prices with). Shows a total, a per-asset table
 * sorted by value with % allocation, and an allocation donut. Holdings
 * we can't price (illiquid trustlines) still appear, valued "—" and
 * excluded from the allocation split.
 */
export function AccountPositions({ id }: { id: string }) {
  // Shares the AccountView state-panel cache (same queryKey) — RQ
  // dedupes, so this doesn't add a request.
  const stateQ = useQuery<AccountStateResp>({
    queryKey: ['/v1/accounts/{id}', id],
    enabled: id.length > 0,
    retry: false,
    queryFn: async () =>
      (await apiGet<{ data: AccountStateResp }>(`/v1/accounts/${encodeURIComponent(id)}`)).data,
    staleTime: 30_000,
  });

  // Asset_ids held: native (when there's a balance) + each trustline.
  const state = stateQ.data;
  const assetIds: string[] = [];
  if (state?.exists) {
    if (state.balance && Number(state.balance) > 0) assetIds.push('native');
    for (const t of state.trustlines ?? []) {
      if (Number(t.balance) > 0) assetIds.push(t.asset);
    }
  }

  const pricesQ = useQuery<Record<string, number>>({
    queryKey: ['/v1/price/batch', 'positions', assetIds.join(',')],
    enabled: assetIds.length > 0,
    retry: false,
    staleTime: 30_000,
    queryFn: async () => {
      const env = await apiGet<{ data: BatchPrice[] }>('/v1/price/batch', {
        asset_ids: assetIds.join(','),
        quote: 'fiat:USD',
      });
      const m: Record<string, number> = {};
      for (const row of env.data ?? []) {
        const p = row.price ? Number(row.price) : NaN;
        if (!(Number.isFinite(p) && p > 0)) continue;
        m[row.asset_id] = p;
        // /v1/price/batch echoes native XLM as `crypto:XLM`; alias both
        // forms so a `native` holding resolves its price.
        if (row.asset_id === 'crypto:XLM') m.native = p;
        if (row.asset_id === 'native') m['crypto:XLM'] = p;
      }
      return m;
    },
  });

  if (stateQ.isLoading) {
    return (
      <Panel title="Positions" bodyClassName="text-sm text-ink-muted">
        Loading balances…
      </Panel>
    );
  }
  if (!state?.exists) {
    // No live state captured — the existing State panel already
    // explains the ledger-entry-window caveat, so stay quiet here.
    return null;
  }

  const priceMap = pricesQ.data ?? {};
  const holdings: Holding[] = assetIds.map((asset) => {
    const raw = asset === 'native' ? state.balance ?? '0' : (state.trustlines ?? []).find((t) => t.asset === asset)?.balance ?? '0';
    const amount = Number(raw) / 1e7;
    const priceUSD = priceMap[asset] ?? null;
    const valueUSD = priceUSD != null && Number.isFinite(amount) ? amount * priceUSD : null;
    return { asset, amount, priceUSD, valueUSD };
  });
  holdings.sort((a, b) => (b.valueUSD ?? -1) - (a.valueUSD ?? -1));

  const total = holdings.reduce((sum, h) => sum + (h.valueUSD ?? 0), 0);
  const pricedCount = holdings.filter((h) => h.valueUSD != null).length;
  const slices = holdings
    .filter((h) => h.valueUSD != null && h.valueUSD > 0)
    .map((h) => ({
      label: shortAsset(h.asset),
      value: h.valueUSD as number,
      href: `/assets/${encodeURIComponent(assetSlug(h.asset))}`,
    }));

  if (holdings.length === 0) {
    return (
      <Panel title="Positions" bodyClassName="text-sm text-ink-muted">
        No non-zero balances in the captured ledger-entry window.
      </Panel>
    );
  }

  return (
    <Panel
      title="Positions"
      hint="Native XLM + trustline balances, valued at the live VWAP. Holdings we can't price are listed without a USD value."
      source={asExample(`/v1/accounts/${id}`)}
      bodyClassName="space-y-4"
    >
      <StatGrid cols={3}>
        <StatCell>
          <Stat label="Portfolio value" value={total > 0 ? usdFmt.format(total) : '—'} />
        </StatCell>
        <StatCell>
          <Stat label="Holdings" value={holdings.length.toLocaleString()} sub={`${pricedCount} priced`} />
        </StatCell>
        <StatCell>
          <Stat
            label="Top holding"
            value={slices[0] ? slices[0].label : '—'}
            sub={slices[0] && total > 0 ? `${((slices[0].value / total) * 100).toFixed(1)}% of value` : undefined}
          />
        </StatCell>
      </StatGrid>

      {slices.length > 1 && total > 0 && (
        <DonutChart
          data={slices}
          centerLabel={usdFmt.format(total).replace(/\.00$/, '')}
          centerSub="value"
          formatValue={(n) => usdFmt.format(n)}
        />
      )}

      <TableWrap>
        <Table>
          <THead>
            <TR className="hover:bg-transparent">
              <Th>Asset</Th>
              <Th align="right">Balance</Th>
              <Th align="right">Price</Th>
              <Th align="right">Value</Th>
              <Th align="right">Allocation</Th>
            </TR>
          </THead>
          <TBody>
            {holdings.map((h) => (
              <TR key={h.asset}>
                <Td>
                  <AssetLink canonical={h.asset} />
                </Td>
                <Td align="right" className="font-mono text-ink-body">
                  {formatCompact(h.amount)}
                </Td>
                <Td align="right" className="font-mono">
                  {h.priceUSD != null ? `$${formatPrice(h.priceUSD)}` : '—'}
                </Td>
                <Td align="right" className="font-mono">
                  {h.valueUSD != null ? usdFmt.format(h.valueUSD) : '—'}
                </Td>
                <Td align="right" className="font-mono text-ink-muted">
                  {h.valueUSD != null && total > 0 ? `${((h.valueUSD / total) * 100).toFixed(1)}%` : '—'}
                </Td>
              </TR>
            ))}
          </TBody>
        </Table>
      </TableWrap>
    </Panel>
  );
}

function formatPrice(n: number): string {
  if (!Number.isFinite(n)) return '—';
  if (n >= 1) return n.toFixed(n >= 100 ? 2 : 4);
  if (n >= 0.0001) return n.toFixed(6);
  return n.toExponential(2);
}

function shortAsset(canonical: string): string {
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.slice(5);
  if (canonical.startsWith('crypto:')) return canonical.slice(7);
  const i = canonical.indexOf('-');
  return i === -1 ? canonical : canonical.slice(0, i);
}

// assetSlug — the static-export-safe short slug for the asset link
// (long-form asset_ids 404). native → native; otherwise the code.
function assetSlug(canonical: string): string {
  if (canonical === 'native') return 'native';
  return shortAsset(canonical);
}
