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
  { type: 'page', label: 'Assets', href: '/assets' },
  { type: 'page', label: 'Markets', href: '/markets' },
  { type: 'page', label: 'Issuers', href: '/issuers' },
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
  { type: 'page', label: 'API docs', href: 'https://docs.ratesengine.net' },
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
 * Empty query: shows top-5 coins (by observation count) +
 * protocols/pages.
 *
 * Non-empty query: hits `/v1/coins?q=…` server-side (debounced
 * 200ms) so any of the ~440K classic assets matches, not just
 * the top-100 default page. Falls back to client-side filter
 * across protocols + static pages.
 */
export function SearchModal() {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  // Debounced query for the server-side /v1/coins?q=… call so a
  // burst of keystrokes doesn't fan out a request per character.
  const [debouncedQ, setDebouncedQ] = useState('');

  const topCoins = useCoins(100);
  // Server-side search — only fires when the user has typed at
  // least 2 chars; below that the top-100 list covers it.
  const searchedCoins = useCoins(
    25,
    undefined,
    undefined,
    debouncedQ.length >= 2 ? debouncedQ : undefined,
  );

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
    if (open) {
      setQ('');
      setDebouncedQ('');
    }
  }, [open]);

  // Debounce the live input into debouncedQ — 200ms balances
  // "feels live" with "doesn't fan out a request per keystroke".
  useEffect(() => {
    const t = setTimeout(() => setDebouncedQ(q.trim()), 200);
    return () => clearTimeout(t);
  }, [q]);

  const results = useMemo(() => {
    const isServerSearched = debouncedQ.length >= 2;
    const sourceCoins = isServerSearched
      ? (searchedCoins.data?.coins ?? [])
      : (topCoins.data?.coins ?? []);
    return search(q, sourceCoins, isServerSearched);
  }, [q, debouncedQ, searchedCoins.data, topCoins.data]);

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
                  No matches across the asset directory, protocols,
                  or pages.
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

function search(
  q: string,
  coins: Coin[],
  isServerSearched: boolean,
): Result[] {
  const norm = q.trim().toLowerCase();
  const coinResults = coins.map(coinResult);
  if (!norm) {
    // Empty query → top 5 coins as a starter list (already sorted
    // by observation_count desc by the API).
    return coinResults.slice(0, 5);
  }

  // Direct-jump detectors run first — when the user has typed a
  // recognisable identifier shape, surface the deep-link as the
  // top result so they can hit Enter and go.
  const direct: Result[] = [];

  // Stellar G-strkey — 56 chars, uppercase base32 starting with 'G'.
  const gMatch = q.trim().match(/^G[A-Z2-7]{55}$/);
  if (gMatch) {
    direct.push({
      type: 'page',
      label: `Issuer ${gMatch[0].slice(0, 8)}…${gMatch[0].slice(-4)}`,
      hint: 'open issuer detail',
      href: `/issuers/${gMatch[0]}`,
    });
  }

  // Pair shortcut: "XLM/USDC", "XLM USDC", "xlm-usdc" → enumerate
  // possible (base, quote) pairs from the loaded coins set and pick
  // the first one. Falls back to /markets if no exact pair match.
  const pairMatch = q.trim().match(/^([A-Za-z0-9]+)[\s/\\-]+([A-Za-z0-9]+)$/);
  if (pairMatch) {
    const baseCode = pairMatch[1].toUpperCase();
    const quoteCode = pairMatch[2].toUpperCase();
    const baseAssetID = lookupAssetID(coins, baseCode);
    const quoteAssetID = lookupAssetID(coins, quoteCode);
    if (baseAssetID && quoteAssetID) {
      direct.push({
        type: 'pair',
        label: `${baseCode} / ${quoteCode}`,
        hint: 'open pair detail',
        href: `/markets/${encodeURIComponent(`${baseAssetID}~${quoteAssetID}`)}`,
      });
    }
  }

  // When coins came from /v1/coins?q=…, they're already filtered;
  // skip the redundant client pass on them. Protocols + pages
  // still need a client filter (they're seeded constants).
  const matchedCoins = isServerSearched
    ? coinResults
    : coinResults.filter((r) => match(norm, r));
  const matchedOther = [...PROTOCOLS, ...STATIC_PAGES].filter((r) =>
    match(norm, r),
  );
  return [...direct, ...matchedCoins, ...matchedOther].slice(0, 12);
}

// lookupAssetID resolves a code (e.g. "USDC") to its canonical
// asset_id (e.g. "USDC-GA5Z…") using the loaded coins set. "XLM"
// special-cases to "native". Returns null when no match — caller
// falls back to /markets.
function lookupAssetID(coins: Coin[], code: string): string | null {
  if (code === 'XLM') return 'native';
  for (const c of coins) {
    if (c.code === code) return c.asset_id;
  }
  return null;
}

function coinResult(c: Coin): Result {
  return {
    type: 'coin',
    label: c.code,
    hint: c.slug,
    href: `/assets/${c.slug}`,
  };
}

function match(q: string, r: Result): boolean {
  const hay = `${r.label} ${r.hint ?? ''} ${r.href}`.toLowerCase();
  // Allow each query token to match anywhere in the haystack.
  return q.split(/\s+/).every((t) => hay.includes(t));
}
