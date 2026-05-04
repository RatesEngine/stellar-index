'use client';

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useState } from 'react';

/**
 * Wraps the app in a TanStack Query client. Default configuration
 * tuned for the showcase:
 *
 *   - 30s staleTime — most endpoints are 60s-cached at the CDN, so
 *     a 30s in-memory window keeps redundant fetches off the API.
 *   - 5min gcTime — hold onto data for navigations between pages.
 *   - refetchOnWindowFocus disabled — the showcase isn't a trading
 *     UI; tab-focus refetches add no value.
 *   - retry: 1 — one bounce on transient failures, then fail.
 *
 * Wrapped at the layout root so every client component can call
 * `useQuery` without re-arming the provider.
 */
export function QueryProvider({ children }: { children: React.ReactNode }) {
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 30_000,
            gcTime: 5 * 60_000,
            refetchOnWindowFocus: false,
            retry: 1,
          },
        },
      }),
  );
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
