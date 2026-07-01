'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { API_BASE_URL } from '@/api/client';

// AssetClientFallback — re-fetches /v1/assets/{slug} from the
// browser when the build-time fetch failed. Cloudflare Pages
// builds occasionally can't reach api.stellarindex.io (cold
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
    'loading' | 'reloading' | 'recoverable' | 'missing' | 'error'
  >('loading');
  const [errMsg, setErrMsg] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    fetch(`${API_BASE_URL}/v1/assets/${encodeURIComponent(slug)}`, {
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
        if (env?.data?.asset_id) {
          // Static HTML for /assets/{slug}/ rendered without the
          // API response (build-time fetch hit a deploy window).
          // Live API is reachable now — auto-reload ONCE to get
          // the freshly-rebuilt page from Cloudflare's edge or, if
          // CF hasn't rebuilt yet, the same fallback re-runs and
          // we render the friendly recovery panel below without
          // looping.
          const reloadKey = `assetFallbackReloaded:${slug}`;
          if (typeof window !== 'undefined' && !sessionStorage.getItem(reloadKey)) {
            sessionStorage.setItem(reloadKey, '1');
            // Brief delay so the reload feels intentional (not a flash).
            setTimeout(() => window.location.reload(), 600);
            setState('reloading');
            return;
          }
          setState('recoverable');
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

  if (state === 'loading' || state === 'reloading') {
    return (
      <Panel
        title="Loading asset…"
        bodyClassName="text-sm text-ink-body"
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
        title={`${slug}`}
        bodyClassName="space-y-2 text-sm text-ink-body"
      >
        <p>
          We&apos;ve got live data for <code className="font-mono">{slug}</code>{' '}
          but this snapshot was rendered before the latest deploy settled.
          Try:
        </p>
        <ul className="list-disc pl-5">
          <li>
            <button
              type="button"
              onClick={() => {
                try { sessionStorage.removeItem(`assetFallbackReloaded:${slug}`); } catch {/* noop */}
                window.location.reload();
              }}
              className="text-brand-600 hover:underline"
            >
              Reload the page
            </button>
            {' '}— a freshly-rebuilt static copy usually lands within a minute.
          </li>
          <li>
            <Link className="text-brand-600 hover:underline" href="/assets">
              Browse the asset list
            </Link>
            {' '}— the listing fetches client-side and works even mid-deploy.
          </li>
          <li>
            Or query the API directly:{' '}
            <a
              className="font-mono text-brand-600 hover:underline"
              href={`${API_BASE_URL}/v1/assets/${encodeURIComponent(slug)}`}
              target="_blank"
              rel="noreferrer"
            >
              /v1/assets/{slug}
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
        bodyClassName="text-sm text-ink-body"
      >
        <p>{errMsg ?? 'Unknown error'}</p>
      </Panel>
    );
  }

  return (
    <Panel
      title="Asset not found"
      bodyClassName="text-sm text-ink-body"
    >
      <p>
        The slug{' '}
        <code className="rounded-sm bg-surface-subtle px-1 font-mono text-xs">
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
