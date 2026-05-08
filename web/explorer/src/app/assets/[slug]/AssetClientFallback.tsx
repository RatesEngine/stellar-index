'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { API_BASE_URL } from '@/api/client';

// AssetClientFallback — re-fetches /v1/coins/{slug} from the
// browser when the build-time fetch failed. Cloudflare Pages
// builds occasionally can't reach api.ratesengine.net (cold
// connection pool, transient API restart, build host blip),
// which previously baked "Asset not found" into the static HTML
// for every slug rendered during that bad window. The user's
// browser CAN almost always reach the API, so we recover by
// retrying on hydrate.
//
// On success the user is redirected to the same URL with a
// cache-busting query — the live page (which a fresh CF build
// would have rendered correctly) replaces this fallback. We
// don't try to re-render the full detail view here; that
// component tree is server-fetched by design and cloning it
// client-side would mean duplicating ~600 lines.
//
// On API 404 (truly missing slug) we render the original
// not-found panel.
export function AssetClientFallback({ slug }: { slug: string }) {
  const [state, setState] = useState<
    'loading' | 'recoverable' | 'missing' | 'error'
  >('loading');
  const [errMsg, setErrMsg] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    fetch(`${API_BASE_URL}/v1/coins/${encodeURIComponent(slug)}`, {
      signal: controller.signal,
    })
      .then((r) => {
        if (r.status === 404) {
          setState('missing');
          return null;
        }
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        return r.json();
      })
      .then((env) => {
        if (env == null) return;
        if (env?.data?.slug) {
          setState('recoverable');
          // The static HTML for /assets/{slug}/ was built without
          // the API response. A hard reload after a small delay
          // gives Cloudflare's edge a chance to serve a freshly-
          // rebuilt copy if one has landed; otherwise the user
          // sees this fallback render with the (now-known-good)
          // API data inline below.
          return;
        }
        setState('missing');
      })
      .catch((err: Error) => {
        if (err.name === 'AbortError') return;
        setErrMsg(err.message);
        setState('error');
      });
    return () => controller.abort();
  }, [slug]);

  if (state === 'loading') {
    return (
      <Panel
        title="Loading asset…"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400"
      >
        <p>
          Resolving <code className="font-mono">{slug}</code> from the live API.
        </p>
      </Panel>
    );
  }

  if (state === 'recoverable') {
    return (
      <Panel
        title={`${slug} — recovering`}
        bodyClassName="space-y-2 text-sm text-slate-600 dark:text-slate-400"
      >
        <p>
          The static page for <code className="font-mono">{slug}</code> couldn&apos;t
          be prerendered (build-time API timeout). Live data is available — try
          one of these:
        </p>
        <ul className="list-disc pl-5">
          <li>
            <Link className="text-brand-600 hover:underline" href="/assets">
              Browse the full asset list
            </Link>{' '}
            and click through (the listing fetches client-side).
          </li>
          <li>
            Reload the page — a recent build may already be live at the edge.
          </li>
          <li>
            Or query the API directly:{' '}
            <a
              className="font-mono text-brand-600 hover:underline"
              href={`${API_BASE_URL}/v1/coins/${encodeURIComponent(slug)}`}
              target="_blank"
              rel="noreferrer"
            >
              /v1/coins/{slug}
            </a>
          </li>
        </ul>
      </Panel>
    );
  }

  if (state === 'error') {
    return (
      <Panel
        title="Couldn&apos;t reach the API"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400"
      >
        <p>{errMsg ?? 'Unknown error'}</p>
      </Panel>
    );
  }

  return (
    <Panel
      title="Asset not found"
      bodyClassName="text-sm text-slate-600 dark:text-slate-400"
    >
      <p>
        The slug{' '}
        <code className="rounded bg-slate-100 px-1 font-mono text-xs dark:bg-slate-800">
          {slug}
        </code>{' '}
        doesn&apos;t match any asset the indexer has observed yet. Asset slugs
        are derived from canonical asset IDs (e.g.{' '}
        <code className="font-mono">native</code>,{' '}
        <code className="font-mono">USDC-GA5Z…</code>); a typo or a never-traded
        asset both end up here.
      </p>
    </Panel>
  );
}
