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

/** useSources — fetches the source registry. */
export function useSources(classFilter?: Source['class']) {
  return useQuery<Source[]>({
    queryKey: ['/v1/sources', classFilter ?? 'all'],
    queryFn: () =>
      apiGet<Source[]>('/v1/sources', classFilter ? { class: classFilter } : undefined),
  });
}
