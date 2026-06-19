// Typed hooks for the Stellar Index v1 API.
//
// Each hook wraps `useQuery` with a stable key + the fetcher built
// from `apiGet`. Components stay free of fetch logic — they just
// consume `data, isLoading, error` from these hooks.
//
// Adding an endpoint:
//
//   1. Hook returns the raw response body (JSON) typed inline. We
//      defer pulling shapes from `src/api/types.ts` until the
//      OpenAPI spec is fully populated for that endpoint and the
//      generated type is meaningful.
//   2. Query key starts with the endpoint's path so the dev tools'
//      hierarchical key view groups by surface.
//   3. Errors propagate as TanStack errors — components render
//      `<ErrorPanel>` placeholders.

'use client';

import { useQuery } from '@tanstack/react-query';
import { apiGet } from './client';

export type StatusResponse = {
  overall: 'ok' | 'degraded' | 'down' | 'unknown';
  latency: { p50_ms: number; p95_ms: number; p99_ms: number };
  freshness?: { last_aggregator_tick: string };
};

/**
 * useIssuerLookup — fetches /v1/issuers once, builds a
 * g_strkey → { home_domain, org_name } map cached for the
 * session. Used by AssetLabel to render classic-asset rows with
 * the issuer's known display name (e.g. "USDC · Circle" instead
 * of "USDC · GA5Z…KZVN") whenever the API knows the issuer.
 *
 * Cache-friendly because /v1/issuers is small (a few hundred
 * rows max) and changes only when the curated `known_issuers`
 * fallback gets a new entry or the operator's eventual
 * issuer-upsert path lands.
 */
/**
 * useVerifiedSlugs — fetches the lowercase slug set of every
 * verified-currency catalogue entry via /v1/assets/verified
 * (R-018 Phase 1.5). Used by listing / homepage components to mark
 * verified rows with a green check badge.
 *
 * Catalogue changes only at API restart (the catalogue is embedded
 * in the binary), so a 1-hour stale window is plenty. The hook
 * exposes a `has(slug)` helper so callers don't re-derive the Set
 * on every render.
 */
export function useVerifiedSlugs() {
  return useQuery<Set<string>>({
    queryKey: ['/v1/assets/verified', 'slug-set'],
    queryFn: async () => {
      const env = await apiGet<{
        data: Array<{ slug: string }>;
      }>('/v1/assets/verified');
      return new Set((env.data ?? []).map((e) => e.slug.toLowerCase()));
    },
    staleTime: 60 * 60 * 1000,
    gcTime: 24 * 60 * 60 * 1000,
    // Don't surface fetch errors to the UI — the badge degrades
    // silently to "not verified" when the catalogue endpoint isn't
    // wired or is unreachable. Same UX as if every slug were
    // unverified.
    retry: false,
  });
}

export function useIssuerLookup() {
  return useQuery<Record<string, { home_domain?: string; org_name?: string }>>({
    queryKey: ['/v1/issuers', 'lookup'],
    queryFn: async () => {
      const env = await apiGet<{
        data: Array<{ g_strkey: string; home_domain?: string; org_name?: string }>;
      }>('/v1/issuers', { limit: 500 });
      const out: Record<string, { home_domain?: string; org_name?: string }> = {};
      for (const row of env.data ?? []) {
        if (row.org_name || row.home_domain) {
          out[row.g_strkey] = {
            home_domain: row.home_domain,
            org_name: row.org_name,
          };
        }
      }
      return out;
    },
    staleTime: 60 * 60 * 1000,
    gcTime: 24 * 60 * 60 * 1000,
  });
}

/**
 * useSACWrappers — fetches the operator-config Stellar-Asset-Contract
 * wrapper map: SAC C-strkey → "CODE-ISSUER" classic asset key. Used
 * by AssetLabel-style components to render Soroban DEX pool rows
 * (Soroswap / Phoenix / Aquarius / Comet emit base/quote as raw SAC
 * contract addresses) with readable asset symbols. The map is
 * operator-static — only changes on API restart with new config —
 * so we cache aggressively (1 hour stale, infinite gc).
 */
