import type { Metadata } from 'next';
import Link from 'next/link';
import { Check, Minus } from 'lucide-react';

import {
  Badge,
  ButtonLink,
  Card,
  CardBody,
  Container,
} from '@/components/ui';

export const metadata: Metadata = {
  title: 'Pricing — plans, quotas, SLAs',
  description:
    'Stellar Index pricing tiers. Free anonymous reads, $0 self-service Starter for higher per-key rate limits, Pro/Business/Enterprise for production volume. Same data on every tier.',
  alternates: { canonical: '/pricing' },
};

interface Tier {
  name: string;
  price: string;
  priceSubtitle?: string;
  rateLimit: string;
  cta: { label: string; href: string };
  highlight?: boolean;
  description: string;
  features: string[];
  notFeatures?: string[];
}

const TIERS: Tier[] = [
  {
    name: 'Free',
    price: '$0',
    priceSubtitle: 'forever',
    rateLimit: '60 req/min per IP',
    cta: { label: 'Read the docs', href: 'https://docs.stellarindex.io' },
    description:
      'Anonymous public reads. Same data as the paid tiers, just rate-limited per IP. Perfect for prototyping, low-traffic embeds, and read-only integrations.',
    features: [
      'Every public endpoint',
      'No signup, no API key, no auth',
      'Same VWAP / freshness as paid tiers',
      '60 requests / minute per source IP',
    ],
    notFeatures: ['Per-key analytics', 'Higher per-key rate limit', 'Dedicated SLA'],
  },
  {
    name: 'Starter',
    price: '$0',
    priceSubtitle: 'self-service',
    rateLimit: '1,000 req/min per key',
    cta: { label: 'Create account', href: '/signup' },
    description:
      'Sign in with magic-link, mint an API key, get 1,000 req/min and per-key usage analytics. Designed for individual builders and side-projects shipping to customers.',
    highlight: true,
    features: [
      'Everything in Free',
      '1,000 requests / minute per key',
      'Per-key usage history (30d)',
      'Mint & rotate keys at /account',
      'Email support',
    ],
    notFeatures: ['Dedicated SLA', 'Operator-issued tier overrides'],
  },
  {
    name: 'Pro',
    price: 'Contact',
    priceSubtitle: 'sales',
    rateLimit: '10,000 req/min per key',
    cta: { label: 'Talk to sales', href: '/contact' },
    description:
      'For wallets and portfolio apps with heavy fan-out. Includes a Slack channel and 24h SLA on incident response.',
    features: [
      'Everything in Starter',
      '10,000 requests / minute per key',
      'Shared-channel Slack support',
      '24h SLA on incident response',
      'Operator-issued tier override',
    ],
  },
  {
    name: 'Business',
    price: 'Contact',
    priceSubtitle: 'sales',
    rateLimit: '50,000 req/min per key',
    cta: { label: 'Talk to sales', href: '/contact' },
    description:
      'For exchanges and market-data redistributors. Dedicated AlertManager routes, named on-call engineer, 1h SLA on SEV-1.',
    features: [
      'Everything in Pro',
      '50,000 requests / minute per key',
      'Dedicated AlertManager routes',
      'Named on-call engineer',
      '1h SLA on SEV-1 incidents',
    ],
  },
  {
    name: 'Enterprise',
    price: 'Custom',
    priceSubtitle: '',
    rateLimit: 'Custom',
    cta: { label: 'Talk to sales', href: '/contact' },
    description:
      'Multi-tenant key isolation, custom retention, dedicated regional capacity, named TAM. For market-makers and pricing-redistributor partners.',
    features: [
      'Everything in Business',
      'Multi-tenant key isolation',
      'Custom retention windows',
      'Dedicated regional capacity (R1/R2/R3)',
      'Named technical account manager',
      'Bespoke SLA',
    ],
  },
];

