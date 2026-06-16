'use client';

import { useMemo, useState } from 'react';
import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';
import { formatCompact } from '@/lib/format';
import { categoryTone, protocolMeta, PROTOCOLS } from './registry';

// Mirrors internal/api/v1/protocols.go ProtocolView.
interface ProtocolCard {
  name: string;
  category: string;
  description: string;
  genesis_ledger: number;
  factories: string[];
  contract_count: number;
  events_24h: number;
  completeness?: { complete: boolean; watermark_ledger: number };
}

/**
 * ProtocolsIndex — the protocol directory: a grid of cards, one per
 * indexed protocol, fetched from /v1/protocols. Each card links into the
 * full /protocols/{name} analytics page. A category filter row scopes
 * the grid. Falls back to the static registry (always-rendered cards,
 * zeroed stats) when the directory endpoint is unreachable, so the
 * pillar never renders empty.
 */
export function ProtocolsIndex() {
  const [filter, setFilter] = useState<string>('');

  const { data, isError } = useQuery<ProtocolCard[]>({
    queryKey: ['/v1/protocols'],
    retry: false,
    staleTime: 60_000,
    queryFn: async () => {
      const env = await apiGet<{ data: { protocols: ProtocolCard[] } }>(
        '/v1/protocols',
      );
      return env.data?.protocols ?? [];
    },
  });

  // Fall back to the static registry so the grid renders even if the API
  // is down (stats degrade to zero, the cards + links still work).
  const cards: ProtocolCard[] = useMemo(() => {
    if (data && data.length > 0) return data;
    return PROTOCOLS.map((p) => ({
      name: p.name,
      category: '',
      description: p.description,
      genesis_ledger: 0,
      factories: [],
      contract_count: 0,
      events_24h: 0,
    }));
  }, [data]);

  const categories = useMemo(() => {
    const set = new Set<string>();
    for (const c of cards) if (c.category) set.add(c.category);
    return Array.from(set).sort();
  }, [cards]);

  const visible = useMemo(
    () => (filter ? cards.filter((c) => c.category === filter) : cards),
    [cards, filter],
  );

  const totalEvents24h = cards.reduce((s, c) => s + (c.events_24h ?? 0), 0);
  const verifiedCount = cards.filter((c) => c.completeness?.complete).length;

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Protocols</h1>
        <p className="max-w-3xl text-sm text-ink-body">
          Every major Stellar protocol we index — DEXes, AMMs, lending, yield
          vaults, bridges and oracles. Each protocol page carries its full
          contract roster, the distribution of every event type it emits, and a
          verified-completeness verdict against the certified ledger lake. Click
          a card to drill in.
        </p>
        <div className="flex flex-wrap gap-x-6 gap-y-1 pt-1 text-xs text-ink-muted">
          <span>
            <span className="font-mono tabular-nums text-ink-body">
              {cards.length}
            </span>{' '}
            protocols
          </span>
          <span>
            <span className="font-mono tabular-nums text-ink-body">
              {verifiedCount}
            </span>{' '}
            verified complete
          </span>
          <span>
            <span className="font-mono tabular-nums text-ink-body">
              {formatCompact(totalEvents24h)}
            </span>{' '}
            events · last 24h
          </span>
        </div>
      </header>

      {isError && (
        <Panel
          title="Live stats unavailable"
          bodyClassName="text-sm text-ink-body"
        >
          The protocol directory endpoint is unreachable, so the cards below show
          the static registry without live counts. The per-protocol pages still
          work.
        </Panel>
      )}

      {categories.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="text-ink-muted">Category:</span>
          <FilterChip active={filter === ''} onClick={() => setFilter('')} label="All" />
          {categories.map((cat) => (
            <FilterChip
              key={cat}
              active={filter === cat}
              onClick={() => setFilter(cat)}
              label={cat}
            />
          ))}
        </div>
      )}

      <div
        className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3"
        data-source={asExample('/v1/protocols').url}
      >
        {visible.map((c) => (
          <ProtocolCardView key={c.name} card={c} />
        ))}
      </div>
    </div>
  );
}

function ProtocolCardView({ card }: { card: ProtocolCard }) {
  const label = protocolMeta(card.name)?.label ?? card.name;
  return (
    <Link
      href={`/protocols/${encodeURIComponent(card.name)}`}
      className="group flex flex-col rounded-lg border border-line bg-surface p-4 transition hover:border-brand-500 hover:shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500"
    >
      <div className="flex items-start justify-between gap-2">
        <h2 className="text-base font-semibold tracking-tight group-hover:text-brand-600">
          {label}
        </h2>
        {card.category && (
          <span
            className={`shrink-0 rounded px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-wider ${categoryTone(card.category)}`}
          >
            {card.category}
          </span>
        )}
      </div>
      <p className="mt-1.5 line-clamp-2 grow text-xs text-ink-muted">
        {card.description}
      </p>
      <div className="mt-3 flex items-end justify-between">
        <dl className="flex gap-5 text-xs">
          <div>
            <dt className="text-[9px] uppercase tracking-wider text-ink-faint">
              Contracts
            </dt>
            <dd className="font-mono tabular-nums text-ink-body">
              {formatCompact(card.contract_count)}
            </dd>
          </div>
          <div>
            <dt className="text-[9px] uppercase tracking-wider text-ink-faint">
              Events · 24h
            </dt>
            <dd className="font-mono tabular-nums text-ink-body">
              {formatCompact(card.events_24h)}
            </dd>
          </div>
        </dl>
        <CardBadge completeness={card.completeness} />
      </div>
    </Link>
  );
}

function CardBadge({
  completeness,
}: {
  completeness?: { complete: boolean };
}) {
  if (!completeness) {
    return (
      <span className="rounded bg-surface-subtle px-1.5 py-0.5 text-[9px] uppercase tracking-wider text-ink-faint">
        unknown
      </span>
    );
  }
  return completeness.complete ? (
    <span className="rounded bg-up-subtle px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wider text-up">
      ✓ complete
    </span>
  ) : (
    <span className="rounded bg-warn-50 px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wider text-warn-700">
      partial
    </span>
  );
}

function FilterChip({
  active,
  onClick,
  label,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-full px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 ${
        active
          ? 'bg-brand-600 text-white'
          : 'bg-surface-subtle text-ink-body hover:bg-line'
      }`}
    >
      {label}
    </button>
  );
}