export function useSACWrappers() {
  return useQuery<Record<string, string>>({
    queryKey: ['/v1/sac-wrappers'],
    queryFn: async () => {
      const env = await apiGet<{ data: Record<string, string> }>(
        '/v1/sac-wrappers',
      );
      return env.data ?? {};
    },
    staleTime: 60 * 60 * 1000,
    gcTime: 24 * 60 * 60 * 1000,
  });
}

/**
 * useStatus — fetches the API's live system-status aggregate.
 * Powers the navbar Status pill (green/amber/red) so the
 * indicator actually reflects state rather than always rendering
 * green. Polls every 60 s — any faster wastes load and the
 * status doc itself is cached for 30 s downstream.
 */
export function useStatus() {
  return useQuery<StatusResponse>({
    queryKey: ['/v1/status'],
    queryFn: async () => {
      const env = await apiGet<{ data: StatusResponse }>('/v1/status');
      return env.data;
    },
    refetchInterval: 60_000,
    // Don't burst the API on every page navigation — share the
    // cached result across components for 30 s.
    staleTime: 30_000,
  });
}

export type MeResponse = {
  user?: {
    id: string;
    email: string;
    display_name?: string;
    role?: string;
    is_staff?: boolean;
  };
  account?: {
    id: string;
    name?: string;
    slug?: string;
    tier?: string;
    status?: string;
  };
  // API-key callers populate the top-level fields.
  key_id?: string;
  tier?: string;
};

// useMe — returns the signed-in account (MeResponse) or null when
// signed out. Drives the navbar session widget + the logged-in
// Account nav group + the /account/* auth gate.
//
// Cross-origin session detection (F-03) is now wired: the explorer
// at stellarindex.io sends a CREDENTIALED request to the API at
// api.stellarindex.io (cross-origin, same-site). This works because
//   1. the session cookie is set with Domain=.stellarindex.io
//      (cookie_domain) so it's visible to the apex,
//   2. the API CORS middleware emits Access-Control-Allow-Credentials
//      for the explorer origin allow-list (allow_credentials=true),
//   3. the cookie is SameSite=None;Secure so browsers send it on the
//      cross-origin request.
// A signed-out visitor gets 401 → null (navbar shows the sign-in CTAs).
export function useMe() {
  return useQuery<MeResponse | null>({
    queryKey: ['/v1/account/me', 'credentialed'],
    queryFn: async () => {
      const url = `${API_BASE_URL_FOR_ME}/v1/account/me`;
      const res = await fetch(url, {
        credentials: 'include',
        headers: { Accept: 'application/json' },
      });
      if (res.status === 401) return null;
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
      const env = (await res.json()) as { data: MeResponse };
      return env.data;
    },
    refetchInterval: 5 * 60_000,
    staleTime: 60_000,
    retry: false,
  });
}

const API_BASE_URL_FOR_ME =
  // Inline the resolved base URL so this hook doesn't require the
  // apiGet helper (which doesn't pass `credentials: 'include'`).
  // Mirrors src/api/client.ts's resolution.
  typeof process !== 'undefined' && process.env.NEXT_PUBLIC_API_BASE_URL
    ? process.env.NEXT_PUBLIC_API_BASE_URL
    : 'https://api.stellarindex.io';

export type NetworkStats = {
  volume_24h_usd: string;
  markets_count_24h: number;
  assets_indexed: number;
  latest_ledger: number;
  exchange_sources: number;
  total_sources: number;
};

/**
 * useNetworkStats — fetches the consolidated network-level aggregate
 * the home strip uses for its 24h-volume / active-markets /
 * assets-indexed / sources-online numbers. Server-aggregated against
 * the full corpus (not capped at any pagination limit), so this is
 * the authoritative source for those numbers.
 *
 * Refetched every 60 s — the underlying continuous aggregate the
 * API reads from refreshes once a minute, so polling faster is
 * wasted load.
 */
