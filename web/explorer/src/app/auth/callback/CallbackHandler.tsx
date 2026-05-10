'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { AlertCircle, CheckCircle2, Loader2 } from 'lucide-react';

import { API_BASE_URL } from '@/api/client';

type State =
  | { kind: 'verifying' }
  | { kind: 'redirecting' }
  | { kind: 'error'; message: string };

/**
 * CallbackHandler verifies the magic-link token via the API.
 *
 * The API endpoint (GET /v1/auth/callback?token=…&next=/path)
 * verifies the token, sets the session cookie via Set-Cookie,
 * and 303-redirects to {DashboardBaseURL}/{next}. By doing a
 * full-page navigation (rather than fetch), the Set-Cookie
 * actually applies and the browser then arrives at the
 * configured landing page logged in.
 *
 * If the operator has DashboardBaseURL = ratesengine.net, the
 * post-login redirect lands on /account here and the navbar's
 * useMe hook lights up.
 */
export function CallbackHandler() {
  const [state, setState] = useState<State>({ kind: 'verifying' });

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const params = new URLSearchParams(window.location.search);
    const token = params.get('token');
    if (!token) {
      setState({
        kind: 'error',
        message: 'Missing token. Request a new sign-in link from /signin.',
      });
      return;
    }
    // Pre-flight only — the actual redirect needs to be a full
    // page navigation so the API's Set-Cookie applies and the
    // 303 redirect lands the browser on the post-login page.
    const next = params.get('next') ?? '/account';
    const safeNext = next.startsWith('/') && !next.startsWith('//') ? next : '/account';
    const url = new URL(`${API_BASE_URL}/v1/auth/callback`);
    url.searchParams.set('token', token);
    url.searchParams.set('next', safeNext);
    setState({ kind: 'redirecting' });
    // Defer one tick so the "redirecting" UI shows briefly.
    const t = setTimeout(() => {
      window.location.replace(url.toString());
    }, 100);
    return () => clearTimeout(t);
  }, []);

  if (state.kind === 'error') {
    return (
      <div className="space-y-3 rounded-md border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900/40 dark:bg-red-950/40 dark:text-red-200">
        <div className="flex items-center justify-center gap-2 font-medium">
          <AlertCircle className="h-4 w-4" />
          Couldn&apos;t sign you in
        </div>
        <p>{state.message}</p>
        <p>
          <Link href="/signin" className="text-brand-600 hover:underline">
            Request a new link
          </Link>
        </p>
      </div>
    );
  }
  if (state.kind === 'redirecting') {
    return (
      <div className="space-y-2 text-sm text-slate-600 dark:text-slate-400">
        <div className="flex items-center justify-center gap-2 font-medium text-slate-900 dark:text-slate-100">
          <CheckCircle2 className="h-4 w-4 text-emerald-600" />
          Signing you in…
        </div>
        <p>Verifying your link with the API. You&apos;ll be redirected to your account.</p>
      </div>
    );
  }
  return (
    <div className="space-y-2 text-sm text-slate-600 dark:text-slate-400">
      <div className="flex items-center justify-center gap-2 font-medium text-slate-900 dark:text-slate-100">
        <Loader2 className="h-4 w-4 animate-spin" />
        Verifying your sign-in link…
      </div>
    </div>
  );
}
