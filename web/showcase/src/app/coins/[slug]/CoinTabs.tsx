'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';

export type CoinTab =
  | 'overview'
  | 'chart'
  | 'markets'
  | 'history'
  | 'supply'
  | 'issuer'
  | 'liquidity';

/**
 * Client tab strip for /coins/[slug]. Reads `?tab=` from URL state;
 * the parent server component renders both overview + chart bodies
 * and toggles visibility based on the active tab.
 *
 * Disabled tabs render as cursor-not-allowed labels until their
 * content lands in subsequent PRs.
 */
export function CoinTabs({ slug, hasIssuer }: { slug: string; hasIssuer: boolean }) {
  const params = useSearchParams();
  const active = (params.get('tab') as CoinTab) || 'overview';

  type T = { key: CoinTab; label: string; disabled?: boolean };
  const tabs: T[] = [
    { key: 'overview', label: 'Overview' },
    { key: 'chart', label: 'Chart' },
    { key: 'markets', label: 'Markets', disabled: true },
    { key: 'history', label: 'History', disabled: true },
    { key: 'supply', label: 'Supply', disabled: true },
    ...(hasIssuer
      ? ([{ key: 'issuer', label: 'Issuer', disabled: true }] as const)
      : []),
    { key: 'liquidity', label: 'Liquidity', disabled: true },
  ];

  return (
    <nav className="flex gap-1 overflow-x-auto border-b border-slate-200 text-sm dark:border-slate-800">
      {tabs.map((t) =>
        t.disabled ? (
          <span
            key={t.key}
            className="cursor-not-allowed border-b-2 border-transparent px-3 py-2 text-slate-400 dark:text-slate-600"
            title="Coming soon"
          >
            {t.label}
          </span>
        ) : (
          <Link
            key={t.key}
            href={
              t.key === 'overview' ? `/coins/${slug}` : `/coins/${slug}?tab=${t.key}`
            }
            className={`border-b-2 px-3 py-2 ${
              t.key === active
                ? 'border-brand-500 font-medium text-brand-600 dark:text-brand-400'
                : 'border-transparent text-slate-600 hover:text-brand-600 dark:text-slate-300'
            }`}
          >
            {t.label}
          </Link>
        ),
      )}
    </nav>
  );
}

export function ActiveTabSlot({
  overview,
  chart,
}: {
  overview: React.ReactNode;
  chart: React.ReactNode;
}) {
  return <ActiveTabClient overview={overview} chart={chart} />;
}

function ActiveTabClient({
  overview,
  chart,
}: {
  overview: React.ReactNode;
  chart: React.ReactNode;
}) {
  // Tiny inline component — single-purpose, exists to read
  // useSearchParams without adding more files. Returns the right
  // ReactNode given the active tab.
  return <ActiveBody overview={overview} chart={chart} />;
}

function ActiveBody({
  overview,
  chart,
}: {
  overview: React.ReactNode;
  chart: React.ReactNode;
}) {
  const params = useSearchParams();
  const tab = (params.get('tab') as CoinTab) || 'overview';
  return tab === 'chart' ? <>{chart}</> : <>{overview}</>;
}