export function useNetworkStats() {
  return useQuery<NetworkStats>({
    queryKey: ['/v1/network/stats'],
    queryFn: async () => {
      const env = await apiGet<{ data: NetworkStats }>('/v1/network/stats');
      return env.data;
    },
    refetchInterval: 60_000,
  });
}

export type ChangeSummary = {
  entity_type: 'coin' | 'protocol' | 'pair' | 'source';
  entity_id: string;
  refreshed_at: string;
  current_value: number;
  h1_value?: number;
  h1_delta_pct?: number;
  h24_value?: number;
  h24_delta_pct?: number;
  d7_value?: number;
  d7_delta_pct?: number;
  d30_value?: number;
  d30_delta_pct?: number;
  ath_value?: number;
  ath_at?: string;
  atl_value?: number;
  atl_at?: string;
  streak_direction?: 'up' | 'down' | 'flat';
  streak_days?: number;
  acceleration?: 'increasing' | 'flat' | 'decreasing';
};

/**
 * useChangeSummary — fetches the multi-window delta strip for one
 * entity. Returns 404 errors gracefully (the worker may not have
 * computed a row yet); components consuming this hook should treat
 * `error` as "no data yet" rather than a hard failure.
 */
export function useChangeSummary(
  entityType: ChangeSummary['entity_type'],
  entityID: string,
) {
  return useQuery<ChangeSummary>({
    queryKey: ['/v1/changes', entityType, entityID],
    queryFn: () => apiGet<ChangeSummary>(`/v1/changes/${entityType}/${entityID}`),
    enabled: !!entityID,
    staleTime: 60_000,
  });
}

export type Source = {
  name: string;
  class:
    | 'exchange'
    | 'aggregator'
    | 'oracle'
    | 'authority_sanity'
    | 'lending'
    | 'router'
    | 'bridge';
  subclass?: string;
  include_in_vwap: boolean;
  paid: boolean;
  backfill_available: boolean;
  backfill_safe: boolean;
  default_weight: number;
  // True when the source observes the Stellar network directly
  // (on-chain ingest) rather than reading an off-chain vendor API.
  // false for CEX / FX / aggregators / Chainlink. The Stellar-network
  // surfaces (/network, /sources) filter on this. Use isOnChainSource
  // rather than reading the field raw so older API builds (no field →
  // undefined) degrade to "show it" instead of hiding every source.
  on_chain?: boolean;
  // Populated only when the caller passes `includeStats`. 0 means
  // "no trades observed in 24h" — the API doesn't distinguish that
  // from "stats not requested" because both end up zero on the
  // wire (omitempty on a numeric field).
  trade_count_24h?: number;
  volume_24h_usd?: string;
  markets_count_24h?: number;
  // Populated only when the caller passes `{ sparkline: true }`.
  // Per-(source, hour) USD-volume buckets — 24 entries oldest →
  // newest, server-side zero-filled for hours with no trades.
  volume_history_24h?: { hour: string; volume_usd: string }[];
};

type SourcesEnvelope = { data: Source[] };

/**
 * isOnChainSource — true when a source observes the Stellar network
 * directly (vs an off-chain CEX / FX / aggregator / Chainlink feed).
 * Mirrors the backend `external.IsOnChain`. The explorer's
 * Stellar-network surfaces (/network, /sources) filter on this.
 *
 * Fails OPEN: if `on_chain` is missing (an older API build), we treat
 * the source as on-chain so the page degrades to "shows everything"
 * rather than "shows nothing". A correct, current API always sends it.
 */
export function isOnChainSource(s: Source): boolean {
  return s.on_chain !== false;
}

