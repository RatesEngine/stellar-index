// Typed hooks for the Rates Engine v1 API.
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

// useMe — null when signed-out (401), MeResponse when authed.
// Kept short: cookie sessions are stable across requests.
export function useMe() {
  return useQuery<MeResponse | null>({
    queryKey: ['/v1/account/me', 'cookie'],
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
    : 'https://api.ratesengine.net';

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
  });
}

export type Source = {
  name: string;
  class: 'exchange' | 'aggregator' | 'oracle' | 'authority_sanity';
  subclass?: string;
  include_in_vwap: boolean;
  paid: boolean;
  backfill_available: boolean;
  backfill_safe: boolean;
  default_weight: number;
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
  });
}

export type Coin = {
  slug: string;
  asset_id: string;
  code: string;
  issuer: string;
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
};

export type CoinsPage = {
  coins: Coin[];
  next_cursor: string;
  limit: number;
};

/**
 * useCoins — fetches the registry-aware asset directory.
 *
 * The /v1/coins response is paginated via keyset cursor. This
 * hook fetches a single page; consumers that want the full set
 * iterate by passing the previous response's `next_cursor` as
 * the next call's `cursor`.
 *
 * Each row carries optional price_usd / volume_24h_usd /
 * market_cap_usd / circulating_supply when the aggregator has
 * computed them.
 */
type CoinsEnvelope = { data: CoinsPage };

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
      '/v1/coins',
      limit,
      issuer ?? null,
      cursor ?? '',
      q ?? '',
      orderBy ?? 'observation_count_desc',
      include ?? '',
    ],
    queryFn: async () => {
      const env = await apiGet<CoinsEnvelope>('/v1/coins', {
        limit,
        ...(issuer ? { issuer } : {}),
        ...(cursor ? { cursor } : {}),
        ...(q ? { q } : {}),
        ...(orderBy ? { order_by: orderBy } : {}),
        ...(include ? { include } : {}),
      });
      return env.data;
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
  options?: { sparkline?: boolean },
) {
  const include = options?.sparkline ? 'sparkline' : undefined;
  return useQuery<{ markets: Market[]; nextCursor?: string }>({
    queryKey: ['/v1/markets', limit, orderBy ?? 'pair', include ?? ''],
    queryFn: async () => {
      const env = await apiGet<MarketsEnvelope | Market[]>('/v1/markets', {
        limit,
        ...(orderBy ? { order_by: orderBy } : {}),
        ...(include ? { include } : {}),
      });
      if (Array.isArray(env)) return { markets: env };
      return { markets: env.data, nextCursor: env.pagination?.next };
    },
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
  });
}
