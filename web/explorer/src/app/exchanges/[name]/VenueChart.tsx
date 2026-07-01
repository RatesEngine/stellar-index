'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';

import { apiGet, asExample } from '@/api/client';
import { Panel } from '@/components/reveal';
import { MarketChart } from '@/components/charts/MarketChart';

interface Market {
  base: string;
  quote: string;
  volume_24h_usd?: string | null;
}

/**
 * VenueChart — the price chart for a CEX venue. Fetches the venue's
 * pair list (by 24h volume), default-selects the top pair, and renders
 * the shared MarketChart (real OHLC + volume from /v1/ohlc). A dropdown
 * switches between the venue's pairs.
 */
export function VenueChart({ venue }: { venue: string }) {
  const pairsQuery = useQuery<Market[], Error>({
    queryKey: ['/v1/markets', venue, 'venuechart'],
    queryFn: async () => {
      const env = await apiGet<{ data: Market[] }>('/v1/markets', {
        source: venue,
        order_by: 'volume_24h_usd_desc',
        limit: 200,
      });
      return env.data ?? [];
    },
  });

  const pairs = pairsQuery.data ?? [];
  const pairsLoading = pairsQuery.isLoading;
  const pairsError = pairsQuery.error ? pairsQuery.error.message : null;

  const [selected, setSelected] = useState<{ base: string; quote: string } | null>(null);
  // Default-select the venue's top pair once its list resolves, and
  // re-default if the venue prop changes. Adjust state during render
  // (tracking the venue we've defaulted against) rather than in an
  // effect — mirrors the previous `setSelected(list[0])` on load.
  const [defaultedFor, setDefaultedFor] = useState<string | null>(null);
  if (!pairsLoading && defaultedFor !== venue) {
    setDefaultedFor(venue);
    setSelected(pairs[0] ? { base: pairs[0].base, quote: pairs[0].quote } : null);
  }

  if (pairsLoading) {
    return (
      <Panel title="Live chart" hint="Loading pairs…" source={asExample('/v1/markets', { source: venue })}>
        <div className="h-[380px]" />
      </Panel>
    );
  }
  if (pairsError) {
    return (
      <Panel title="Live chart" hint="Pair list unavailable" source={asExample('/v1/markets', { source: venue })}>
        <div className="flex h-[380px] items-center justify-center px-4 text-center text-sm text-ink-muted">
          Couldn&apos;t load pairs for this venue ({pairsError}).
        </div>
      </Panel>
    );
  }
  if (pairs.length === 0) {
    return (
      <Panel title="Live chart" hint="No pairs reporting" source={asExample('/v1/markets', { source: venue })}>
        <div className="flex h-[380px] items-center justify-center text-sm text-ink-muted">
          No pairs reporting in the last 14 days.
        </div>
      </Panel>
    );
  }

  const baseLabel = selected ? labelOf(selected.base) : '';
  const quoteLabel = selected ? labelOf(selected.quote) : '';

  return (
    <Panel
      title="Live chart"
      hint="OHLC + volume"
      source={
        selected
          ? asExample('/v1/ohlc', {
              base: selected.base,
              quote: selected.quote,
              interval: '1h',
              limit: 168,
            })
          : asExample('/v1/markets', { source: venue })
      }
      bodyClassName="space-y-3"
    >
      <PairPicker pairs={pairs} value={selected} onChange={(p) => setSelected(p)} />
      {selected && (
        <MarketChart
          base={selected.base}
          quote={selected.quote}
          baseLabel={baseLabel}
          quoteLabel={quoteLabel}
        />
      )}
    </Panel>
  );
}

// labelOf strips the canonical-form prefix so dropdown + header text
// reads as a plain ticker (e.g. "XLM / USDT" not "crypto:XLM").
function labelOf(canonical: string): string {
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.slice(5);
  if (canonical.startsWith('crypto:')) return canonical.slice(7);
  const dashIx = canonical.indexOf('-');
  if (dashIx !== -1) return canonical.slice(0, dashIx);
  return canonical.length > 12 ? `${canonical.slice(0, 6)}…` : canonical;
}

function PairPicker({
  pairs,
  value,
  onChange,
}: {
  pairs: Market[];
  value: { base: string; quote: string } | null;
  onChange: (p: { base: string; quote: string }) => void;
}) {
  return (
    <label className="inline-flex items-center gap-1.5 rounded-md border border-line bg-surface px-2 py-1">
      <span className="text-[10px] font-medium uppercase tracking-wider text-ink-muted">Pair</span>
      <select
        value={value ? `${value.base}|${value.quote}` : ''}
        onChange={(e) => {
          const [base, quote] = e.target.value.split('|');
          onChange({ base, quote });
        }}
        className="bg-transparent text-xs font-mono uppercase tracking-wider text-ink-body focus:outline-hidden"
      >
        {pairs.map((p) => (
          <option key={`${p.base}|${p.quote}`} value={`${p.base}|${p.quote}`}>
            {labelOf(p.base)}/{labelOf(p.quote)}
          </option>
        ))}
      </select>
    </label>
  );
}