/**
 * useSources — fetches the source registry.
 *
 * `includeStats` opts into per-source 24h trade counts via the
 * `?include=stats` flag the backend added in #845. Cheap (one
 * GROUP BY against the trades hypertable) but not free, so the
 * static-only callers (e.g. the home page's source list) leave it
 * off.
 */
export function useSources(
  classFilter?: Source['class'],
  includeStats?: boolean,
  options?: { sparkline?: boolean },
) {
  const include = options?.sparkline
    ? 'stats,sparkline'
    : includeStats
      ? 'stats'
      : undefined;
  return useQuery<Source[]>({
    queryKey: [
      '/v1/sources',
      classFilter ?? 'all',
      include ?? 'no-stats',
    ],
    queryFn: async () => {
      const env = await apiGet<SourcesEnvelope | Source[]>(
        '/v1/sources',
        {
          ...(classFilter ? { class: classFilter } : {}),
          ...(include ? { include } : {}),
        },
      );
      return Array.isArray(env) ? env : env.data;
    },
    staleTime: 5 * 60_000,
  });
}

/**
 * useNativeUsdPrice — the canonical XLM/USD price from
 * /v1/price?asset=native (VWAP), plus a 24h % change derived from the
 * /v1/chart 24h series. CRITICAL: native XLM has NO row in
 * classic_assets, so it never appears in /v1/assets (useCoins) — code
 * that pulled "the first coin" or searched `q=XLM` resolved to USDC
 * (top by observation) and showed ~$1.00 mislabelled as XLM. Always
 * source XLM's price from /v1/price?asset=native, never the coins list.
 */
export function useNativeUsdPrice() {
  const price = useQuery<number | null>({
    queryKey: ['/v1/price', 'native', 'fiat:USD'],
    retry: false,
    staleTime: 30_000,
    queryFn: async () => {
      const env = await apiGet<{ data: { price?: string | null } }>('/v1/price', {
        asset: 'native',
        quote: 'fiat:USD',
      });
      const p = env.data?.price ? Number(env.data.price) : null;
      return p != null && Number.isFinite(p) && p > 0 ? p : null;
    },
  });
  const change = useQuery<number | null>({
    queryKey: ['/v1/chart', 'native', 'fiat:USD', '24h-change'],
    retry: false,
    staleTime: 60_000,
    queryFn: async () => {
      const env = await apiGet<{ data: { points?: { p?: string | null }[] } }>(
        '/v1/chart',
        { asset: 'native', quote: 'fiat:USD', timeframe: '24h', granularity: '1h' },
      );
      const pts = (env.data?.points ?? [])
        .map((x) => (x.p != null ? Number(x.p) : NaN))
        .filter((n) => Number.isFinite(n) && n > 0);
      if (pts.length < 2 || pts[0] <= 0) return null;
      return ((pts[pts.length - 1] - pts[0]) / pts[0]) * 100;
    },
  });
  return {
    price: price.data ?? null,
    change24hPct: change.data ?? null,
    isLoading: price.isLoading,
    isError: price.isError,
  };
}

export type Coin = {
  slug: string;
  asset_id: string;
  code: string;
  issuer: string;
  // type — "classic" | "soroban" | "global" | "external". "global"
  // identifies catalogue cross-chain rows (USDC the currency, GBP
  // the fiat) emitted by /v1/assets when asset_class is set or
  // asset_class=all is in play.
  type?: string;
  // class — populated for catalogue-backed rows. "fiat" |
  // "stablecoin" | "crypto". Absent on classic_assets rows.
  class?: string;
  // name — populated for catalogue rows from the verified-currency
  // catalogue (e.g. "Bitcoin", "US Dollar"). Absent on most
  // classic_assets rows.
  name?: string;
  first_seen_ledger: number;
  last_seen_ledger: number;
  observation_count: number;
  price_usd?: string | null;
  volume_24h_usd?: string | null;
  market_cap_usd?: string | null;
  circulating_supply?: string | null;
  change_1h_pct?: string | null;
  change_24h_pct?: string | null;
  change_7d_pct?: string | null;
  // 24 hourly USD-price samples (oldest first). Populated only
  // when the request includes `?include=sparkline`.
  price_history_24h?: { t: string; p?: string | null }[];
  // 7 daily USD-price samples (oldest first). Populated only
  // when the request includes `?include=sparkline7d`.
  price_history_7d?: { t: string; p?: string | null }[];
  // All-time-high USD price + day it was set. Populated only
  // when the request includes `?include=ath`.
  ath?: { usd: string; at: string } | null;
  // Non-empty when the asset's `issuer` G-strkey appears in the
  // curated scam directory. Mirrors the `scam_reason` field on
  // /v1/issuers; clients render a prominent warning when present.
  issuer_scam_reason?: string | null;
};

