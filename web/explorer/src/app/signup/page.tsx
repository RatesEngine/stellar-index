import type { Metadata } from 'next';
import Link from 'next/link';

import { API_BASE_URL } from '@/api/client';

import { SignInForm } from '../signin/SignInForm';

export const metadata: Metadata = {
  // Auth form — no SEO value; keep it out of the index (and the sitemap).
  robots: { index: false, follow: false },
  title: 'Create account — Stellar Index',
  description:
    'Create your Stellar Index account. Magic-link email auth — no passwords. Free tier included; paid plans unlock higher rate limits + dedicated SLAs.',
};

const TIERS = [
  {
    name: 'Free',
    rateLimit: '60 req/min per IP',
    cost: '$0',
    notes: 'Public read of every endpoint. No account required for the anonymous tier.',
  },
  {
    name: 'Starter',
    rateLimit: '1,000 req/min per key',
    cost: '$0',
    notes: 'Self-service. Sign in with the form on the right; first sign-in creates the account. API keys live under your account once you’re in.',
    highlight: true,
  },
  {
    name: 'Pro',
    rateLimit: '10,000 req/min per key',
    cost: 'Contact sales',
    notes: 'For wallets + portfolio apps with heavy fan-out. Includes Slack channel + 24h SLA on incident response.',
  },
  {
    name: 'Business',
    rateLimit: '50,000 req/min per key',
    cost: 'Contact sales',
    notes: 'For exchanges + market-data redistributors. Dedicated AlertManager routes + named on-call + 1h SLA on SEV-1.',
  },
  {
    name: 'Enterprise',
    rateLimit: 'Custom',
    cost: 'Custom',
    notes: 'Bespoke shape. Multi-tenant key isolation, custom retention, dedicated regional capacity, named TAM. Talk to us.',
  },
];

export default function SignupPage() {
  return (
    <div className="mx-auto w-full max-w-4xl px-4 py-12 sm:px-6 sm:py-16">
      <header className="mb-10">
        <h1 className="text-3xl font-bold tracking-tight text-ink sm:text-4xl">
          Create your account
        </h1>
        <p className="mt-3 max-w-2xl text-base text-ink-body">
          Magic-link sign-in — no passwords. Once you&apos;re in, mint
          API keys, watch usage, and manage billing under your account.
          The free tier covers most prototyping; paid plans unlock
          higher per-key rate limits + dedicated SLAs.
        </p>
      </header>

      <section className="mb-12 rounded-xl border border-line bg-surface p-6 shadow-sm sm:p-8">
        <SignInForm mode="signup" />
        <p className="mt-4 text-xs text-ink-muted">
          Already have an account?{' '}
          <Link href="/signin" className="text-brand-600 hover:underline">
            Sign in
          </Link>{' '}
          — same magic-link form, just lands on your existing account.
        </p>
      </section>

      <section className="mb-12">
        <h2 className="mb-4 text-xl font-semibold text-ink">
          Tiers
        </h2>
        <div className="overflow-hidden rounded-xl border border-line">
          <table className="min-w-full divide-y divide-line">
            <thead className="bg-surface-muted">
              <tr>
                <th scope="col" className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-ink-body">
                  Tier
                </th>
                <th scope="col" className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-ink-body">
                  Rate limit
                </th>
                <th scope="col" className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-ink-body">
                  Cost
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line bg-surface">
              {TIERS.map((tier) => (
                <tr key={tier.name} className={tier.highlight ? 'bg-brand-50' : ''}>
                  <td className="whitespace-nowrap px-4 py-3 text-sm font-semibold text-ink">
                    {tier.name}
                    {tier.highlight && (
                      <span className="ml-2 inline-flex items-center rounded-full bg-brand-600 px-2 py-0.5 text-xs font-medium text-white">
                        you are here
                      </span>
                    )}
                  </td>
                  <td className="whitespace-nowrap px-4 py-3 text-sm text-ink-body">
                    {tier.rateLimit}
                  </td>
                  <td className="whitespace-nowrap px-4 py-3 text-sm text-ink-body">
                    {tier.cost === 'Contact sales' || tier.cost === 'Custom' ? (
                      <Link
                        href="/contact"
                        className="text-brand-600 hover:underline"
                      >
                        {tier.cost} →
                      </Link>
                    ) : (
                      tier.cost
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <ul className="mt-4 space-y-2 text-sm text-ink-body">
          {TIERS.map((tier) => (
            <li key={tier.name}>
              <strong className="text-ink">{tier.name}.</strong>{' '}
              {tier.notes}
            </li>
          ))}
        </ul>
      </section>

      <section className="rounded-xl border border-warn-300 bg-warn-50 p-5 text-sm text-warn-700">
        <strong>Anonymous reads work without an account.</strong>{' '}
        If you&rsquo;re prototyping, just hit{' '}
        <a href="https://docs.stellarindex.io" className="underline">
          the public endpoints
        </a>{' '}
        directly — the 60 req/min IP-floor covers exploratory scripts
        and low-traffic embeds. Create an account when you want the
        higher per-key rate-limit + usage analytics, or when
        you&rsquo;re ready to ship to customers.
      </section>

      <p className="mt-8 text-xs text-ink-muted">
        API base URL: <code className="font-mono">{API_BASE_URL}</code>
      </p>
    </div>
  );
}