export default function PricingPage() {
  return (
    <Container className="space-y-12 py-10 sm:py-14">
      <header className="mx-auto max-w-2xl space-y-4 text-center">
        <p className="text-xs font-medium uppercase tracking-wider text-brand-600">
          Plans
        </p>
        <h1 className="text-h1 font-semibold text-ink md:text-display-sm">
          Pricing
        </h1>
        <p className="text-lg leading-relaxed text-ink-muted">
          Same data on every tier. Free reads work without an account; paid
          plans unlock higher per-key rate limits, usage analytics, and
          dedicated SLAs.
        </p>
      </header>

      <div className="grid grid-cols-1 gap-5 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
        {TIERS.map((tier) => (
          <TierCard key={tier.name} tier={tier} />
        ))}
      </div>

      <Card className="p-6 sm:p-8">
        <h2 className="text-h3 font-semibold text-ink">Honest notes</h2>
        <ul className="mt-4 space-y-2.5 text-sm text-ink-body">
          <li>
            <strong className="text-ink-body">
              Free is not a trial.
            </strong>{' '}
            Anonymous reads are a permanent commitment — public-tier
            access is what the SDF / Freighter RFP asked for and what
            we shipped against.
          </li>
          <li>
            <strong className="text-ink-body">
              Same data, every tier.
            </strong>{' '}
            We do not gate endpoints, freshness, or precision behind
            paid tiers. The difference is rate limit, support model,
            and SLA — never the data itself.
          </li>
          <li>
            <strong className="text-ink-body">
              No card to sign up Starter.
            </strong>{' '}
            Magic-link account, mint a key, ship. Rate-limit upgrades
            beyond Starter route through{' '}
            <Link href="/contact" className="text-brand-600 hover:underline">
              /contact
            </Link>
            .
          </li>
          <li>
            <strong className="text-ink-body">
              Pricing is pre-v1.
            </strong>{' '}
            We&apos;re shipping the paid surface alongside v1. Concrete
            numbers for Pro / Business will land here once the
            commercial paperwork is ready.
          </li>
        </ul>
      </Card>
    </Container>
  );
}

function TierCard({ tier }: { tier: Tier }) {
  const ctaVariant = tier.highlight ? 'primary' : 'secondary';
  return (
    <Card className={tier.highlight ? 'ring-1 ring-brand-500/40' : undefined}>
      <CardBody className="flex h-full flex-col">
        <div className="mb-3 flex items-center justify-between gap-2">
          <h3 className="text-h3 font-semibold text-ink">{tier.name}</h3>
          {tier.highlight && <Badge tone="brand">Self-service</Badge>}
        </div>
        <div>
          <div className="font-mono text-3xl font-semibold tnum text-ink">
            {tier.price}
          </div>
          {tier.priceSubtitle && (
            <div className="text-xs text-ink-muted">{tier.priceSubtitle}</div>
          )}
        </div>
        <div className="mt-3 rounded-md bg-surface-muted px-3 py-2 font-mono text-xs text-ink-body">
          {tier.rateLimit}
        </div>
        <p className="mt-3 text-sm text-ink-muted">{tier.description}</p>

        <ul className="mt-4 space-y-1.5 text-sm">
          {tier.features.map((f) => (
            <li key={f} className="flex items-start gap-2 text-ink-body">
              <Check className="mt-0.5 h-4 w-4 shrink-0 text-up" />
              <span>{f}</span>
            </li>
          ))}
          {tier.notFeatures?.map((f) => (
            <li key={f} className="flex items-start gap-2 text-ink-faint">
              <Minus className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{f}</span>
            </li>
          ))}
        </ul>

        <div className="mt-auto pt-5">
          {tier.cta.href.startsWith('http') ? (
            <ButtonLink
              href={tier.cta.href}
              target="_blank"
              rel="noreferrer noopener"
              variant={ctaVariant}
              className="w-full"
            >
              {tier.cta.label}
            </ButtonLink>
          ) : (
            <ButtonLink
              href={tier.cta.href}
              variant={ctaVariant}
              className="w-full"
            >
              {tier.cta.label}
            </ButtonLink>
          )}
        </div>
      </CardBody>
    </Card>
  );
}