export type CoinsPage = {
  coins: Coin[];
  next_cursor: string;
  limit: number;
};

/**
 * useCoins — fetches the registry-aware asset directory.
 *
 * Sourced from /v1/assets (R-018 finish — assets-unification). The
 * hook name is kept for ergonomics during the consumer migration:
 * callers reading `data.coins[i].price_usd` work unchanged because
 * /v1/assets rows now carry the same field shape as /v1/coins
 * rows did (rc.47 lifted the missing scalars onto AssetDetail).
 *
 * Response envelope is reshaped from /v1/assets's `{data: [], pagination: {next}}`
 * to the legacy `{coins: [], next_cursor, limit}` shape so existing
 * consumers don't have to change. Future consumers should use
 * `useAssets` directly when one ships.
 *
 * Each row carries optional price_usd / volume_24h_usd /
 * market_cap_usd / circulating_supply when the aggregator has
 * computed them.
 */
type AssetsListEnvelope = {
  data: Coin[];
  pagination?: { next?: string };
};

export type AssetClassFilter = 'all' | 'fiat' | 'blockchain' | 'stablecoin';

export type AssetsPage = {
  assets: Coin[];
  next_cursor: string;
  limit: number;
};

/**
 * useAssets — the CMC/CoinGecko-style global asset directory.
 *
 * Backs the redesigned `/assets` page. Hits `/v1/assets` with the
 * unified-listing query params:
 *
 *   - `asset_class=all`        → catalogue (market-cap desc) then
 *                                classic_assets (volume desc) via
 *                                phase-prefixed cursor protocol.
 *   - `asset_class=fiat`       → 19 fiat catalogue rows.
 *   - `asset_class=stablecoin` → 5 stablecoin catalogue rows.
 *   - `asset_class=blockchain` → 21 crypto catalogue rows
 *                                (server-side alias for `crypto`).
 */
export function useAssets(
  assetClass: AssetClassFilter,
  limit: number,
  cursor: string,
  q: string | undefined,
  options?: { sparkline7d?: boolean },
) {
  const includeParts: string[] = [];
  if (options?.sparkline7d) includeParts.push('sparkline7d');
  const include = includeParts.length > 0 ? includeParts.join(',') : undefined;
  return useQuery<AssetsPage>({
    queryKey: [
      '/v1/assets',
      'unified',
      assetClass,
      limit,
      cursor,
      q ?? '',
      include ?? '',
    ],
    queryFn: async () => {
      const env = await apiGet<AssetsListEnvelope>('/v1/assets', {
        asset_class: assetClass,
        limit,
        ...(cursor ? { cursor } : {}),
        ...(q ? { q } : {}),
        ...(include ? { include } : {}),
      });
      return {
        assets: env.data ?? [],
        next_cursor: env.pagination?.next ?? '',
        limit,
      };
    },
    placeholderData: (prev) => prev,
  });
}

