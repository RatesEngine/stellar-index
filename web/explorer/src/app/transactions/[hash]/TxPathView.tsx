'use client';

import { useLastPathSegment } from '@/lib/useLastPathSegment';

import { TxView } from '../../tx/TxView';

/**
 * Path-reading wrapper for /transactions/[hash].
 *
 * Only ONE static page is built for this route (the `shell` sentinel — see
 * generateStaticParams in page.tsx). The Cloudflare Pages Function
 * (functions/transactions/[[path]].js) serves that shell's HTML for any
 * /transactions/{hash} that has no pre-rendered file, with the browser URL
 * preserved. So we read the real hash from the path at runtime (not from the
 * build-time route param, which is always the sentinel) and hand it to the
 * existing TxView.
 */
export function TxPathView() {
  const hash = useLastPathSegment();
  return <TxView hash={hash} />;
}
