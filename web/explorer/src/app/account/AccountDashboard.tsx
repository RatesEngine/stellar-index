'use client';

import {
  AlertCircle,
  CheckCircle2,
  Copy,
  KeyRound,
  Loader2,
  LogOut,
  Plus,
} from 'lucide-react';
import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';

import { API_BASE_URL } from '@/api/client';

interface AccountMe {
  // Magic-link session callers populate `user` + `account`.
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
  key_prefix?: string;
  label?: string;
  tier?: string;
  rate_limit_per_min?: number;
  created_at?: string;
}

interface APIKey {
  key_id: string;
  label?: string;
  key_prefix?: string;
  tier?: string;
  rate_limit_per_min?: number;
  created_at: string;
}

interface UsageRow {
  date: string;
  requests: number;
}

type State =
  | { kind: 'loading' }
  | { kind: 'unauthed' }
  | { kind: 'error'; message: string }
  | { kind: 'authed'; me: AccountMe; keys: APIKey[]; usage: UsageRow[] };

async function apiFetch<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(`${API_BASE_URL}${path}`, {
    credentials: 'include',
    headers: {
      Accept: 'application/json',
      ...(opts.body ? { 'Content-Type': 'application/json' } : {}),
      ...opts.headers,
    },
    ...opts,
  });
  if (res.status === 401) {
    throw new ApiError(401, 'unauthorised');
  }
  if (!res.ok) {
    let detail = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { detail?: string };
      if (body.detail) detail = body.detail;
    } catch {
      // ignore
    }
    throw new ApiError(res.status, detail);
  }
  if (res.status === 204) return undefined as unknown as T;
  return (await res.json()) as T;
}

class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export function AccountDashboard() {
  const [state, setState] = useState<State>({ kind: 'loading' });
  const [showMintForm, setShowMintForm] = useState(false);
  const [mintLabel, setMintLabel] = useState('');
  const [mintError, setMintError] = useState<string | null>(null);
  const [mintedKey, setMintedKey] = useState<{ key_id: string; plaintext: string } | null>(null);
  const [copied, setCopied] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setState({ kind: 'loading' });
    try {
      const me = await apiFetch<{ data: AccountMe }>('/v1/account/me');
      let keys: APIKey[] = [];
      try {
        const list = await apiFetch<{ data: APIKey[] }>('/v1/account/keys');
        keys = list.data ?? [];
      } catch (err) {
        if (!(err instanceof ApiError) || err.status !== 401) throw err;
      }
      let usage: UsageRow[] = [];
      try {
        const u = await apiFetch<{ data: UsageRow[] }>('/v1/account/usage');
        usage = u.data ?? [];
      } catch {
        // Usage is best-effort; don't block the rest of the page.
      }
      setState({ kind: 'authed', me: me.data, keys, usage });
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setState({ kind: 'unauthed' });
        return;
      }
      setState({
        kind: 'error',
        message: err instanceof Error ? err.message : 'unknown error',
      });
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  async function handleLogout() {
    try {
      await apiFetch('/v1/auth/logout', { method: 'POST' });
    } catch {
      // Logout is best-effort — clear UI state regardless.
    }
    setMintedKey(null);
    setShowMintForm(false);
    setState({ kind: 'unauthed' });
  }

  async function handleMint(e: React.FormEvent) {
    e.preventDefault();
    if (!mintLabel.trim()) return;
    setMintError(null);
    try {
      const res = await apiFetch<{ data: { key_id: string; plaintext: string } }>(
        '/v1/account/keys',
        {
          method: 'POST',
          body: JSON.stringify({ label: mintLabel.trim() }),
        },
      );
      setMintedKey({ key_id: res.data.key_id, plaintext: res.data.plaintext });
      setShowMintForm(false);
      setMintLabel('');
      void refresh();
    } catch (err) {
      setMintError(err instanceof Error ? err.message : 'unknown error');
    }
  }

  function copy(value: string, kind: string) {
    void navigator.clipboard.writeText(value);
    setCopied(kind);
    setTimeout(() => setCopied(null), 1800);
  }

  if (state.kind === 'loading') {
    return (
      <div className="flex items-center justify-center py-16 text-sm text-slate-500">
        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
        Loading your account…
      </div>
    );
  }

