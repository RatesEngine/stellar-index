'use client';

import {
  AlertCircle,
  CheckCircle2,
  Copy,
  KeyRound,
  LogOut,
  Plus,
  RefreshCw,
} from 'lucide-react';
import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';

import { API_BASE_URL } from '@/api/client';

type Key = {
  key_id: string;
  label?: string;
  tier: string;
  rate_limit_per_min?: number;
  created_at: string;
};

type ListEnvelope = { data: Key[] };
type CreateEnvelope = {
  data: { key_id: string; plaintext: string; label?: string };
};

type State =
  | { kind: 'unauthed' }
  | { kind: 'loading' }
  | { kind: 'error'; message: string }
  | { kind: 'authed'; me: Key; keys: Key[] };

const STORAGE_KEY = 'ratesengine.api_key';

async function authedGet<T>(path: string, key: string): Promise<T> {
  const res = await fetch(`${API_BASE_URL}${path}`, {
    headers: { Authorization: `Bearer ${key}`, Accept: 'application/json' },
    cache: 'no-store',
  });
  if (!res.ok) {
    let detail = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { detail?: string };
      detail = body.detail ?? detail;
    } catch {
      // not problem+json
    }
    throw new Error(detail);
  }
  return (await res.json()) as T;
}

async function authedPost<T>(path: string, key: string, body: object): Promise<T> {
  const res = await fetch(`${API_BASE_URL}${path}`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${key}`,
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    let detail = `${res.status} ${res.statusText}`;
    try {
      const b = (await res.json()) as { detail?: string };
      detail = b.detail ?? detail;
    } catch {
      // ignore
    }
    throw new Error(detail);
  }
  return (await res.json()) as T;
}

function formatTier(tier: string, rateLimit?: number): { label: string; classes: string } {
  // Map rate-limit → tier name (matches the /signup page table).
  let label = tier;
  if (rateLimit !== undefined) {
    if (rateLimit >= 50000) label = 'Business';
    else if (rateLimit >= 10000) label = 'Pro';
    else if (rateLimit >= 1000) label = 'Starter';
  }
  const classes =
    label === 'Business'
      ? 'bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-200'
      : label === 'Pro'
        ? 'bg-brand-100 text-brand-800 dark:bg-brand-900/30 dark:text-brand-200'
        : 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300';
  return { label, classes };
}

export function AccountDashboard() {
  const [state, setState] = useState<State>({ kind: 'unauthed' });
  const [keyInput, setKeyInput] = useState('');
  const [showMintForm, setShowMintForm] = useState(false);
  const [mintLabel, setMintLabel] = useState('');
  const [mintError, setMintError] = useState<string | null>(null);
  const [mintedKey, setMintedKey] = useState<{ key_id: string; plaintext: string } | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  const refresh = useCallback(async (key: string) => {
    setState({ kind: 'loading' });
    try {
      const [me, list] = await Promise.all([
        authedGet<{ data: Key }>('/v1/account/me', key),
        authedGet<ListEnvelope>('/v1/account/keys', key),
      ]);
      setState({ kind: 'authed', me: me.data, keys: list.data });
    } catch (err) {
      setState({
        kind: 'error',
        message: err instanceof Error ? err.message : 'unknown error',
      });
    }
  }, []);

  // On first load, reuse the stored key if present.
  useEffect(() => {
    if (typeof window === 'undefined') return;
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (stored) {
      setKeyInput(stored);
      void refresh(stored);
    }
  }, [refresh]);

  function handleLogin(e: React.FormEvent) {
    e.preventDefault();
    if (!keyInput.trim()) return;
    window.localStorage.setItem(STORAGE_KEY, keyInput.trim());
    void refresh(keyInput.trim());
  }

  function handleLogout() {
    window.localStorage.removeItem(STORAGE_KEY);
    setKeyInput('');
    setState({ kind: 'unauthed' });
    setMintedKey(null);
    setShowMintForm(false);
  }

  async function handleMint(e: React.FormEvent) {
    e.preventDefault();
    if (!mintLabel.trim()) return;
    setMintError(null);
    try {
      const stored = window.localStorage.getItem(STORAGE_KEY);
      if (!stored) throw new Error('not logged in');
      const res = await authedPost<CreateEnvelope>('/v1/account/keys', stored, {
        label: mintLabel.trim(),
      });
      setMintedKey({ key_id: res.data.key_id, plaintext: res.data.plaintext });
      setShowMintForm(false);
      setMintLabel('');
      void refresh(stored);
    } catch (err) {
      setMintError(err instanceof Error ? err.message : 'unknown error');
    }
  }

  function copy(value: string, kind: string) {
    void navigator.clipboard.writeText(value);
    setCopiedKey(kind);
    setTimeout(() => setCopiedKey(null), 1800);
  }

  // ─── Unauthenticated form ────────────────────────────────────
  if (state.kind === 'unauthed' || state.kind === 'error') {
    return (
      <div className="space-y-6">
        <form onSubmit={handleLogin} className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <label htmlFor="key" className="mb-2 block text-sm font-medium text-slate-700 dark:text-slate-300">
            API key
          </label>
          <div className="flex flex-col gap-2 sm:flex-row">
            <input
              id="key"
              type="password"
              autoComplete="off"
              spellCheck={false}
              value={keyInput}
              onChange={(e) => setKeyInput(e.target.value)}
              placeholder="rek_…"
              className="flex-1 rounded-md border border-slate-300 bg-white px-3 py-2 font-mono text-sm text-slate-900 placeholder:text-slate-400 focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100"
            />
            <button
              type="submit"
              disabled={!keyInput.trim()}
              className="inline-flex items-center justify-center gap-1.5 rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-60"
            >
              <KeyRound className="h-4 w-4" />
              View account
            </button>
          </div>
          {state.kind === 'error' && (
            <div className="mt-3 flex items-start gap-2 rounded-lg border border-rose-200 bg-rose-50 p-3 dark:border-rose-900/50 dark:bg-rose-900/20">
              <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-rose-600 dark:text-rose-400" />
              <p className="text-sm text-rose-800 dark:text-rose-300">{state.message}</p>
            </div>
          )}
          <p className="mt-3 text-xs text-slate-500 dark:text-slate-400">
            No key yet?{' '}
            <Link href="/signup" className="text-brand-600 underline">
              Sign up
            </Link>{' '}
            to get one — Starter is free.
          </p>
        </form>
      </div>
    );
  }

  if (state.kind === 'loading') {
    return (
      <div className="rounded-xl border border-slate-200 bg-white p-8 text-center text-sm text-slate-500 dark:border-slate-800 dark:bg-slate-900">
        <RefreshCw className="mx-auto mb-2 h-5 w-5 animate-spin text-slate-400" />
        Loading account…
      </div>
    );
  }

  // ─── Authed dashboard ────────────────────────────────────────
  const tier = formatTier(state.me.tier, state.me.rate_limit_per_min);
  return (
    <div className="space-y-6">
      <section className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">
            Logged in as
          </h2>
          <button
            type="button"
            onClick={handleLogout}
            className="inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 text-xs text-slate-600 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800"
          >
            <LogOut className="h-3.5 w-3.5" />
            Forget key
          </button>
        </div>
        <dl className="grid grid-cols-2 gap-4 text-sm sm:grid-cols-4">
          <div>
            <dt className="text-xs uppercase tracking-wider text-slate-500">Tier</dt>
            <dd className="mt-0.5">
              <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-semibold ${tier.classes}`}>
                {tier.label}
              </span>
            </dd>
          </div>
          <div>
            <dt className="text-xs uppercase tracking-wider text-slate-500">Rate limit</dt>
            <dd className="mt-0.5 text-slate-900 dark:text-slate-100">
              {(state.me.rate_limit_per_min ?? 60).toLocaleString()} req/min
            </dd>
          </div>
          <div>
            <dt className="text-xs uppercase tracking-wider text-slate-500">Active key</dt>
            <dd className="mt-0.5 font-mono text-xs text-slate-900 dark:text-slate-100">
              {state.me.key_id}
            </dd>
          </div>
          <div>
            <dt className="text-xs uppercase tracking-wider text-slate-500">Member since</dt>
            <dd className="mt-0.5 text-slate-900 dark:text-slate-100">
              {new Date(state.me.created_at).toLocaleDateString()}
            </dd>
          </div>
        </dl>
      </section>

      <section className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">
            All keys ({state.keys.length})
          </h2>
          <button
            type="button"
            onClick={() => setShowMintForm((v) => !v)}
            className="inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-brand-700"
          >
            <Plus className="h-3.5 w-3.5" />
            Mint another key
          </button>
        </div>

        {showMintForm && (
          <form onSubmit={handleMint} className="mb-4 rounded-lg border border-slate-200 bg-slate-50 p-4 dark:border-slate-800 dark:bg-slate-800/50">
            <label htmlFor="mint-label" className="mb-1.5 block text-xs font-medium text-slate-700 dark:text-slate-300">
              Label
            </label>
            <div className="flex flex-col gap-2 sm:flex-row">
              <input
                id="mint-label"
                type="text"
                value={mintLabel}
                onChange={(e) => setMintLabel(e.target.value)}
                placeholder="ci-bot, dev-laptop, …"
                maxLength={128}
                className="flex-1 rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm text-slate-900 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100"
              />
              <button
                type="submit"
                disabled={!mintLabel.trim()}
                className="inline-flex items-center justify-center rounded-md bg-brand-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-60"
              >
                Create
              </button>
            </div>
            {mintError && (
              <p className="mt-2 text-sm text-rose-700 dark:text-rose-300">{mintError}</p>
            )}
          </form>
        )}

        {mintedKey && (
          <div className="mb-4 rounded-lg border border-emerald-200 bg-emerald-50 p-4 dark:border-emerald-800 dark:bg-emerald-900/20">
            <div className="mb-2 flex items-center gap-2 text-sm font-semibold text-emerald-900 dark:text-emerald-200">
              <CheckCircle2 className="h-4 w-4" />
              Key minted — {mintedKey.key_id}
            </div>
            <p className="mb-2 text-xs text-emerald-800 dark:text-emerald-300">
              Copy now — plaintext is shown once and unrecoverable.
            </p>
            <div className="flex items-stretch gap-2">
              <input
                readOnly
                value={mintedKey.plaintext}
                onFocus={(e) => e.currentTarget.select()}
                className="flex-1 rounded-md border border-emerald-300 bg-white px-2.5 py-1.5 font-mono text-xs text-slate-900 dark:border-emerald-800 dark:bg-slate-900 dark:text-slate-100"
              />
              <button
                type="button"
                onClick={() => copy(mintedKey.plaintext, mintedKey.key_id)}
                className="inline-flex items-center gap-1 rounded-md bg-emerald-700 px-2.5 py-1.5 text-xs font-semibold text-white hover:bg-emerald-800"
              >
                <Copy className="h-3 w-3" />
                {copiedKey === mintedKey.key_id ? 'Copied' : 'Copy'}
              </button>
            </div>
          </div>
        )}

        <div className="overflow-hidden rounded-lg border border-slate-200 dark:border-slate-800">
          <table className="min-w-full divide-y divide-slate-200 dark:divide-slate-800">
            <thead className="bg-slate-50 dark:bg-slate-800/50">
              <tr>
                <th className="px-3 py-2 text-left text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
                  Key
                </th>
                <th className="px-3 py-2 text-left text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
                  Label
                </th>
                <th className="px-3 py-2 text-left text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
                  Rate limit
                </th>
                <th className="px-3 py-2 text-left text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
                  Created
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-200 bg-white dark:divide-slate-800 dark:bg-slate-900">
              {state.keys.map((k) => (
                <tr key={k.key_id} className={k.key_id === state.me.key_id ? 'bg-brand-50/50 dark:bg-brand-900/10' : ''}>
                  <td className="whitespace-nowrap px-3 py-2 font-mono text-xs text-slate-900 dark:text-slate-100">
                    {k.key_id}
                    {k.key_id === state.me.key_id && (
                      <span className="ml-2 inline-flex items-center rounded-full bg-brand-600 px-1.5 py-0.5 text-[10px] font-medium text-white">
                        active
                      </span>
                    )}
                  </td>
                  <td className="whitespace-nowrap px-3 py-2 text-sm text-slate-700 dark:text-slate-300">
                    {k.label ?? '—'}
                  </td>
                  <td className="whitespace-nowrap px-3 py-2 text-sm text-slate-700 dark:text-slate-300">
                    {(k.rate_limit_per_min ?? 0).toLocaleString()} req/min
                  </td>
                  <td className="whitespace-nowrap px-3 py-2 text-sm text-slate-700 dark:text-slate-300">
                    {new Date(k.created_at).toLocaleDateString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <p className="mt-3 text-xs text-slate-500 dark:text-slate-400">
          Key revocation is on the post-launch backlog; if you need
          to deactivate a key today, contact support with the{' '}
          <code className="font-mono">key_id</code>.
        </p>
      </section>

      <section className="rounded-xl border border-slate-200 bg-slate-50 p-5 text-sm text-slate-700 dark:border-slate-800 dark:bg-slate-800/50 dark:text-slate-300">
        <h3 className="mb-2 font-semibold text-slate-900 dark:text-slate-100">
          Want a higher rate-limit?
        </h3>
        <p>
          Pro (10,000 req/min) and Business (50,000 req/min) tiers are available
          via Stripe. Your existing keys keep working — the tier upgrade lifts
          the rate-limit on the same key, no rotation needed. Contact sales or
          use the upgrade flow on the{' '}
          <Link href="/signup" className="underline">
            signup page
          </Link>
          .
        </p>
      </section>
    </div>
  );
}
