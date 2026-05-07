import type { Metadata } from 'next';
import Link from 'next/link';
import { ArrowRight, Github, Mail } from 'lucide-react';

export const metadata: Metadata = {
  title: 'Careers — work on Stellar pricing infrastructure',
  description:
    'Roles at Rates Engine — Apache-2.0 codebase, real on-chain pricing, no AI-slop shortcuts. No open roles listed today; contributions via GitHub PRs welcome.',
};

const VALUES = [
  {
    title: 'No mock data, ever.',
    body: 'Every panel on every page reads from a real endpoint or shows an honest "we don\'t have this yet" placeholder. We do not ship UI built on simulated numbers.',
  },
  {
    title: 'Engineering decisions in the open.',
    body: 'Architecture decisions are documented as ADRs. Per-source integrations have public discovery audits. The launch-readiness backlog lives in the repo, not a private Notion.',
  },
  {
    title: 'Production-grade from day one.',
    body: '3-region active-active, full validators per region, pre-push verify gate, OpenAPI as the source of truth. No "we\'ll harden it later" debt.',
  },
  {
    title: 'Public tier is permanent.',
    body: "We never gate data behind paid tiers. The free anonymous read budget exists in perpetuity; commercial revenue funds the operator team and infrastructure, not a paywall.",
  },
];

const CONTRIBUTING_PATHS = [
  {
    label: 'Decoder for a new on-chain DEX',
    href: 'https://github.com/RatesEngine/rates-engine/blob/main/CLAUDE.md#add-a-new-on-chain-soroban-dex',
    description: 'Five-file convention: README + events + decode + consumer + tests. Templates exist for Soroswap / Phoenix / Aquarius / Comet.',
  },
  {
    label: 'CEX connector',
    href: 'https://github.com/RatesEngine/rates-engine/blob/main/CLAUDE.md#add-a-new-cex-connector',
    description: 'WebSocket / REST poller against vendor APIs. Same five-file convention; reference implementations for Binance / Coinbase / Kraken / Bitstamp.',
  },
  {
    label: 'Supply observer',
    href: 'https://github.com/RatesEngine/rates-engine/blob/main/CLAUDE.md#add-a-new-supply-observer',
    description: 'Per-domain observer (Algorithm 1 XLM / Algorithm 2 classic / Algorithm 3 SEP-41). Plug into the dispatcher hook matching what the source emits.',
  },
  {
    label: 'Documentation + ADRs',
    href: 'https://github.com/RatesEngine/rates-engine/tree/main/docs',
    description: 'Architecture narratives, runbooks, integration audits. Every PR description in this repo follows the same shape — pick one and write the next.',
  },
];

export default function CareersPage() {
  return (
    <div className="mx-auto max-w-3xl space-y-12 px-6 py-12">
      <header className="space-y-3">
        <p className="font-mono text-xs uppercase tracking-widest text-brand-600 dark:text-brand-400">
          Careers
        </p>
        <h1 className="text-4xl font-semibold tracking-tight">
          Work on real pricing infrastructure.
        </h1>
        <p className="text-base text-slate-600 dark:text-slate-400">
          We&apos;re a small team shipping the v1 platform. The
          codebase is Apache-2.0, the architecture is public, and
          every PR ships against the same verify-gate the operator
          runs before deploy.
        </p>
      </header>

      <section className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-lg font-semibold">Open roles</h2>
        <p className="mt-3 text-sm text-slate-600 dark:text-slate-400">
          No open roles listed today. We&apos;re focused on shipping
          v1 and the post-launch backlog with the existing team. When
          we open a role it&apos;ll appear here with a job description
          and an application path.
        </p>
        <p className="mt-3 text-sm text-slate-600 dark:text-slate-400">
          Want to be considered when we do open one? Drop a note via{' '}
          <Link href="/contact" className="text-brand-600 hover:underline">
            /contact
          </Link>{' '}
          with a link to public work — we hire from open-source
          contributions.
        </p>
      </section>

      <section className="space-y-4">
        <h2 className="text-2xl font-semibold tracking-tight">How we work</h2>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          {VALUES.map((v) => (
            <div
              key={v.title}
              className="rounded-xl border border-slate-200 bg-white p-5 dark:border-slate-800 dark:bg-slate-900"
            >
              <h3 className="text-sm font-semibold text-slate-900 dark:text-slate-100">
                {v.title}
              </h3>
              <p className="mt-2 text-sm text-slate-600 dark:text-slate-400">
                {v.body}
              </p>
            </div>
          ))}
        </div>
      </section>

      <section className="space-y-4">
        <div className="space-y-2">
          <h2 className="text-2xl font-semibold tracking-tight">
            Contribute via PRs
          </h2>
          <p className="text-sm text-slate-600 dark:text-slate-400">
            The fastest path to working on this codebase is to land a
            PR. Apache-2.0 means you don&apos;t need our permission to
            fork, build, or run your own copy. Four common starting
            points:
          </p>
        </div>
        <div className="space-y-3">
          {CONTRIBUTING_PATHS.map((p) => (
            <a
              key={p.href}
              href={p.href}
              target="_blank"
              rel="noreferrer noopener"
              className="block rounded-lg border border-slate-200 bg-white p-4 transition-colors hover:border-brand-500 dark:border-slate-800 dark:bg-slate-900"
            >
              <div className="flex items-start justify-between gap-2">
                <h3 className="text-sm font-semibold text-slate-900 dark:text-slate-100">
                  {p.label}
                </h3>
                <ArrowRight className="h-4 w-4 shrink-0 text-slate-400" />
              </div>
              <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
                {p.description}
              </p>
            </a>
          ))}
        </div>
      </section>

      <section className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-lg font-semibold">Get in touch</h2>
        <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Link
            href="/contact"
            className="inline-flex items-center justify-center gap-2 rounded-md border border-slate-200 px-3 py-2 text-sm hover:border-brand-500 hover:text-brand-600 dark:border-slate-700"
          >
            <Mail className="h-4 w-4" />
            Contact
          </Link>
          <a
            href="https://github.com/RatesEngine/rates-engine"
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex items-center justify-center gap-2 rounded-md border border-slate-200 px-3 py-2 text-sm hover:border-brand-500 hover:text-brand-600 dark:border-slate-700"
          >
            <Github className="h-4 w-4" />
            GitHub
          </a>
        </div>
      </section>
    </div>
  );
}