  if (state.kind === 'unauthed') {
    return (
      <div className="space-y-4 rounded-xl border border-slate-200 bg-white p-8 text-center shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <KeyRound className="mx-auto h-10 w-10 text-slate-400" />
        <div>
          <h2 className="text-lg font-semibold">Sign in to see your account</h2>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            Magic-link auth — we email you a one-click sign-in. No passwords.
          </p>
        </div>
        <div className="flex justify-center gap-3">
          <Link
            href="/signin"
            className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700"
          >
            Sign in
          </Link>
          <Link
            href="/signup"
            className="inline-flex items-center gap-2 rounded-md border border-slate-200 px-4 py-2 text-sm font-medium text-slate-700 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:text-slate-300"
          >
            Create account
          </Link>
        </div>
      </div>
    );
  }

  if (state.kind === 'error') {
    return (
      <div className="space-y-3 rounded-md border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900/40 dark:bg-red-950/40 dark:text-red-200">
        <div className="flex items-center gap-2 font-medium">
          <AlertCircle className="h-4 w-4" />
          Couldn&apos;t load your account
        </div>
        <p>{state.message}</p>
        <button
          type="button"
          onClick={() => void refresh()}
          className="rounded-md border border-red-300 px-3 py-1 text-xs hover:bg-red-100 dark:border-red-800 dark:hover:bg-red-900/40"
        >
          Retry
        </button>
      </div>
    );
  }

  const { me, keys, usage } = state;
  const userEmail = me.user?.email;
  const accountName = me.account?.name;
  const accountTier = me.account?.tier ?? me.tier ?? 'starter';

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4 rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <div className="space-y-1">
          {userEmail && (
            <div className="flex items-center gap-2 text-sm">
              <span className="text-slate-500">Signed in as</span>
              <span className="font-mono font-medium text-slate-900 dark:text-slate-100">
                {userEmail}
              </span>
            </div>
          )}
          {accountName && (
            <div className="text-xs text-slate-500">Account: {accountName}</div>
          )}
          <div className="mt-2 inline-flex items-center gap-1.5 rounded-full bg-brand-100 px-2 py-0.5 text-xs font-medium uppercase tracking-wider text-brand-800 dark:bg-brand-900/40 dark:text-brand-200">
            {accountTier}
          </div>
        </div>
        <button
          type="button"
          onClick={handleLogout}
          className="inline-flex items-center gap-1.5 rounded-md border border-slate-200 px-3 py-1.5 text-xs text-slate-600 hover:border-slate-400 hover:text-slate-900 dark:border-slate-700 dark:text-slate-400 dark:hover:text-slate-100"
        >
          <LogOut className="h-3.5 w-3.5" />
          Sign out
        </button>
      </div>

      {mintedKey && (
        <div className="space-y-3 rounded-xl border border-emerald-200 bg-emerald-50 p-4 text-sm dark:border-emerald-900/40 dark:bg-emerald-950/40">
          <div className="flex items-center gap-2 font-medium text-emerald-900 dark:text-emerald-200">
            <CheckCircle2 className="h-4 w-4" />
            New API key minted — copy it now, you won&apos;t see it again.
          </div>
          <div className="flex items-center gap-2 rounded-md bg-white p-2 font-mono text-xs dark:bg-slate-900">
            <code className="flex-1 select-all break-all">{mintedKey.plaintext}</code>
            <button
              type="button"
              onClick={() => copy(mintedKey.plaintext, 'minted')}
              className="rounded border border-slate-200 p-1 hover:bg-slate-100 dark:border-slate-700 dark:hover:bg-slate-800"
              aria-label="Copy"
            >
              {copied === 'minted' ? (
                <CheckCircle2 className="h-3.5 w-3.5 text-emerald-600" />
              ) : (
                <Copy className="h-3.5 w-3.5" />
              )}
            </button>
          </div>
        </div>
      )}

      <UsagePanel usage={usage} />

