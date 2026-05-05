import type { Metadata } from 'next';

import { AccountDashboard } from './AccountDashboard';

export const metadata: Metadata = {
  title: 'Account — Rates Engine',
  description:
    'View your API keys, current tier, and rate-limit budget. Paste your key to log in client-side.',
};

/**
 * /account — customer-facing dashboard. Paste the API key once
 * (kept in localStorage for the session, never POSTed back to
 * a server we control), see every key on the account, the active
 * tier, and the rate-limit budget. Mint additional keys (rotation)
 * + delete keys (when revocation lands) live here too.
 *
 * Distinct from /signup (one-shot mint) and /status (system health).
 * /account is "I'm an existing customer who wants to manage my
 * credentials."
 *
 * Pure client-side wiring against the public API — no Next.js
 * server-side rendering of customer keys (the static-export build
 * couldn't keep them secret anyway). The page is a static shell;
 * the AccountDashboard component does all the fetching from the
 * caller's browser.
 */
export default function AccountPage() {
  return (
    <div className="mx-auto w-full max-w-4xl px-4 py-12 sm:px-6 sm:py-16">
      <header className="mb-8">
        <h1 className="text-3xl font-bold tracking-tight text-slate-900 dark:text-slate-100 sm:text-4xl">
          Your account
        </h1>
        <p className="mt-3 max-w-2xl text-base text-slate-600 dark:text-slate-400">
          Paste your API key to see every key on the account, your
          active tier, and your rate-limit budget. The key is kept in{' '}
          <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs dark:bg-slate-800">
            localStorage
          </code>{' '}
          for this browser session — we never persist it on a server
          we control.
        </p>
      </header>

      <AccountDashboard />
    </div>
  );
}
