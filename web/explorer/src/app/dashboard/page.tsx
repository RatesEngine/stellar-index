'use client';

import {
  Activity,
  ArrowUpRight,
  BarChart3,
  BookOpen,
  CheckCircle2,
  Circle,
  KeyRound,
  Plus,
  ShieldCheck,
} from 'lucide-react';
import Link from 'next/link';
import { type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';

import { ApiError, listKeys, type APIKey } from '@/api/account';
import type { MeResponse } from '@/api/hooks';
import {
  Badge,
  ButtonLink,
  Callout,
  Card,
  CardBody,
  CardHeader,
  Container,
  Mono,
  PageHeader,
  Section,
  Skeleton,
  Stat,
  StatCell,
  StatGrid,
} from '@/components/ui';
import {
  fmtInt,
  fmtRelative,
  tierCeiling,
  tierLabel,
} from '@/lib/account-format';

import { AccountGate } from './AccountGate';

const DOCS_URL = 'https://docs.stellarindex.io';

/**
 * /dashboard — the in-site customer dashboard landing (Overview). Ported
 * from the standalone dashboard's overview: a metric strip, a
 * getting-started checklist, the current plan, and quick links. Gated
 * on the magic-link session via AccountGate (reuses useMe); the
 * ConsoleShell sidebar already provides the surrounding chrome.
 */
export default function AccountOverviewPage() {
  return <AccountGate>{(me) => <OverviewBody me={me} />}</AccountGate>;
}

function OverviewBody({ me }: { me: MeResponse }) {
  const keysQuery = useQuery<APIKey[], Error>({
    queryKey: ['dashboard', 'keys'],
    queryFn: ({ signal }) => listKeys(signal),
  });
  const keys = keysQuery.data ?? null;
  const error = keysQuery.error
    ? keysQuery.error instanceof ApiError
      ? (keysQuery.error.detail ?? keysQuery.error.message)
      : 'Failed to load keys'
    : null;

  return (
    <Container>
      <Section className="space-y-8">
        <PageHeader
          eyebrow="Account"
          title={`Welcome back, ${firstName(me)}`}
          description="Your account at a glance — keys, plan, and where to go next."
          actions={
            <ButtonLink href="/dashboard/keys" variant="primary">
              <Plus className="h-4 w-4" />
              New API key
            </ButtonLink>
          }
        />

        {error && (
          <Callout tone="bad" title="Couldn't load your keys">
            {error}
          </Callout>
        )}

        <MetricStrip me={me} keys={keys} />

        <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
          <div className="lg:col-span-2">
            <GettingStarted me={me} keys={keys} />
          </div>
          <div className="space-y-6">
            <PlanCard me={me} />
            <QuickLinks />
          </div>
        </div>
      </Section>
    </Container>
  );
}

function MetricStrip({ me, keys }: { me: MeResponse; keys: APIKey[] | null }) {
  if (keys === null) {
    return (
      <StatGrid cols={4}>
        {Array.from({ length: 4 }).map((_, i) => (
          <StatCell key={i}>
            <Skeleton className="h-3 w-24" />
            <Skeleton className="mt-2 h-8 w-16" />
          </StatCell>
        ))}
      </StatGrid>
    );
  }

  const tier = accountTier(me);
  const status = me.account?.status;
  const active = keys.filter((k) => !k.revoked_at);
  const lastUsedAt = keys
    .map((k) => k.last_used_at)
    .filter((d): d is string => Boolean(d))
    .sort()
    .at(-1);
  const ceiling = tierCeiling(tier);

  return (
    <StatGrid cols={4}>
      <StatCell>
        <Stat
          icon={<KeyRound className="h-3.5 w-3.5" />}
          label="Active keys"
          value={fmtInt(active.length)}
          sub={
            keys.length > active.length
              ? `${fmtInt(keys.length - active.length)} revoked`
              : 'all active'
          }
        />
      </StatCell>
      <StatCell>
        <Stat
          icon={<ShieldCheck className="h-3.5 w-3.5" />}
          label="Plan"
          value={tierLabel(tier)}
          sub={status === 'active' ? 'active' : (status ?? '—')}
        />
      </StatCell>
      <StatCell>
        <Stat
          icon={<Activity className="h-3.5 w-3.5" />}
          label="Rate limit"
          value={ceiling !== null ? fmtInt(ceiling) : '—'}
          sub="requests / min"
        />
      </StatCell>
      <StatCell>
        <Stat
          icon={<BarChart3 className="h-3.5 w-3.5" />}
          label="Last request"
          value={lastUsedAt ? fmtRelative(lastUsedAt) : 'No traffic yet'}
          sub={lastUsedAt ? 'across all keys' : 'mint a key to begin'}
        />
      </StatCell>
    </StatGrid>
  );
}

function GettingStarted({
  me,
  keys,
}: {
  me: MeResponse;
  keys: APIKey[] | null;
}) {
  const hasKey = keys !== null && keys.some((k) => !k.revoked_at);
  const hasTraffic = keys !== null && keys.some((k) => Boolean(k.last_used_at));
  const email = me.user?.email;

  const steps: { done: boolean; title: string; body: ReactNode }[] = [
    {
      done: true,
      title: 'Create your account',
      body: email ? (
        <>
          Signed in as{' '}
          <Mono value={email} copy={false} className="text-[13px]" />
        </>
      ) : (
        'Your account is active.'
      ),
    },
    {
      done: hasKey,
      title: 'Mint an API key',
      body: hasKey ? (
        'You have at least one active key.'
      ) : (
        <>
          Keys authenticate your requests to{' '}
          <code className="rounded-sm bg-surface-subtle px-1 py-0.5 font-mono text-[12px]">
            api.stellarindex.io
          </code>
          .{' '}
          <Link
            href="/dashboard/keys"
            className="font-medium text-brand-700 hover:underline"
          >
            Create one →
          </Link>
        </>
      ),
    },
    {
      done: hasTraffic,
      title: 'Make your first request',
      body: hasTraffic ? (
        'We have seen traffic on your keys.'
      ) : (
        <>
          Try the pricing API:{' '}
          <code className="rounded-sm bg-surface-subtle px-1 py-0.5 font-mono text-[12px]">
            GET /v1/price/XLM-USD
          </code>{' '}
          with your{' '}
          <code className="rounded-sm bg-surface-subtle px-1 py-0.5 font-mono text-[12px]">
            X-API-Key
          </code>{' '}
          header.
        </>
      ),
    },
    {
      done: false,
      title: 'Explore the docs',
      body: (
        <>
          Endpoints, rate limits, and SSE streams are in the{' '}
          <a
            href={DOCS_URL}
            target="_blank"
            rel="noopener noreferrer"
            className="font-medium text-brand-700 hover:underline"
          >
            API reference
          </a>
          .
        </>
      ),
    },
  ];

  return (
    <Card>
      <CardHeader
        title="Getting started"
        description="A few steps to get your integration live."
      />
      <CardBody className="space-y-1">
        {keys === null ? (
          <div className="space-y-4 py-2">
            {Array.from({ length: 3 }).map((_, i) => (
              <div key={i} className="flex gap-3">
                <Skeleton className="h-5 w-5 rounded-full" />
                <div className="flex-1 space-y-1.5">
                  <Skeleton className="h-4 w-40" />
                  <Skeleton className="h-3 w-64" />
                </div>
              </div>
            ))}
          </div>
        ) : (
          <ol className="divide-y divide-line">
            {steps.map((s, i) => (
              <li
                key={i}
                className="flex items-start gap-3 py-3 first:pt-1 last:pb-1"
              >
                {s.done ? (
                  <CheckCircle2 className="mt-0.5 h-5 w-5 shrink-0 text-ok-500" />
                ) : (
                  <Circle className="mt-0.5 h-5 w-5 shrink-0 text-line-strong" />
                )}
                <div className="min-w-0">
                  <div
                    className={
                      s.done
                        ? 'text-sm font-medium text-ink-muted line-through decoration-line-strong'
                        : 'text-sm font-medium text-ink'
                    }
                  >
                    {s.title}
                  </div>
                  <div className="mt-0.5 text-sm text-ink-muted">{s.body}</div>
                </div>
              </li>
            ))}
          </ol>
        )}
      </CardBody>
    </Card>
  );
}

function PlanCard({ me }: { me: MeResponse }) {
  const tier = accountTier(me);
  const ceiling = tierCeiling(tier);
  const status = me.account?.status ?? 'active';
  const isEnterprise = (tier ?? '').toLowerCase() === 'enterprise';
  return (
    <Card>
      <CardHeader title="Your plan" />
      <CardBody className="space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <div className="text-lg font-semibold text-ink">
              {tierLabel(tier)}
            </div>
            <div className="mt-0.5 text-sm text-ink-muted">
              {ceiling !== null
                ? `${fmtInt(ceiling)} req/min`
                : 'Custom limits'}
            </div>
          </div>
          <Badge tone={status === 'active' ? 'ok' : 'warn'} dot>
            {status}
          </Badge>
        </div>
        {!isEnterprise && (
          <ButtonLink
            href="/dashboard/settings"
            variant="secondary"
            size="sm"
            className="w-full"
          >
            Manage plan
            <ArrowUpRight className="h-3.5 w-3.5" />
          </ButtonLink>
        )}
      </CardBody>
    </Card>
  );
}

function QuickLinks() {
  const links = [
    {
      href: '/dashboard/keys',
      label: 'API keys',
      icon: KeyRound,
      desc: 'Create & revoke',
    },
    {
      href: '/dashboard/usage',
      label: 'Usage',
      icon: BarChart3,
      desc: 'Requests & quota',
    },
  ];
  return (
    <Card>
      <CardHeader title="Quick links" />
      <CardBody className="space-y-1">
        {links.map((l) => {
          const Icon = l.icon;
          return (
            <Link
              key={l.href}
              href={l.href}
              className="group flex items-center gap-3 rounded-lg px-2 py-2 transition-colors hover:bg-surface-muted"
            >
              <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-surface-subtle text-ink-muted group-hover:text-brand-700">
                <Icon className="h-4 w-4" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block text-sm font-medium text-ink">
                  {l.label}
                </span>
                <span className="block text-xs text-ink-muted">{l.desc}</span>
              </span>
              <ArrowUpRight className="h-4 w-4 text-ink-faint group-hover:text-brand-600" />
            </Link>
          );
        })}
        <a
          href={DOCS_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="group flex items-center gap-3 rounded-lg px-2 py-2 transition-colors hover:bg-surface-muted"
        >
          <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-surface-subtle text-ink-muted group-hover:text-brand-700">
            <BookOpen className="h-4 w-4" />
          </span>
          <span className="min-w-0 flex-1">
            <span className="block text-sm font-medium text-ink">API docs</span>
            <span className="block text-xs text-ink-muted">
              Reference & guides
            </span>
          </span>
          <ArrowUpRight className="h-4 w-4 text-ink-faint group-hover:text-brand-600" />
        </a>
      </CardBody>
    </Card>
  );
}

// accountTier reads the tier from whichever shape `me` carries: a
// magic-link session populates `account.tier`; an API-key caller
// populates the top-level `tier`.
function accountTier(me: MeResponse): string | undefined {
  return me.account?.tier ?? me.tier;
}

function firstName(me: MeResponse): string {
  const name = me.user?.display_name?.trim();
  if (name) return name.split(/\s+/)[0];
  const email = me.user?.email;
  if (email) return email.split('@')[0];
  return 'there';
}