export function useCoins(
  limit = 100,
  issuer?: string,
  cursor?: string,
  q?: string,
  orderBy?: 'observation_count_desc' | 'volume_24h_usd_desc',
  options?: { sparkline?: boolean; sparkline7d?: boolean; ath?: boolean },
) {
  const includeParts: string[] = [];
  if (options?.sparkline) includeParts.push('sparkline');
  if (options?.sparkline7d) includeParts.push('sparkline7d');
  if (options?.ath) includeParts.push('ath');
  const include = includeParts.length > 0 ? includeParts.join(',') : undefined;
  return useQuery<CoinsPage>({
    queryKey: [
      '/v1/assets',
      limit,
      issuer ?? null,
      cursor ?? '',
      q ?? '',
      orderBy ?? 'observation_count_desc',
      include ?? '',
    ],
    queryFn: async () => {
      const env = await apiGet<AssetsListEnvelope>('/v1/assets', {
        limit,
        ...(issuer ? { issuer } : {}),
        ...(cursor ? { cursor } : {}),
        ...(q ? { q } : {}),
        ...(orderBy ? { order_by: orderBy } : {}),
        ...(include ? { include } : {}),
      });
      // Reshape /v1/assets envelope into the legacy CoinsPage so
      // existing consumers stay byte-for-byte compatible during the
      // migration. The row shape is identical (AssetDetail is a
      // strict superset of the old Coin) — just envelope re-wrap.
      return {
        coins: env.data ?? [],
        next_cursor: env.pagination?.next ?? '',
        limit,
      };
    },
    // Keep showing the previous page data while a new query (e.g.
    // sparkline-augmented or paginated) is in flight. Avoids a
    // full-table flash to the loading skeleton on every nav — the
    // user sees prior rows while sparklines fade in. TanStack v5's
    // `placeholderData: (prev) => prev` is the recommended idiom.
    placeholderData: (prev) => prev,
  });
}

export type IssuerListEntry = {
  g_strkey: string;
  home_domain?: string;
  org_name?: string;
  asset_count: number;
  total_observation_count: number;
  // Non-empty when the issuer is on the curated scam list (sourced
  // from stellar.expert's directory). Render a warning badge on
  // any UI that surfaces this issuer's row.
  scam_reason?: string;
};

type IssuersListEnvelope = { data: IssuerListEntry[] };

/**
 * useIssuers — fetches `/v1/issuers`, the issuer directory ranked
 * by total observation count across each issuer's classic assets.
 * v0 of this hook just returns the first page (default 100).
 */
export function useIssuers(limit = 100) {
  return useQuery<IssuerListEntry[]>({
    queryKey: ['/v1/issuers', limit],
    queryFn: async () => {
      const env = await apiGet<IssuersListEnvelope | IssuerListEntry[]>(
        '/v1/issuers',
        { limit },
      );
      return Array.isArray(env) ? env : env.data;
    },
    staleTime: 5 * 60_000,
  });
}

export type IssuedAsset = {
  asset_id: string;
  code: string;
  slug: string;
  first_seen_ledger: number;
  last_seen_ledger: number;
  observation_count: number;
};

export type Issuer = {
  g_strkey: string;
  home_domain?: string;
  org_name?: string;
  scam_reason?: string;
  auth_required?: boolean;
  auth_revocable?: boolean;
  auth_immutable?: boolean;
  auth_clawback?: boolean;
  sep1_resolved_at?: string;
  sep1_payload?: unknown;
  creation_ledger?: number;
  assets?: IssuedAsset[];
};

type IssuerEnvelope = { data: Issuer };

/**
 * useIssuer — fetches /v1/issuers/{g_strkey}. Returns the issuer
 * row + embedded assets list. 404 errors propagate as TanStack
 * errors; consumers should treat that as "no issuer record yet"
 * rather than a hard failure.
 */
export function useIssuer(gStrkey: string | undefined) {
  return useQuery<Issuer>({
    queryKey: ['/v1/issuers', gStrkey],
    queryFn: async () => {
      const env = await apiGet<IssuerEnvelope | Issuer>(
        `/v1/issuers/${gStrkey}`,
      );
      return 'data' in env ? env.data : env;
    },
    enabled: !!gStrkey,
    staleTime: 5 * 60_000,
  });
}

