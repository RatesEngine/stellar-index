import type { Metadata } from 'next';

import { AccountDashboard } from './AccountDashboard';

export const metadata: Metadata = {
  title: 'Account — Rates Engine',
  description:
    'Manage your Rates Engine account, API keys, and rate-limit budget. Magic-link sign-in — no passwords.',
};

/**
 * /account — customer dashboard. Reads the magic-link session
 * cookie set by /v1/auth/callback. Anonymous visitors see a
 * "sign in" prompt linking to /signin (which kicks off the
 * magic-link email).
 *
 * Pure client-side wiring against the public API. The static
 * shell is pre-rendered; AccountDashboard handles auth state +
 * data fetching with `credentials: 'include'`.
 */
export default function AccountPage() {
  return (
    <div className="mx-auto w-full max-w-4xl px-4 py-12 sm:px-6 sm:py-16">
      <header className="mb-8">
        <h1 className="text-3xl font-bold tracking-tight text-slate-900 dark:text-slate-100 sm:text-4xl">
          Your account
        </h1>
        <p className="mt-3 max-w-2xl text-base text-slate-600 dark:text-slate-400">
          See your tier, manage API keys, watch usage. Magic-link
          sign-in — no passwords.
        </p>
      </header>

      <AccountDashboard />
    </div>
  );
}
