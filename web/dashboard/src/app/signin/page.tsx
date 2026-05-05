'use client';

import { useState } from 'react';
import { toast } from 'sonner';
import { ApiError, requestMagicLink } from '@/lib/api';

export default function SigninPage() {
  const [email, setEmail] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [sent, setSent] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!email.trim() || submitting) return;
    setSubmitting(true);
    try {
      await requestMagicLink(email.trim().toLowerCase());
      setSent(true);
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? (err.detail ?? err.message)
          : 'Network error — please try again.';
      toast.error(msg);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center px-6">
      <div className="w-full max-w-sm space-y-6 rounded-lg border border-surface-line bg-surface p-8 shadow-sm">
        <div className="space-y-2">
          <h1 className="text-xl font-semibold tracking-tight text-ink">
            Sign in to Rates Engine
          </h1>
          <p className="text-sm text-ink-muted">
            We&apos;ll email you a single-use link. New here? The same link
            creates your account.
          </p>
        </div>

        {sent ? (
          <div className="rounded-md bg-brand-50 p-4 text-sm text-brand-900">
            <p className="font-medium">Check your email.</p>
            <p className="mt-1 text-brand-900/80">
              A sign-in link is on its way to{' '}
              <span className="font-mono text-brand-900">{email}</span>. The
              link expires in 15 minutes.
            </p>
            <button
              onClick={() => {
                setSent(false);
                setEmail('');
              }}
              className="mt-3 text-xs text-brand-600 underline hover:text-brand-900"
            >
              Use a different email
            </button>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <label className="block">
              <span className="mb-1.5 block text-sm font-medium text-ink">
                Email
              </span>
              <input
                type="email"
                autoFocus
                required
                inputMode="email"
                autoComplete="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className="w-full rounded-md border border-surface-line bg-white px-3 py-2 text-sm shadow-sm placeholder:text-ink-faint focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/30"
                placeholder="you@example.com"
              />
            </label>
            <button
              type="submit"
              disabled={submitting}
              className="w-full rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-brand-900 disabled:opacity-60"
            >
              {submitting ? 'Sending…' : 'Email me a sign-in link'}
            </button>
          </form>
        )}

        <p className="text-center text-xs text-ink-faint">
          By signing in you agree to the{' '}
          <a
            href="https://ratesengine.net/terms"
            className="underline hover:text-ink-muted"
          >
            terms of service
          </a>
          .
        </p>
      </div>
    </div>
  );
}