export type Market = {
  base: string;
  quote: string;
  last_trade_at: string;
  trade_count_24h: number;
  volume_24h_usd?: string | null;
  // Most recent quote-per-base price observed for this pair
  // (cross-source) within the trailing 24h. Numeric-stringified
  // for precision; null when no recent prices_1m bucket has a
  // non-null last_price.
  last_price?: string | null;
  volume_history_24h?: { hour: string; volume_usd: string }[];
};

type MarketsEnvelope = {
  data: Market[];
  pagination?: { next?: string };
};

/**
 * useMarkets — fetches the recently-active markets directory from
 * `/v1/markets`. Cursor pagination is intentionally NOT exposed
 * here — the v0 showcase page just renders the first page. When
 * we add a "Load more" button or virtual scrolling, switch to
 * useInfiniteQuery and surface the cursor from the envelope.
 */
export function useMarkets(
  limit = 100,
  orderBy?: 'pair' | 'volume_24h_usd_desc',
  options?: { sparkline?: boolean; asset?: string },
) {
  const include = options?.sparkline ? 'sparkline' : undefined;
  const asset = options?.asset;
  return useQuery<{ markets: Market[]; nextCursor?: string }>({
    queryKey: ['/v1/markets', limit, orderBy ?? 'pair', include ?? '', asset ?? ''],
    queryFn: async () => {
      const env = await apiGet<MarketsEnvelope | Market[]>('/v1/markets', {
        limit,
        ...(orderBy ? { order_by: orderBy } : {}),
        ...(include ? { include } : {}),
        ...(asset ? { asset } : {}),
      });
      if (Array.isArray(env)) return { markets: env };
      return { markets: env.data, nextCursor: env.pagination?.next };
    },
    staleTime: 60_000,
    placeholderData: (prev) => prev,
  });
}

export type Pool = {
  source: string;
  base: string;
  quote: string;
  last_trade_at: string;
  trade_count_24h: number;
  volume_24h_usd?: string | null;
  last_price?: string | null;
};

type PoolsEnvelope = { data: Pool[]; pagination?: { next?: string } };

/**
 * usePools — fetches Stellar on-chain DEX pools from `/v1/pools`.
 * Unlike `/v1/markets` (which aggregates across ALL sources including
 * off-chain CEX reference feeds), `/v1/pools` is hard-scoped to the
 * DEX-subclass sources server-side — so this is the canonical
 * "Stellar markets" feed. One row per (source, base, quote).
 */
export function usePools(
  limit = 100,
  orderBy: 'pair' | 'volume_24h_usd_desc' = 'volume_24h_usd_desc',
) {
  return useQuery<Pool[]>({
    queryKey: ['/v1/pools', limit, orderBy],
    queryFn: async () => {
      const env = await apiGet<PoolsEnvelope | Pool[]>('/v1/pools', {
        limit,
        order_by: orderBy,
      });
      return Array.isArray(env) ? env : env.data;
    },
    staleTime: 60_000,
    placeholderData: (prev) => prev,
  });
}

export type Cursor = {
  source: string;
  sub_source?: string;
  last_ledger: number;
  last_updated: string;
  lag_seconds: number;
};

type CursorsEnvelope = { data: Cursor[] };

/**
 * useCursors — fetches the per-source ingest cursor table from
 * `/v1/diagnostics/cursors`. Refetched every 15s so the showcase
 * /diagnostics page reflects backfill progress without manual reload.
 */
export function useCursors() {
  return useQuery<Cursor[]>({
    queryKey: ['/v1/diagnostics/cursors'],
    queryFn: async () => {
      const env = await apiGet<CursorsEnvelope | Cursor[]>('/v1/diagnostics/cursors');
      return Array.isArray(env) ? env : env.data;
    },
    refetchInterval: 15_000,
  });
}

