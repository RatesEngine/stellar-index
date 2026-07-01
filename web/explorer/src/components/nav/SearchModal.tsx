'use client';

import { useQuery } from '@tanstack/react-query';
import { Search, X } from 'lucide-react';
import Link from 'next/link';
import { useEffect, useMemo, useRef, useState } from 'react';

import { apiGet } from '@/api/client';
import { useCoins, useVerifiedSlugs, type Coin } from '@/api/hooks';
import { assetHrefFor } from '@/lib/fiat-slugs';

type Result = {
  type:
    | 'coin'
    | 'pair'
    | 'protocol'
    | 'oracle'
    | 'page'
    | 'currency'
    // Explorer entity classifications (ADR-0038 Phase D) — surfaced
    // additively alongside the existing asset/protocol/page results
    // via the /v1/search classification endpoint.
    | 'transaction'
    | 'ledger'
    | 'account'
    | 'contract'
    | 'asset';
  label: string;
  hint?: string;
  href: string;
  // Verified-currency flag (R-018 Phase 1.5). When true, the row
  // renders a small green-check badge next to the label.
  verified?: boolean;
};

// Explorer-entity classification from GET /v1/search?q=. Distinct
// from the existing client-side asset/protocol/page index — this is
// the backend's authoritative "what kind of thing is this string"
// answer for unbounded entities (tx hashes, ledger seqs, accounts,
// contracts) that the client index can't enumerate.
type SearchClassification = {
  query: string;
  kind: 'transaction' | 'ledger' | 'account' | 'contract' | 'asset' | 'unknown';
  canonical?: string;
  href?: string;
  supported?: boolean;
  note?: string;
};

// looksLikeExplorerEntity gates the /v1/search call so it only fires
// when the input has the SHAPE of an explorer entity — a tx hash,
// ledger sequence, G-strkey account, or C-contract. Asset/protocol
// searches (handled fully client-side) don't trigger a backend
// round-trip.
function looksLikeExplorerEntity(q: string): boolean {
  const s = q.trim();
  if (!s) return false;
  return (
    /^[0-9a-fA-F]{64}$/.test(s) || // tx hash
    /^\d{1,12}$/.test(s) || // ledger sequence
    /^G[A-Z2-7]{55}$/.test(s) || // account (G-strkey)
    /^C[A-Z2-7]{55}$/.test(s) // contract (C-strkey)
  );
}

// explorerHref maps a /v1/search classification to the explorer
// route per ADR-0038 Phase D. Prefers the backend-provided href when
// present; otherwise routes by kind + canonical. Returns null for
// unknown / unsupported / canonical-less classifications.
function explorerHref(c: SearchClassification): string | null {
  const canonical = c.canonical?.trim();
  switch (c.kind) {
    case 'transaction':
      return canonical ? `/transactions/${encodeURIComponent(canonical)}/` : null;
    case 'ledger':
      return canonical ? `/ledgers/${encodeURIComponent(canonical)}/` : null;
    case 'contract':
      return canonical ? `/contracts/${encodeURIComponent(canonical)}/` : null;
    case 'account':
      // Accounts are unbounded → the query-param explorer page
      // (/accounts?id=), NOT /issuers/{g} (which only static-exports
      // the top ~100 issuers and 404s every ordinary account).
      return canonical ? `/accounts/${encodeURIComponent(canonical)}/` : null;
    case 'asset':
      // The classification may hand back a ready-made explorer href
      // (e.g. /assets/<slug>); prefer it, else build from canonical.
      if (c.href && c.href.startsWith('/')) return c.href;
      return canonical ? `/assets/${encodeURIComponent(canonical)}` : null;
    default:
      return null;
  }
}

const EXPLORER_KIND_LABEL: Record<
  Exclude<SearchClassification['kind'], 'unknown'>,
  string
> = {
  transaction: 'Transaction',
  ledger: 'Ledger',
  account: 'Account',
  contract: 'Contract',
  asset: 'Asset',
};

type CurrencyEntry = { ticker: string; name: string };

