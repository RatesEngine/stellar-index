'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useMemo } from 'react';

import { Panel } from '@/components/reveal';
import { DirectionPill, RankBadge, Sparkline } from '@/components/primitives';
import { asExample } from '@/api/client';
import { formatCompact, formatPrice } from '@/lib/format';

type SeedRow = {
  rank: number;
  rankDelta: number;
  slug: string;
  ticker: string;
  name: string;
  price: number;
  h1: number;
  h24: number;
  d7: number;
  volume24h: number;
  marketCap: number;
  spark: number[];
};

const SAMPLE: SeedRow[] = [
  {
    rank: 1, rankDelta: 0,
    slug: 'stellar', ticker: 'XLM', name: 'Stellar',
    price: 0.1234, h1: 0.5, h24: 3.2, d7: -1.1,
    volume24h: 12_400_000, marketCap: 3_400_000_000,
    spark: [0.115, 0.118, 0.12, 0.119, 0.122, 0.121, 0.1234],
  },
  {
    rank: 2, rankDelta: 1,
    slug: 'aqua', ticker: 'AQUA', name: 'Aquarius',
    price: 0.0042, h1: 1.2, h24: 8.1, d7: 14.5,
    volume24h: 2_100_000, marketCap: 421_000_000,
    spark: [0.0036, 0.0038, 0.0039, 0.0041, 0.0040, 0.0042, 0.0042],
  },
  {
    rank: 3, rankDelta: -1,
    slug: 'usdc', ticker: 'USDC', name: 'USD Coin (Centre)',
    price: 1.0001, h1: 0.0, h24: 0.02, d7: -0.01,
    volume24h: 8_700_000, marketCap: 50_000_000,
    spark: [0.9999, 1.0, 1.0001, 1.0, 0.9998, 1.0001, 1.0001],
  },
  {
    rank: 4, rankDelta: 0,
    slug: 'blnd', ticker: 'BLND', name: 'Blend',
    price: 0.0823, h1: -0.4, h24: -2.1, d7: -7.8,
    volume24h: 312_000, marketCap: 41_200_000,
    spark: [0.092, 0.090, 0.087, 0.085, 0.083, 0.084, 0.0823],
  },
  {
    rank: 5, rankDelta: 2,
    slug: 'eurc', ticker: 'EURC', name: 'Euro Coin',
    price: 1.0805, h1: 0.0, h24: 0.1, d7: 0.4,
    volume24h: 521_000, marketCap: 12_800_000,
    spark: [1.076, 1.077, 1.078, 1.079, 1.080, 1.081, 1.0805],
  },
  {
    rank: 6, rankDelta: -1,
    slug: 'yxlm', ticker: 'yXLM', name: 'yXLM (yieldswap)',
    price: 1.044, h1: 0.0, h24: 0.5, d7: 1.8,
    volume24h: 92_000, marketCap: 8_300_000,
    spark: [1.024, 1.029, 1.033, 1.038, 1.041, 1.042, 1.044],
  },
  {
    rank: 7, rankDelta: 0,
    slug: 'mxne', ticker: 'MXNe', name: 'Mexican Peso (Bitso)',
    price: 0.0589, h1: 0.0, h24: -0.1, d7: 0.2,
    volume24h: 45_000, marketCap: 2_100_000,
    spark: [0.0588, 0.0590, 0.0589, 0.0591, 0.0588, 0.0589, 0.0589],
  },
];

/**
 * Client-side sortable seed table. Reads `?sort=col:dir` from the
 * URL via `useSearchParams`. Replace SAMPLE with a TanStack Query
 * hook against `/v1/coins` once that endpoint ships; the surrounding
 * shape stays the same.
 */
export function CoinsTable() {
  const params = useSearchParams();
  const sortParam = params.get('sort') ?? 'volume24h:desc';
  const rows = useMemo(() => sortRows(SAMPLE, sortParam), [sortParam]);

  return (
    <Panel
      source={asExample('/v1/coins', { sort: sortParam, limit: 100 })}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
              <Th>#</Th>
              <Th>Asset</Th>
              <Th align="right">Price</Th>
              <Th align="right">1h</Th>
              <Th align="right">24h</Th>
              <Th align="right">7d</Th>
              <Th align="right">Volume 24h</Th>
              <Th align="right">Market cap</Th>
              <Th align="right">Sparkline 7d</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {rows.map((row, i) => (
              <tr key={row.slug} className="hover:bg-slate-50 dark:hover:bg-slate-900/40">
                <Td>
                  <div className="flex items-center gap-1">
                    <span className="text-slate-400">{i + 1}</span>
                    <RankBadge delta={row.rankDelta} />
                  </div>
                </Td>
                <Td>
                  <Link
                    href={`/coins/${row.slug}`}
                    className="flex items-baseline gap-2 hover:text-brand-600"
                  >
                    <span className="font-medium">{row.ticker}</span>
                    <span className="text-xs text-slate-500">{row.name}</span>
                  </Link>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums">{formatPrice(row.price)}</span>
                </Td>
                <Td align="right"><DirectionPill deltaPct={row.h1} compact /></Td>
                <Td align="right"><DirectionPill deltaPct={row.h24} compact /></Td>
                <Td align="right"><DirectionPill deltaPct={row.d7} compact /></Td>
                <Td align="right">
                  <span className="font-mono tabular-nums">${formatCompact(row.volume24h)}</span>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums">${formatCompact(row.marketCap)}</span>
                </Td>
                <Td align="right"><Sparkline values={row.spark} /></Td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

function Th({
  children,
  align,
}: {
  children: React.ReactNode;
  align?: 'left' | 'right';
}) {
  return (
    <th className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`} scope="col">
      {children}
    </th>
  );
}

function Td({
  children,
  align,
}: {
  children: React.ReactNode;
  align?: 'left' | 'right';
}) {
  return (
    <td className={`px-4 py-3 ${align === 'right' ? 'text-right' : 'text-left'}`}>
      {children}
    </td>
  );
}

function sortRows(rows: SeedRow[], sortParam: string): SeedRow[] {
  const [col, dir] = sortParam.split(':');
  const desc = dir === 'desc';
  const sorted = [...rows].sort((a, b) => {
    const av = (a as unknown as Record<string, number>)[col!] ?? 0;
    const bv = (b as unknown as Record<string, number>)[col!] ?? 0;
    return desc ? bv - av : av - bv;
  });
  return sorted;
}
