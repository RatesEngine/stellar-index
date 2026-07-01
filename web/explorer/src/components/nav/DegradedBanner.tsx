'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { AlertTriangle, XCircle, Info } from 'lucide-react';

import { API_BASE_URL } from '@/api/client';

/**
 * DegradedBanner surfaces in-product degraded / down state from
 * `/v1/status.overall`. Sits between the Navbar and the page
 * content; renders nothing when overall = "ok" or unknown.
 *
 * Why in-product instead of just on /status:
 * a consumer or developer reading prices doesn't naturally
 * navigate to a separate status domain to discover the API
 * is degraded — so a stale chart looks like normal data
 * unless we tell them otherwise. QA finding F-01 in
 * docs/review-2026-05-13-live-site-qa.md.
 *
 * Cadence: 60s. The status endpoint is cheap server-side
 * and the underlying state changes on the order of minutes,
 * not seconds.
 */

type Overall = 'ok' | 'degraded' | 'down' | 'unknown';

interface IncidentEntry {
  name: string;
  severity: 'page' | 'ticket' | 'informational';
}

interface StatusEnvelope {
  data: {
    overall: Overall;
    incidents?: {
      active_count?: number;
      page_count?: number;
      active?: IncidentEntry[];
    };
  };
}

const POLL_INTERVAL_MS = 60_000;

export function DegradedBanner() {
  const [overall, setOverall] = useState<Overall>('unknown');
  const [pageCount, setPageCount] = useState(0);
  const [activeCount, setActiveCount] = useState(0);
  const [topAlert, setTopAlert] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function poll() {
      try {
        const res = await fetch(`${API_BASE_URL}/v1/status`, { cache: 'no-store' });
        if (!res.ok) return;
        const env = (await res.json()) as StatusEnvelope;
        if (cancelled) return;
        setOverall(env.data.overall);
        const incs = env.data.incidents;
        setActiveCount(incs?.active_count ?? 0);
        setPageCount(incs?.page_count ?? 0);
        const top = (incs?.active ?? []).find((i) => i.severity === 'page')
          ?? (incs?.active ?? [])[0];
        setTopAlert(top?.name ?? null);
      } catch {
        // Silent — the banner is opportunistic; no signal beats
        // a misleading "all good" if the status feed itself is
        // unreachable. Existing console errors from the page
        // already surface that to developers.
      }
    }
    poll();
    const id = setInterval(poll, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  if (overall === 'ok' || overall === 'unknown') return null;

  const isDown = overall === 'down' || pageCount > 0;
  const tone = isDown
    ? {
        bg: 'bg-bad-50',
        border: 'border-bad-500/30',
        fg: 'text-bad-700',
        Icon: XCircle,
        label: 'Major incident in progress',
      }
    : {
        bg: 'bg-warn-50',
        border: 'border-warn-500/30',
        fg: 'text-warn-700',
        Icon: AlertTriangle,
        label: 'Degraded performance',
      };
  const Icon = tone.Icon;
  return (
    <div
      role="status"
      aria-live="polite"
      className={`border-y px-4 py-2 text-sm ${tone.bg} ${tone.border} ${tone.fg}`}
    >
      <div className="mx-auto flex max-w-7xl items-center gap-3">
        <Icon className="h-4 w-4 shrink-0" />
        <span className="font-medium">{tone.label}.</span>
        <span className="hidden text-xs opacity-90 sm:inline">
          {activeCount} active alert{activeCount === 1 ? '' : 's'}
          {topAlert && (
            <>
              {' '}
              · top:{' '}
              <code className="rounded-sm bg-surface/40 px-1 py-0.5 text-[11px]">
                {topAlert}
              </code>
            </>
          )}
        </span>
        <span className="ml-auto text-xs">
          <Link
            href="/status"
            target="_blank"
            rel="noopener noreferrer"
            className="underline-offset-2 hover:underline"
          >
            View status →
          </Link>
        </span>
      </div>
    </div>
  );
}

export function DegradedBannerFallback() {
  // Server-rendered placeholder. Keeps layout stable until the
  // client component mounts; renders nothing visible.
  return (
    <noscript>
      <div className="px-4 py-2 text-xs text-ink-faint">
        <Info className="mr-1 inline h-3 w-3" />
        Live status updates require JavaScript.
      </div>
    </noscript>
  );
}