const STATIC_PAGES: Result[] = [
  { type: 'page', label: 'Home', href: '/' },
  {
    type: 'page',
    label: 'Assets',
    hint: 'every asset — crypto, fiat, stablecoins',
    href: '/assets',
  },
  {
    type: 'page',
    label: 'Exchanges',
    hint: 'CEXes + per-pair tables',
    href: '/exchanges',
  },
  {
    type: 'page',
    label: 'Markets',
    hint: 'cross-source pair listing',
    href: '/markets',
  },
  { type: 'page', label: 'Issuers', href: '/issuers' },
  { type: 'page', label: 'Transactions', href: '/transactions', hint: 'recent network activity' },
  { type: 'page', label: 'Contracts', href: '/contracts', hint: 'active Soroban contracts' },
  { type: 'page', label: 'SDEX Markets', href: '/dexes/sdex', hint: 'native order book' },
  { type: 'page', label: 'AMM Pools', href: '/dexes' },
  { type: 'page', label: 'Lending', href: '/lending' },
  { type: 'page', label: 'Aggregators', href: '/aggregators' },
  { type: 'page', label: 'Bridges', href: '/bridges', hint: 'CCTP · Rozo' },
  { type: 'page', label: 'Oracles', href: '/oracles' },
  { type: 'page', label: 'Sources', href: '/sources' },
  {
    type: 'page',
    label: 'Methodology',
    hint: 'how rates are computed',
    href: '/methodology',
  },
  {
    type: 'page',
    label: 'Research',
    hint: 'ADRs + architecture docs',
    href: '/research',
  },
  { type: 'page', label: 'Changelog', href: '/changelog' },
  { type: 'page', label: 'Diagnostics', href: '/diagnostics' },
  { type: 'page', label: 'Anomalies', href: '/anomalies' },
  { type: 'page', label: 'Divergences', href: '/divergences' },
  { type: 'page', label: 'MEV', href: '/mev' },
  {
    type: 'page',
    label: 'Status',
    hint: 'live system status',
    href: '/status',
  },
  { type: 'page', label: 'API docs', href: 'https://docs.stellarindex.io' },
  { type: 'page', label: 'Sign in', hint: 'magic-link auth', href: '/signin' },
  {
    type: 'page',
    label: 'Sign up',
    hint: 'create your account',
    href: '/signup',
  },
  { type: 'page', label: 'Account', hint: 'manage API keys', href: '/account' },
  {
    type: 'page',
    label: 'Pricing',
    hint: 'plans, quotas, SLAs',
    href: '/pricing',
  },
  { type: 'page', label: 'Blog', hint: 'engineering notes', href: '/blog' },
  { type: 'page', label: 'Company', href: '/company' },
  { type: 'page', label: 'Careers', href: '/careers' },
  {
    type: 'page',
    label: 'Widgets',
    hint: 'embeddable price cards',
    href: '/widgets',
  },
  {
    type: 'page',
    label: 'Contact',
    hint: 'sales / security / GitHub',
    href: '/contact',
  },
  { type: 'page', label: 'Go SDK', hint: 'pkg/client examples', href: '/sdk' },
];

