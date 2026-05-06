import type { Metadata } from 'next';

import { API_BASE_URL } from '@/api/client';

import { SignupForm } from './SignupForm';

export const metadata: Metadata = {
  title: 'Sign up — Rates Engine',
  description:
    'Get a free API key for the Rates Engine. 1000 requests per minute, no credit card required, instant.',
};

const TIERS = [
  {
    name: 'Free',
    rateLimit: '60 req/min per IP',
    cost: '$0',
    auth: 'No key required',
    notes: 'Public read of every endpoint. Perfect for casual integrations + initial exploration. Same data as paid tiers — just rate-limited.',
  },
  {
    name: 'Starter',
    rateLimit: '1,000 req/min per key',
    cost: '$0',
    auth: 'POST /v1/signup',
    notes: 'Self-service. The form below mints your key. Same data; higher per-key rate-limit; usage analytics + key rotation via /v1/account/*.',
    highlight: true,
  },
  {
    name: 'Pro',
    rateLimit: '10,000 req/min per key',
    cost: 'Contact sales',
    auth: 'Operator-issued',
    notes: 'For wallets + portfolio apps with heavy fan-out. Includes Slack channel + 24h SLA on incident response.',
  },
  {
    name: 'Business',
    rateLimit: '50,000 req/min per key',
    cost: 'Contact sales',
    auth: 'Operator-issued',
    notes: 'For exchanges + market-data redistributors. Includes dedicated AlertManager routes + named on-call engineer + 1h SLA on SEV-1.',
  },
  {
    name: 'Enterprise',
    rateLimit: 'Custom',
    cost: 'Custom',
    auth: 'SEP-10 + per-tenant',
    notes: 'Bespoke shape. Multi-tenant key isolation, custom retention, dedicated regional capacity, named TAM. Talk to us.',
  },
];

export default function SignupPage() {
  return (
    <div className="mx-auto w-full max-w-4xl px-4 py-12 sm:px-6 sm:py-16">
      <header className="mb-10">
        <h1 className="text-3xl font-bold tracking-tight text-slate-900 dark:text-slate-100 sm:text-4xl">
          Get a free API key
        </h1>
        <p className="mt-3 max-w-2xl text-base text-slate-600 dark:text-slate-400">
          1,000 requests per minute, no credit card, the key is yours
          in under a second. The same surface as the paid tiers — you
          just get the higher rate-limit and per-key usage analytics.
        </p>
      </header>

      <section className="mb-12 rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900 sm:p-8">
        <SignupForm />
      </section>

      <section className="mb-12">
        <h2 className="mb-4 text-xl font-semibold text-slate-900 dark:text-slate-100">
          Tiers
        </h2>
        <div className="overflow-hidden rounded-xl border border-slate-200 dark:border-slate-800">
          <table className="min-w-full divide-y divide-slate-200 dark:divide-slate-800">
            <thead className="bg-slate-50 dark:bg-slate-800/50">
              <tr>
                <th scope="col" className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
                  Tier
                </th>
                <th scope="col" className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
                  Rate limit
                </th>
                <th scope="col" className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
                  Cost
                </th>
                <th scope="col" className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-slate-600 dark:text-slate-400">
                  Get one
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-200 bg-white dark:divide-slate-800 dark:bg-slate-900">
              {TIERS.map((tier) => (
                <tr key={tier.name} className={tier.highlight ? 'bg-brand-50 dark:bg-brand-900/20' : ''}>
                  <td className="whitespace-nowrap px-4 py-3 text-sm font-semibold text-slate-900 dark:text-slate-100">
                    {tier.name}
                    {tier.highlight && (
                      <span className="ml-2 inline-flex items-center rounded-full bg-brand-600 px-2 py-0.5 text-xs font-medium text-white">
                        you are here
                      </span>
                    )}
                  </td>
                  <td className="whitespace-nowrap px-4 py-3 text-sm text-slate-700 dark:text-slate-300">
                    {tier.rateLimit}
                  </td>
                  <td className="whitespace-nowrap px-4 py-3 text-sm text-slate-700 dark:text-slate-300">
                    {tier.cost}
                  </td>
                  <td className="px-4 py-3 text-sm text-slate-700 dark:text-slate-300">
                    {tier.auth}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <ul className="mt-4 space-y-2 text-sm text-slate-600 dark:text-slate-400">
          {TIERS.map((tier) => (
            <li key={tier.name}>
              <strong className="text-slate-900 dark:text-slate-100">{tier.name}.</strong>{' '}
              {tier.notes}
            </li>
          ))}
        </ul>
      </section>

      <section className="mb-12">
        <h2 className="mb-4 text-xl font-semibold text-slate-900 dark:text-slate-100">
          Already have a key?
        </h2>
        <ul className="space-y-2 text-sm text-slate-600 dark:text-slate-400">
          <li>
            <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs dark:bg-slate-800">
              GET /v1/account/me
            </code>{' '}
            — your account info (key_id, tier, created_at).
          </li>
          <li>
            <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs dark:bg-slate-800">
              POST /v1/account/keys
            </code>{' '}
            — mint another key under the same account (e.g. for a
            second deployment, or to rotate).
          </li>
          <li>
            <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs dark:bg-slate-800">
              GET /v1/account/usage
            </code>{' '}
            — daily request counts (rolled-up reporting is a
            post-launch enhancement; currently returns an empty
            array under the locked wire shape).
          </li>
        </ul>
      </section>

      <section className="rounded-xl border border-amber-200 bg-amber-50 p-5 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-900/20 dark:text-amber-200">
        <strong>Free tier already gets you most of what you need.</strong>{' '}
        If you&rsquo;re prototyping, just hit{' '}
        <a href="https://docs.ratesengine.net" className="underline">
          the public endpoints
        </a>{' '}
        directly — no signup required. The 60 req/min anonymous floor
        works for an exploratory script or a low-traffic embed. Sign
        up when you want the higher per-key rate-limit + usage
        analytics, or when you&rsquo;re ready to ship to customers.
      </section>

      <p className="mt-8 text-xs text-slate-500 dark:text-slate-500">
        API base URL: <code className="font-mono">{API_BASE_URL}</code>
      </p>
    </div>
  );
}
