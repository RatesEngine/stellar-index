import type { Metadata } from 'next';
import Link from 'next/link';
import { AlertTriangle, ArrowRight, ExternalLink, KeyRound, Lock, MessageSquare } from 'lucide-react';
import { GithubIcon } from '@/components/GithubIcon';

export const metadata: Metadata = {
  title: 'Contact — Stellar Index',
  description:
    'How to reach the Stellar Index team. Security disclosures, sales for higher rate-limit tiers, GitHub for general issues, status feed for incident updates.',
  alternates: { canonical: '/contact' },
};

type Channel = {
  icon: React.ComponentType<{ className?: string }>;
  title: string;
  blurb: string;
  destination: string;
  href: string;
  external?: boolean;
};

const CHANNELS: Channel[] = [
  {
    icon: Lock,
    title: 'Security disclosures',
    blurb:
      'Found a vulnerability? Email the security team directly — never open a public issue. We respond within 24 hours and credit the reporter on resolution per the disclosure policy in SECURITY.md.',
    destination: 'security@stellarindex.io',
    href: 'mailto:security@stellarindex.io',
  },
  {
    icon: KeyRound,
    title: 'Sales — Pro / Business / Enterprise',
    blurb:
      'Need higher than the 1,000 req/min Starter rate-limit, named on-call, dedicated regional capacity, or a Slack channel? Email sales with the tier you need (Pro = 10K rpm, Business = 50K rpm, Enterprise = custom) and your traffic profile.',
    destination: 'sales@stellarindex.io',
    href: 'mailto:sales@stellarindex.io',
  },
  {
    icon: GithubIcon,
    title: 'General issues + feature requests',
    blurb:
      "Open a GitHub issue. Bug? Open it under Issues. Feature idea? Open under Discussions. The team triages daily; we don't run a separate support email for general questions.",
    destination: 'github.com/StellarIndex/stellar-index',
    href: 'https://github.com/StellarIndex/stellar-index/issues',
    external: true,
  },
  {
    icon: AlertTriangle,
    title: 'Incident updates',
    blurb:
      'Status page shows live system state. Subscribe to /v1/incidents.atom (Feedly, Slack RSS bot, etc.) for push-style notifications when an incident posts or resolves.',
    destination: 'stellarindex.io/status',
    href: '/status',
    external: true,
  },
  {
    icon: MessageSquare,
    title: 'Architecture + methodology questions',
    blurb:
      "Most of our load-bearing decisions are documented as Architecture Decision Records (ADRs) and per-DEX integration audits. Browse them before reaching out — there's a good chance the rationale is already public.",
    destination: '/research',
    href: '/research',
  },
];

const FAQS: { q: string; a: string }[] = [
  {
    q: 'Is the public tier really free?',
    a: 'Yes. 60 req/min per IP, no auth, no signup. The same data the paid tiers serve, just rate-limited per-IP. Sign up only when you need the higher per-key rate limit and usage analytics.',
  },
  {
    q: 'Do you redistribute paid CEX data?',
    a: 'No. The CEX feeds we ingest (Binance, Coinbase, Kraken, etc.) are read from each venue’s public market-data API. We do not resell tier-restricted feeds. If you need a venue we don’t cover, open a GitHub issue.',
  },
  {
    q: 'Can I self-host this?',
    a: 'The repo is Apache-2.0 — yes. The whole stack runs on one box with Docker Compose; the Makefile has the bring-up. See the archival-node-bringup runbook in /docs/operations for end-to-end setup.',
  },
  {
    q: 'How do I become an early customer / design partner?',
    a: 'Email sales@stellarindex.io with your use-case + scale. Pre-launch we’re lining up a small set of design partners across wallets, fintechs, and on-chain DeFi for the v1 cutover.',
  },
];

export default function ContactPage() {
  return (
    <div className="mx-auto w-full max-w-4xl px-6 py-12 sm:py-16">
      <header className="mb-10 space-y-3">
        <p className="font-mono text-xs uppercase tracking-widest text-brand-600">
          Get in touch
        </p>
        <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">
          Contact
        </h1>
        <p className="max-w-2xl text-base text-ink-body">
          We don&apos;t run a support inbox for the public tier — issues land on
          GitHub, sales go to email, and security goes to a separate inbox
          with a real disclosure SLA. Pick the channel that fits your
          message.
        </p>
      </header>

      <section className="space-y-3">
        {CHANNELS.map((c) => (
          <ChannelCard key={c.title} channel={c} />
        ))}
      </section>

      <section className="mt-12 space-y-4">
        <h2 className="text-xl font-semibold tracking-tight">FAQ</h2>
        <div className="space-y-3">
          {FAQS.map((f) => (
            <details
              key={f.q}
              className="group rounded-xl border border-line bg-surface p-4 open:shadow-sm"
            >
              <summary className="flex cursor-pointer items-center justify-between text-sm font-medium">
                {f.q}
                <span className="text-ink-faint group-open:rotate-180">
                  <ArrowRight className="h-3.5 w-3.5 -rotate-90 transition" />
                </span>
              </summary>
              <p className="mt-3 text-sm text-ink-body">
                {f.a}
              </p>
            </details>
          ))}
        </div>
      </section>

      <section className="mt-12 rounded-xl border border-line bg-surface p-5 text-sm text-ink-body">
        <p>
          Want an API key? Sign in with magic-link at{' '}
          <Link href="/signin" className="text-brand-600 hover:underline">
            /signin
          </Link>
          {' '}(no password) and mint one from{' '}
          <Link href="/dashboard" className="text-brand-600 hover:underline">
            /account
          </Link>
          . The same page shows your tier, 30d usage history, and per-key
          revoke buttons for rotation.
        </p>
      </section>
    </div>
  );
}

function ChannelCard({ channel }: { channel: Channel }) {
  const Icon = channel.icon;
  const isInternal = !channel.external;
  const inner = (
    <div className="flex items-start gap-4 rounded-xl border border-line bg-surface p-5 transition hover:border-brand-300 hover:shadow-sm">
      <div className="rounded-lg bg-surface-subtle p-2 text-brand-600">
        <Icon className="h-4 w-4" />
      </div>
      <div className="flex-1 space-y-1.5">
        <div className="flex items-baseline gap-2">
          <h3 className="text-sm font-semibold">{channel.title}</h3>
          <code className="font-mono text-xs text-ink-muted">
            {channel.destination}
          </code>
          {channel.external && (
            <ExternalLink className="h-3 w-3 text-ink-faint" />
          )}
        </div>
        <p className="text-sm text-ink-body">
          {channel.blurb}
        </p>
      </div>
    </div>
  );
  if (isInternal && channel.href.startsWith('/')) {
    return <Link href={channel.href}>{inner}</Link>;
  }
  return (
    <a
      href={channel.href}
      target={channel.external ? '_blank' : undefined}
      rel={channel.external ? 'noreferrer noopener' : undefined}
    >
      {inner}
    </a>
  );
}
