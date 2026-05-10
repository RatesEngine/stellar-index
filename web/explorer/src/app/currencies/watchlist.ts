'use client';

// Watchlist — localStorage-backed set of starred currency keys.
// One unified storage key for both crypto + fiat; entries are
// `{kind}:{ticker}` strings (e.g. `crypto:USDC`, `fiat:EUR`) so
// the same ticker name in both kinds can be starred independently
// (rare but happens with stablecoins like USDC that have a fiat
// USD counterpart).
//
// Pure browser-side; never sent to the API. Future enhancement
// could sync via the user's authenticated account.

import { useEffect, useState } from 'react';

const STORAGE_KEY = 'ratesengine.watchlist';

function entryKey(kind: 'crypto' | 'fiat', ticker: string): string {
  return `${kind}:${ticker.toUpperCase()}`;
}

function readSet(): Set<string> {
  if (typeof window === 'undefined') return new Set();
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return new Set();
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return new Set();
    return new Set(parsed.filter((s) => typeof s === 'string'));
  } catch {
    return new Set();
  }
}

function writeSet(s: Set<string>) {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(Array.from(s)));
  } catch {
    // Quota exceeded / disabled storage — degrade to in-memory only.
  }
}

/**
 * useWatchlist — hook that exposes the current watchlist + toggle
 * action. SSR-safe (returns empty set on first render, hydrates
 * from localStorage on mount).
 *
 * Multiple instances of the hook stay in sync via a custom event
 * fired on every write — without this the listing's filter chip
 * count and the per-row star can drift after a toggle.
 */
export function useWatchlist(): {
  has: (kind: 'crypto' | 'fiat', ticker: string) => boolean;
  toggle: (kind: 'crypto' | 'fiat', ticker: string) => void;
  size: number;
} {
  const [set, setSet] = useState<Set<string>>(() => new Set());

  useEffect(() => {
    setSet(readSet());
    function onChange() {
      setSet(readSet());
    }
    window.addEventListener('ratesengine:watchlist-change', onChange);
    window.addEventListener('storage', onChange);
    return () => {
      window.removeEventListener('ratesengine:watchlist-change', onChange);
      window.removeEventListener('storage', onChange);
    };
  }, []);

  function has(kind: 'crypto' | 'fiat', ticker: string): boolean {
    return set.has(entryKey(kind, ticker));
  }

  function toggle(kind: 'crypto' | 'fiat', ticker: string) {
    const next = new Set(set);
    const k = entryKey(kind, ticker);
    if (next.has(k)) {
      next.delete(k);
    } else {
      next.add(k);
    }
    writeSet(next);
    setSet(next);
    window.dispatchEvent(new CustomEvent('ratesengine:watchlist-change'));
  }

  return { has, toggle, size: set.size };
}
