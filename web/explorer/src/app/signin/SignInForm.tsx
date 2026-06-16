'use client';

import { useState } from 'react';
import { AlertCircle, Check, Loader2, Mail } from 'lucide-react';

import { API_BASE_URL } from '@/api/client';

type State =
  | { kind: 'idle' }
  | { kind: 'submitting' }
  | { kind: 'sent'; email: string }
  | { kind: 'error'; message: string };

export function SignInForm({ mode = 'signin' }: { mode?: 'signin' | 'signup' }) {
  const [email, setEmail] = useState('');
  const [state, setState] = useState<State>({ kind: 'idle' });

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    const trimmed = email.trim().toLowerCase();
    if (!trimmed) return;
    setState({ kind: 'submitting' });
    try {
      const res = await fetch(`${API_BASE_URL}/v1/auth/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email: trimmed }),
      });
      if (!res.ok) {
        let detail: string | undefined;
        try {
          const body = (await res.json()) as { detail?: string; title?: string };
          detail = body.detail ?? body.title;
        } catch {
          // ignore
        }
        setState({
          kind: 'error',
          message: detail ?? `Request failed (${res.status} ${res.statusText})`,
        });
        return;
      }
      setState({ kind: 'sent', email: trimmed });
    } catch {
      setState({ kind: 'error', message: 'Network error — please try again.' });
    }
  }

  if (state.kind === 'sent') {
    return (
      <div className="space-y-4 rounded-lg border border-up/30 bg-up-subtle p-6 text-sm">
        <div className="flex items-center gap-2 font-medium text-up-strong">
          <Check className="h-5 w-5" />
          Check your inbox.
        </div>
        <p className="text-up-strong/80">
          We sent a magic-link sign-in to{' '}
          <span className="font-mono font-medium">{state.email}</span>. The
          link is valid for 15 minutes — clicking it signs you in
          {mode === 'signup' ? ' and creates your account if it doesn’t already exist' : ''}
          .
        </p>
        <p className="text-xs text-up-strong/70">
          Didn&apos;t arrive? Check spam, or{' '}
          <button
            type="button"
            onClick={() => setState({ kind: 'idle' })}
            className="underline hover:no-underline"
          >
            request another
          </button>
          .
        </p>
      </div>
    );
  }

  return (
    <form onSubmit={onSubmit} className="space-y-4">
      <label className="block space-y-1">
        <span className="text-sm font-medium text-ink-body">
          Email
        </span>
        <div className="relative">
          <Mail className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-ink-faint" />
          <input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            autoComplete="email"
            placeholder="you@example.com"
            className="w-full rounded-md border border-line bg-surface py-2 pl-8 pr-3 text-sm placeholder:text-ink-faint focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
          />
        </div>
      </label>

      {state.kind === 'error' && (
        <div className="flex items-start gap-2 rounded-md border border-bad-300 bg-bad-50 p-3 text-sm text-bad-700">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
          <span>{state.message}</span>
        </div>
      )}

      <button
        type="submit"
        disabled={state.kind === 'submitting' || !email.trim()}
        className="inline-flex w-full items-center justify-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-60"
      >
        {state.kind === 'submitting' && <Loader2 className="h-4 w-4 animate-spin" />}
        {mode === 'signup' ? 'Create account' : 'Send magic link'}
      </button>

      <p className="text-xs text-ink-muted">
        Magic-link sign-in — no passwords. The link in the email is
        valid for 15 minutes. New emails create an account on first
        sign-in.
      </p>
    </form>
  );
}
