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
};

type SourcesEnvelope = { data: Source[] };

/** useSources — fetches the source registry. */
export function useSources(classFilter?: Source['class']) {
  return useQuery<Source[]>({
    queryKey: ['/v1/sources', classFilter ?? 'all'],
    queryFn: async () => {
      const env = await apiGet<SourcesEnvelope | Source[]>(
        '/v1/sources',
        classFilter ? { class: classFilter } : undefined,
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
};

/**
 * useCoins — fetches the registry-aware coin directory. v0 returns
 * bare classic-asset rows; future passes will join change_summary_5m
 * + classic_asset_stats_5m so each row carries pre-computed price +
 * delta + volume.
 *
 * The API wraps the array in `{ data: [...] }` per the standard
 * envelope; the hook unwraps it so consumers get a plain array.
 */
type CoinsEnvelope = { data: Coin[] };

export function useCoins(limit = 100) {
  return useQuery<Coin[]>({
    queryKey: ['/v1/coins', limit],
    queryFn: async () => {
      const env = await apiGet<CoinsEnvelope | Coin[]>('/v1/coins', { limit });
      return Array.isArray(env) ? env : env.data;
    },
  });
}

export type IssuerListEntry = {
  g_strkey: string;
  home_domain?: string;
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
export function useMarkets(limit = 100) {
  return useQuery<{ markets: Market[]; nextCursor?: string }>({
    queryKey: ['/v1/markets', limit],
    queryFn: async () => {
      const env = await apiGet<MarketsEnvelope | Market[]>('/v1/markets', { limit });
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
