'use client';

import { Search, X } from 'lucide-react';
import Link from 'next/link';
import { useEffect, useMemo, useState } from 'react';

import { useCoins, type Coin } from '@/api/hooks';

type Result = {
  type: 'coin' | 'pair' | 'protocol' | 'oracle' | 'page';
  label: string;
  hint?: string;
  href: string;
};

const STATIC_PAGES: Result[] = [
  { type: 'page', label: 'Home', href: '/' },
  { type: 'page', label: 'Coins', href: '/coins' },
  { type: 'page', label: 'Markets', href: '/markets' },
  { type: 'page', label: 'DEXes', href: '/dexes' },
  { type: 'page', label: 'Lending', href: '/lending' },
  { type: 'page', label: 'Aggregators', href: '/aggregators' },
  { type: 'page', label: 'Oracles', href: '/oracles' },
  { type: 'page', label: 'Sources', href: '/sources' },
  { type: 'page', label: 'Network', href: '/network' },
  { type: 'page', label: 'Research', href: '/research' },
  { type: 'page', label: 'Diagnostics', href: '/diagnostics' },
  { type: 'page', label: 'Anomalies', href: '/anomalies' },
  { type: 'page', label: 'Divergences', href: '/divergences' },
  { type: 'page', label: 'MEV', href: '/mev' },
  { type: 'page', label: 'API docs', href: '/docs' },
];

const PROTOCOLS: Result[] = [
  { type: 'protocol', label: 'Soroswap', hint: 'AMM + router', href: '/dexes' },
  { type: 'protocol', label: 'Phoenix', hint: 'AMM', href: '/dexes' },
  { type: 'protocol', label: 'Aquarius', hint: 'AMM with gauges', href: '/dexes' },
  { type: 'protocol', label: 'SDEX', hint: 'native order book', href: '/dexes' },
  { type: 'protocol', label: 'Blend', hint: 'lending', href: '/lending' },
  { type: 'protocol', label: 'DeFindex', hint: 'yield aggregator', href: '/aggregators' },
  { type: 'oracle', label: 'Reflector DEX', href: '/oracles' },
  { type: 'oracle', label: 'Reflector CEX', href: '/oracles' },
  { type: 'oracle', label: 'Reflector FX', href: '/oracles' },
  { type: 'oracle', label: 'Redstone', href: '/oracles' },
  { type: 'oracle', label: 'Band', href: '/oracles' },
];

/**
 * Cmd-K search modal. Mounts globally via the Navbar; opens on
 * Cmd-K / Ctrl-K and on the Navbar's search-icon button.
 *
 * Coins come from the live `/v1/coins` endpoint (top-500 by
 * observation count); protocols + static pages stay seeded since
 * neither is in the API yet. Replaced by `/v1/search?q=...` once
 * that endpoint ships per data-inventory §10.13.
 */
export function SearchModal() {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  // Reuses the same cache key as `/coins` (limit=100) so navigating
  // there afterwards is instant — no separate fetch for search.
  const { data: coins } = useCoins(100);

  // Cmd-K / Ctrl-K toggles.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        setOpen((v) => !v);
      }
      if (e.key === 'Escape') setOpen(false);
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // Reset query each time the modal opens — the search cursor
  // should always start fresh, and stale state on re-open is
  // surprising.
  useEffect(() => {
    if (open) setQ('');
  }, [open]);

  const results = useMemo(() => search(q, coins ?? []), [q, coins]);

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="hidden items-center gap-2 rounded-md border border-slate-200 bg-white px-2.5 py-1.5 text-xs text-slate-500 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-400 sm:inline-flex"
        aria-label="Open search"
      >
        <Search className="h-3.5 w-3.5" />
        Search
        <kbd className="ml-2 rounded border border-slate-200 bg-slate-50 px-1 text-[10px] font-medium dark:border-slate-700 dark:bg-slate-800">
          ⌘K
        </kbd>
      </button>
      {open && (
        <div
          className="fixed inset-0 z-50 flex items-start justify-center bg-slate-900/50 p-4 pt-24"
          onClick={() => setOpen(false)}
        >
          <div
            className="w-full max-w-xl overflow-hidden rounded-lg bg-white shadow-2xl dark:bg-slate-900"
            onClick={(e) => e.stopPropagation()}
            role="dialog"
            aria-modal
          >
            <div className="flex items-center gap-2 border-b border-slate-200 px-3 py-3 dark:border-slate-800">
              <Search className="h-4 w-4 text-slate-400" />
              <input
                autoFocus
                className="flex-1 bg-transparent text-sm outline-none placeholder:text-slate-400"
                placeholder="Coins, pairs, protocols, accounts, transactions…"
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
              <button
                type="button"
                onClick={() => setOpen(false)}
                className="text-slate-400 hover:text-slate-700 dark:hover:text-slate-300"
                aria-label="Close"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <ul className="max-h-96 overflow-y-auto p-2 text-sm">
              {results.length === 0 && (
                <li className="px-3 py-2 text-xs text-slate-500">
                  No matches. Search ranks against the live coin
                  directory plus seeded protocols/pages; the unified{' '}
                  <code className="font-mono">/v1/search</code> endpoint
                  lands in Phase 6.6.
                </li>
              )}
              {results.map((r) => (
                <li key={`${r.type}:${r.label}:${r.href}`}>
                  <Link
                    href={r.href}
                    onClick={() => setOpen(false)}
                    className="flex items-center justify-between rounded-md px-3 py-2 hover:bg-slate-50 dark:hover:bg-slate-800"
                  >
                    <span className="flex items-center gap-2">
                      <span className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-slate-500 dark:bg-slate-800">
                        {r.type}
                      </span>
                      <span className="font-medium">{r.label}</span>
                      {r.hint && (
                        <span className="text-xs text-slate-500">— {r.hint}</span>
                      )}
                    </span>
                    <span className="text-xs font-mono text-slate-400">
                      {r.href}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
            <div className="border-t border-slate-100 px-3 py-1.5 text-[10px] text-slate-400 dark:border-slate-800">
              <kbd>↑↓</kbd> navigate · <kbd>↵</kbd> open · <kbd>esc</kbd> close
            </div>
          </div>
        </div>
      )}
    </>
  );
}

function search(q: string, coins: Coin[]): Result[] {
  const norm = q.trim().toLowerCase();
  const coinResults = coins.map(coinResult);
  if (!norm) {
    // Empty query → top 5 coins as a starter list (already sorted
    // by observation_count desc by the API).
    return coinResults.slice(0, 5);
  }
  const all: Result[] = [...coinResults, ...PROTOCOLS, ...STATIC_PAGES];
  return all.filter((r) => match(norm, r)).slice(0, 12);
}

function coinResult(c: Coin): Result {
  return {
    type: 'coin',
    label: c.code,
    hint: c.slug,
    href: `/coins/${c.slug}`,
  };
}

function match(q: string, r: Result): boolean {
  const hay = `${r.label} ${r.hint ?? ''} ${r.href}`.toLowerCase();
  // Allow each query token to match anywhere in the haystack.
  return q.split(/\s+/).every((t) => hay.includes(t));
}