const PROTOCOLS: Result[] = [
  { type: 'protocol', label: 'Soroswap', hint: 'AMM + router', href: '/dexes' },
  { type: 'protocol', label: 'Phoenix', hint: 'AMM', href: '/dexes' },
  {
    type: 'protocol',
    label: 'Aquarius',
    hint: 'AMM with gauges',
    href: '/dexes',
  },
  {
    type: 'protocol',
    label: 'SDEX',
    hint: 'native order book',
    href: '/dexes',
  },
  { type: 'protocol', label: 'Blend', hint: 'lending', href: '/lending' },
  {
    type: 'protocol',
    label: 'DeFindex',
    hint: 'yield aggregator',
    href: '/aggregators',
  },
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
  // a11y (audit-2026-06-14 Q3): dialog focus management. dialogRef scopes the
  // Tab-trap; restoreFocusRef returns focus to whatever was focused when the
  // dialog opened (the ⌘K trigger or wherever the keyboard user was).
  const dialogRef = useRef<HTMLDivElement>(null);
  const restoreFocusRef = useRef<HTMLElement | null>(null);
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

  // Currency catalogue — fetched once on first modal open, cached
  // for the session. Used so typing "EUR" / "JPY" / "BRL" jumps
  // straight to /currencies/<ticker> instead of falling through
  // to the seeded protocol list.
  //
  // F-1201 migration (audit-2026-05-12): pre-rc.48 this called
  // /v1/currencies; rc.48 removed that route. /v1/assets/verified
  // returns the verified catalogue with class ∈ {crypto, stablecoin,
  // fiat}; filter to fiat client-side so the search index keeps
  // its "ISO ticker → currencies/<t>" affordance.
  const currencies = useQuery<CurrencyEntry[]>({
    queryKey: ['/v1/assets/verified', 'searchindex-fiat'],
    enabled: open,
    staleTime: 60 * 60 * 1000,
    queryFn: async () => {
      const env = await apiGet<{
        data: Array<{ ticker: string; name: string; class: string }>;
      }>('/v1/assets/verified');
      return (env.data ?? [])
        .filter((row) => row.class === 'fiat')
        .map((row) => ({ ticker: row.ticker, name: row.name }));
    },
  });

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

  // a11y (Q3): on open, remember the element to return focus to; on close,
  // restore it so keyboard/SR users aren't dumped on <body>.
  useEffect(() => {
    if (open) {
      restoreFocusRef.current = document.activeElement as HTMLElement | null;
    } else if (restoreFocusRef.current) {
      restoreFocusRef.current.focus?.();
      restoreFocusRef.current = null;
    }
  }, [open]);

  // a11y (Q3): trap Tab within the dialog so focus can't walk out into the
  // (still-visible) page behind the overlay.
  function trapTab(e: React.KeyboardEvent<HTMLDivElement>) {
    if (e.key !== 'Tab' || !dialogRef.current) return;
    const focusables = dialogRef.current.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input, [tabindex]:not([tabindex="-1"])',
    );
    if (focusables.length === 0) return;
    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    const active = document.activeElement;
    if (e.shiftKey && active === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      first.focus();
    }
  }

  // Debounce the live input into debouncedQ — 200ms balances
  // "feels live" with "doesn't fan out a request per keystroke".
  useEffect(() => {
    const t = setTimeout(() => setDebouncedQ(q.trim()), 200);
    return () => clearTimeout(t);
  }, [q]);

  const { data: verifiedSlugs } = useVerifiedSlugs();

  // Explorer classification — hits GET /v1/search?q= only when the
  // debounced input has the shape of an explorer entity (tx hash /
  // ledger seq / account / contract). Purely additive: the result
  // is prepended as a top "direct-jump" row; existing asset /
  // protocol / page search is untouched.
  const explorer = useQuery<SearchClassification | null>({
    queryKey: ['/v1/search', debouncedQ],
    enabled: open && looksLikeExplorerEntity(debouncedQ),
    staleTime: 60_000,
    retry: false,
    queryFn: async () => {
      const env = await apiGet<{ data: SearchClassification }>('/v1/search', {
        q: debouncedQ,
      });
      return env.data ?? null;
    },
  });

  const explorerResult = useMemo<Result | null>(() => {
    const c = explorer.data;
    if (!c || c.kind === 'unknown') return null;
    const href = explorerHref(c);
    if (!href) return null;
    const canonical = c.canonical ?? debouncedQ;
    const short =
      canonical.length > 16
        ? `${canonical.slice(0, 8)}…${canonical.slice(-6)}`
        : canonical;
    return {
      type: c.kind,
      label: `${EXPLORER_KIND_LABEL[c.kind]} ${short}`,
      hint: c.note ?? 'open in explorer',
      href,
    };
  }, [explorer.data, debouncedQ]);

  const results = useMemo(() => {
    const isServerSearched = debouncedQ.length >= 2;
    const sourceCoins = isServerSearched
      ? (searchedCoins.data?.coins ?? [])
      : (topCoins.data?.coins ?? []);
    const base = search(
      q,
      sourceCoins,
      currencies.data ?? [],
      isServerSearched,
      verifiedSlugs,
    );
    // Prepend the explorer classification (if any), de-duping on href
    // so we don't show the same /assets/ link twice when /v1/search
    // classified the input as an asset the client index also matched.
    if (explorerResult) {
      const filtered = base.filter((r) => r.href !== explorerResult.href);
      return [explorerResult, ...filtered].slice(0, 12);
    }
    return base;
  }, [
    q,
    debouncedQ,
    searchedCoins.data,
    topCoins.data,
    currencies.data,
    verifiedSlugs,
    explorerResult,
  ]);

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="flex w-full items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2 text-sm text-ink-muted shadow-xs transition-colors hover:border-line-strong hover:text-ink-body"
        aria-label="Open search"
      >
        <Search className="h-4 w-4 shrink-0" />
        <span>Search</span>
        <kbd className="ml-auto rounded-sm border border-line bg-surface-muted px-1.5 py-0.5 text-[10px] font-medium text-ink-faint">
          ⌘K
        </kbd>
      </button>
      {open && (
        <div
          className="fixed inset-0 z-50 flex items-start justify-center bg-ink/50 p-4 pt-24"
          onClick={() => setOpen(false)}
        >
          <div
            ref={dialogRef}
            className="w-full max-w-xl overflow-hidden rounded-lg bg-surface shadow-2xl"
            onClick={(e) => e.stopPropagation()}
            onKeyDown={trapTab}
            role="dialog"
            aria-modal="true"
            aria-label="Site search"
          >
            <div className="flex items-center gap-2 border-b border-line px-3 py-3">
              <Search className="h-4 w-4 text-ink-faint" aria-hidden="true" />
              <input
                autoFocus
                aria-label="Search coins, pairs, protocols, accounts, and transactions"
                className="flex-1 bg-transparent text-sm outline-hidden placeholder:text-ink-faint"
                placeholder="Coins, pairs, protocols, accounts, transactions…"
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
              <button
                type="button"
                onClick={() => setOpen(false)}
                className="text-ink-faint hover:text-ink-body"
                aria-label="Close"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <ul className="max-h-96 overflow-y-auto p-2 text-sm">
              {results.length === 0 && (
                <li className="px-3 py-2 text-xs text-ink-muted">
                  No matches across the asset directory, protocols, or pages.
                </li>
              )}
              {results.map((r) => (
                <li key={`${r.type}:${r.label}:${r.href}`}>
                  <Link
                    href={r.href}
                    onClick={() => setOpen(false)}
                    className="flex items-center justify-between rounded-md px-3 py-2 hover:bg-surface-muted"
                  >
                    <span className="flex items-center gap-2">
                      <span className="rounded-sm bg-surface-subtle px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-ink-muted">
                        {r.type}
                      </span>
                      <span className="font-medium">{r.label}</span>
                      {r.verified && (
                        <span
                          title="Verified currency"
                          aria-label="Verified currency"
                          className="inline-flex items-center"
                        >
                          <svg
                            xmlns="http://www.w3.org/2000/svg"
                            viewBox="0 0 20 20"
                            fill="currentColor"
                            className="h-3 w-3 text-up"
                            aria-hidden="true"
                          >
                            <path
                              fillRule="evenodd"
                              d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z"
                              clipRule="evenodd"
                            />
                          </svg>
                        </span>
                      )}
                      {r.hint && (
                        <span className="text-xs text-ink-muted">
                          — {r.hint}
                        </span>
                      )}
                    </span>
                    <span className="font-mono text-xs text-ink-faint">
                      {r.href}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
            <div className="border-t border-line-subtle px-3 py-1.5 text-[10px] text-ink-muted">
              <kbd>tab</kbd> navigate · <kbd>↵</kbd> open · <kbd>esc</kbd> close
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
  currencies: CurrencyEntry[],
  isServerSearched: boolean,
  verifiedSlugs?: Set<string>,
): Result[] {
  const norm = q.trim().toLowerCase();
  const coinResults = coins.map((c) =>
    coinResult(c, verifiedSlugs?.has(c.slug.toLowerCase()) ?? false),
  );
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
  // Route to the query-param account explorer (/accounts?id=), not
  // /issuers/{g} (static-exports only the top ~100 issuers → 404 for
  // every ordinary account).
  const gMatch = q.trim().match(/^G[A-Z2-7]{55}$/);
  if (gMatch) {
    direct.push({
      type: 'account',
      label: `Account ${gMatch[0].slice(0, 8)}…${gMatch[0].slice(-4)}`,
      hint: 'open account detail',
      href: `/accounts/${encodeURIComponent(gMatch[0])}/`,
    });
  }

  // ISO-4217 ticker exact match → direct-jump to /assets/<ticker>.
  // R-018 assets-unification: fiat currencies live under /assets;
  // /v1/assets/{ticker} dispatches via the catalogue's ticker
  // fallback (matches USD → us-dollar slug → GlobalAssetView).
  // We only direct-jump on an exact 3-letter-ticker match so
  // partial codes (e.g. "U" while the user is mid-typing "USDC")
  // fall through to coin results.
  const upper = q.trim().toUpperCase();
  if (/^[A-Z]{3}$/.test(upper)) {
    const match = currencies.find((c) => c.ticker === upper);
    if (match) {
      direct.push({
        type: 'currency',
        label: `${match.ticker} — ${match.name}`,
        hint: 'open asset detail',
        href: `/assets/${match.ticker}`,
      });
    }
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
  const matchedCurrencies = currencies
    .filter(
      (c) =>
        c.ticker.toLowerCase().includes(norm) ||
        c.name.toLowerCase().includes(norm),
    )
    .slice(0, 5)
    .map(currencyResult);
  const matchedOther = [...PROTOCOLS, ...STATIC_PAGES].filter((r) =>
    match(norm, r),
  );
  return [
    ...direct,
    ...matchedCoins,
    ...matchedCurrencies,
    ...matchedOther,
  ].slice(0, 12);
}

function currencyResult(c: CurrencyEntry): Result {
  return {
    type: 'currency',
    label: c.ticker,
    hint: c.name,
    href: assetHrefFor(c.ticker),
  };
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

function coinResult(c: Coin, verified: boolean): Result {
  return {
    type: 'coin',
    label: c.code,
    hint: c.slug,
    href: `/assets/${c.slug}`,
    verified,
  };
}

function match(q: string, r: Result): boolean {
  const hay = `${r.label} ${r.hint ?? ''} ${r.href}`.toLowerCase();
  // Allow each query token to match anywhere in the haystack.
  return q.split(/\s+/).every((t) => hay.includes(t));
}