export type TradeRow = {
  source: string;
  ledger: number;
  tx_hash: string;
  op_index: number;
  ts: string;
  base_asset: string;
  quote_asset: string;
  base_amount: string;
  quote_amount: string;
  price?: string;
};

type TradeHistoryEnvelope = {
  data: TradeRow[];
  pagination?: { next?: string };
};

/**
 * useHistory — fetches recent trades for a (base, quote) pair from
 * `/v1/history`. Limit caps at 1000 server-side; the showcase
 * History tab requests 100 by default. Pagination cursor is left
 * on the envelope but not consumed yet.
 */
export function useHistory(base: string | undefined, quote: string, limit = 100) {
  return useQuery<TradeRow[]>({
    queryKey: ['/v1/history', base, quote, limit],
    enabled: !!base,
    queryFn: async () => {
      const env = await apiGet<TradeHistoryEnvelope>('/v1/history', {
        base: base ?? '',
        quote,
        limit,
      });
      return env.data ?? [];
    },
    staleTime: 30_000,
  });
}

export type AssetDetail = {
  asset_id: string;
  type: string;
  code?: string;
  issuer?: string;
  decimals: number;
  name?: string;
  description?: string;
  image?: string;
  org_name?: string;
  anchor_asset?: string;
  // F2 supply fields (ADR-0011) — decimal strings in the asset's
  // smallest integer unit. Null when the supply reader isn't wired
  // or the asset has no snapshot.
  circulating_supply?: string | null;
  total_supply?: string | null;
  max_supply?: string | null;
  market_cap_usd?: string | null;
  fdv_usd?: string | null;
  supply_basis?: string | null;
  volume_24h_usd?: string | null;
  change_24h_pct?: string | null;
  is_unlimited?: boolean | null;
  fixed_number?: string | null;
  max_number?: string | null;
};

/**
 * useAsset — fetches the rich asset-detail surface from
 * `/v1/assets/{id}`. Backs the Supply tab's F2 fields and any
 * panel that needs SEP-1 metadata for an asset.
 */
export function useAsset(assetID: string | undefined) {
  return useQuery<AssetDetail>({
    queryKey: ['/v1/assets/{id}', assetID],
    enabled: !!assetID,
    queryFn: async () => {
      const env = await apiGet<{ data: AssetDetail }>(
        `/v1/assets/${encodeURIComponent(assetID ?? '')}`,
      );
      return env.data;
    },
    staleTime: 60_000,
  });
}

/**
 * AssetSupply — live per-token supply from `/v1/assets/{id}/supply`,
 * summed from the decode-at-ingest supply_flows lake (ADR-0034).
 * Amounts are smallest-unit decimal strings (divide by 10^decimals to
 * display). `source` distinguishes mint/burn/clawback flows from the
 * native XLM ledger total_coins.
 */
export type AssetSupply = {
  asset_id: string;
  contract_id?: string;
  total_supply: string;
  mint_total?: string;
  burn_total?: string;
  clawback_total?: string;
  flow_count: number;
  source: 'mint_burn_flows' | 'ledger_total_coins';
};

/**
 * useAssetSupply — live on-chain supply (mint − burn − clawback),
 * always current (the indexer dual-sink feeds it; no rollup refresh).
 * 404s for a classic asset without a configured SAC wrapper, in which
 * case the caller degrades gracefully (omits the section).
 */
export function useAssetSupply(assetID: string | undefined) {
  return useQuery<AssetSupply>({
    queryKey: ['/v1/assets/{id}/supply', assetID],
    enabled: !!assetID,
    retry: false,
    queryFn: async () => {
      const env = await apiGet<{ data: AssetSupply }>(
        `/v1/assets/${encodeURIComponent(assetID ?? '')}/supply`,
      );
      return env.data;
    },
    staleTime: 30_000,
  });
}