      <div className="rounded-xl border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <div className="flex items-center justify-between border-b border-slate-200 px-5 py-3 dark:border-slate-800">
          <h2 className="text-sm font-semibold">API keys</h2>
          {!showMintForm ? (
            <button
              type="button"
              onClick={() => setShowMintForm(true)}
              className="inline-flex items-center gap-1 rounded-md bg-brand-600 px-2.5 py-1 text-xs font-medium text-white hover:bg-brand-700"
            >
              <Plus className="h-3.5 w-3.5" />
              Mint key
            </button>
          ) : null}
        </div>

        {showMintForm && (
          <form onSubmit={handleMint} className="space-y-2 border-b border-slate-200 p-4 dark:border-slate-800">
            <label className="block text-xs text-slate-500">Label</label>
            <div className="flex gap-2">
              <input
                type="text"
                value={mintLabel}
                onChange={(e) => setMintLabel(e.target.value)}
                placeholder="e.g. production-server"
                required
                className="flex-1 rounded-md border border-slate-200 bg-white px-2 py-1 text-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 dark:border-slate-700 dark:bg-slate-900"
              />
              <button
                type="submit"
                disabled={!mintLabel.trim()}
                className="rounded-md bg-brand-600 px-3 py-1 text-xs font-medium text-white hover:bg-brand-700 disabled:opacity-50"
              >
                Mint
              </button>
              <button
                type="button"
                onClick={() => {
                  setShowMintForm(false);
                  setMintError(null);
                  setMintLabel('');
                }}
                className="rounded-md border border-slate-200 px-2 py-1 text-xs text-slate-600 hover:border-slate-400 dark:border-slate-700 dark:text-slate-400"
              >
                Cancel
              </button>
            </div>
            {mintError && <div className="text-xs text-red-600">{mintError}</div>}
          </form>
        )}

        <div className="divide-y divide-slate-100 dark:divide-slate-800">
          {keys.length === 0 ? (
            <div className="px-5 py-6 text-center text-sm text-slate-500">
              No keys yet — mint one above to start authenticating requests.
            </div>
          ) : (
            keys.map((k) => (
              <div key={k.key_id} className="flex items-center justify-between px-5 py-3">
                <div>
                  <div className="text-sm font-medium">
                    {k.label || <span className="text-slate-400 italic">unlabelled</span>}
                  </div>
                  <div className="font-mono text-[11px] text-slate-500">
                    {k.key_prefix ? `${k.key_prefix}…` : k.key_id}
                  </div>
                </div>
                <div className="text-right text-xs">
                  <div className="text-slate-700 dark:text-slate-300">
                    {k.tier ?? '—'} · {k.rate_limit_per_min?.toLocaleString() ?? '—'}/min
                  </div>
                  <div className="text-slate-500">
                    {new Date(k.created_at).toLocaleDateString()}
                  </div>
                </div>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}

function UsagePanel({ usage }: { usage: UsageRow[] }) {
  const total = usage.reduce((s, r) => s + (r.requests || 0), 0);
  return (
    <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-800 dark:bg-slate-900">
      <div className="flex items-baseline justify-between">
        <h2 className="text-sm font-semibold">Usage (last 30 days)</h2>
        <span className="font-mono text-xs text-slate-500">
          {total.toLocaleString()} requests
        </span>
      </div>
      {usage.length === 0 ? (
        <p className="mt-3 text-xs text-slate-500">
          No tracked requests yet for this account in the last 30 days.
          Requests count against the per-account daily window the
          UsageTracker middleware records.
        </p>
      ) : (
        <UsageBars rows={usage} />
      )}
    </div>
  );
}

function UsageBars({ rows }: { rows: UsageRow[] }) {
  const max = Math.max(...rows.map((r) => r.requests || 0), 1);
  return (
    <div className="mt-3 grid grid-cols-7 gap-0.5 sm:grid-cols-15 lg:grid-cols-30">
      {rows.map((r) => {
        const h = Math.max(2, (r.requests / max) * 36);
        return (
          <div
            key={r.date}
            title={`${r.date}: ${r.requests.toLocaleString()} requests`}
            className="flex h-10 items-end justify-center"
          >
            <div
              className="w-full rounded-sm bg-brand-500/70 dark:bg-brand-400/70"
              style={{ height: `${h}px` }}
            />
          </div>
        );
      })}
    </div>
  );
}
