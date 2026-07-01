'use client';

import { useSyncExternalStore } from 'react';

// The path only changes on a full navigation (these shells don't client-route
// within themselves), so there's nothing to subscribe to.
const subscribe = () => () => {};

function readLastSegment(): string {
  if (typeof window === 'undefined') return '';
  const seg = window.location.pathname.replace(/\/+$/, '').split('/').pop() ?? '';
  try {
    return decodeURIComponent(seg);
  } catch {
    return seg;
  }
}

/**
 * useLastPathSegment returns the decoded final segment of the current URL.
 *
 * The *PathView shells need this because Cloudflare serves one built static
 * shell for any /accounts/{g}, /tx/{hash}, /ledgers/{seq}, /contracts/{id}, so
 * the real id is NOT a build-time route param — it must be read from
 * window.location at runtime.
 *
 * useSyncExternalStore reads that client-only value without a
 * setState-in-effect (react-hooks/set-state-in-effect) and without a hydration
 * mismatch: the server snapshot is '' and the client snapshot is the real
 * segment, and React swaps them at hydration for us.
 */
export function useLastPathSegment(): string {
  return useSyncExternalStore(subscribe, readLastSegment, () => '');
}
