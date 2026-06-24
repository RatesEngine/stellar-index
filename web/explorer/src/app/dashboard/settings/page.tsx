'use client';

import { ArrowUpRight, Loader2, LogOut } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { useState, type ReactNode } from 'react';

import { logout } from '@/api/account';
import type { MeResponse } from '@/api/hooks';
import {
  Badge,
  Button,
  ButtonLink,
  Callout,
  Card,
  CardBody,
  CardHeader,
  Container,
  Mono,
  PageHeader,
  Section,
} from '@/components/ui';
import {
  fmtInt,
  tierCeiling,
  tierLabel,
  titleCase,
} from '@/lib/account-format';
import { cn } from '@/lib/cn';

import { AccountGate } from '../AccountGate';

/**
 * /dashboard/settings — read-only profile, the current plan, and a
 * danger zone (sign out). Self-service mutations (rename / email
 * change / deletion) are honestly deferred to support. Ported from the
 * standalone dashboard.
 */
export default function SettingsPage() {
  return <AccountGate>{(me) => <SettingsBody me={me} />}</AccountGate>;
}

function SettingsBody({ me }: { me: MeResponse }) {
  return (
    <Container>
      <Section className="max-w-3xl space-y-6">
        <PageHeader
          eyebrow="Account"
          title="Settings"
          description="Your profile, plan, and account controls."
        />

        <ProfileCard me={me} />
        <PlanCard me={me} />
        <DangerZone />

        <p className="text-xs text-ink-faint">
          Need to change the email on file, rename the account, or configure
          webhooks? Contact{' '}
          <a
            className="font-medium text-brand-700 hover:underline"
            href="mailto:support@stellarindex.io"
          >
            support@stellarindex.io
          </a>{' '}
          until self-service controls ship.
        </p>
      </Section>
    </Container>
  );
}

function ProfileCard({ me }: { me: MeResponse }) {
  const user = me.user;
  const account = me.account;
  const rows: { label: string; value: ReactNode; mono?: boolean }[] = [
    { label: 'Email', value: user?.email ?? '—', mono: Boolean(user?.email) },
    { label: 'Display name', value: user?.display_name || '—' },
    { label: 'Role', value: titleCase(user?.role) },
    { label: 'Account', value: account?.name ?? '—' },
    {
      label: 'Account slug',
      value: account?.slug ?? '—',
      mono: Boolean(account?.slug),
    },
    {
      label: 'Account ID',
      value: account?.id ? <Mono value={account.id} truncate copy /> : '—',
    },
  ];
  return (
    <Card>
      <CardHeader title="Profile" description="Read-only account identity." />
      <CardBody className="p-0">
        <dl className="divide-y divide-line">
          {rows.map((r) => (
            <div
              key={r.label}
              className="flex items-center justify-between gap-4 px-5 py-3.5"
            >
              <dt className="text-sm text-ink-muted">{r.label}</dt>
              <dd
                className={cn(
                  'min-w-0 truncate text-right text-sm text-ink',
                  r.mono && 'font-mono text-[13px]',
                )}
              >
                {r.value}
              </dd>
            </div>
          ))}
        </dl>
      </CardBody>
    </Card>
  );
}

function PlanCard({ me }: { me: MeResponse }) {
  const tier = me.account?.tier ?? me.tier;
  const ceiling = tierCeiling(tier);
  const status = me.account?.status ?? 'active';
  const isEnterprise = (tier ?? '').toLowerCase() === 'enterprise';
  return (
    <Card>
      <CardHeader
        title="Plan"
        description="Your subscription tier and its limits."
        actions={
          <Badge tone={status === 'active' ? 'ok' : 'warn'} dot>
            {status}
          </Badge>
        }
      />
      <CardBody className="flex flex-col gap-5 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="text-2xl font-semibold tracking-tight text-ink">
            {tierLabel(tier)}
          </div>
          <div className="tnum mt-1 text-sm text-ink-muted">
            {ceiling !== null
              ? `${fmtInt(ceiling)} requests / minute`
              : 'Custom rate limits'}
          </div>
        </div>
        {isEnterprise ? (
          <ButtonLink href="mailto:sales@stellarindex.io" variant="secondary">
            Contact your account team
          </ButtonLink>
        ) : (
          <ButtonLink href="mailto:sales@stellarindex.io" variant="primary">
            Upgrade plan
            <ArrowUpRight className="h-4 w-4" />
          </ButtonLink>
        )}
      </CardBody>
    </Card>
  );
}

function DangerZone() {
  const router = useRouter();
  const [signingOut, setSigningOut] = useState(false);

  async function handleSignOut() {
    setSigningOut(true);
    try {
      await logout();
    } catch {
      // Logout is best-effort — bounce regardless.
    } finally {
      router.replace('/signin');
    }
  }

  return (
    <Card className="border-bad-300">
      <CardHeader className="border-bad-300/60" title="Danger zone" />
      <CardBody className="space-y-4">
        <Callout tone="bad">
          Account deletion is handled by support. Email{' '}
          <a
            className="font-medium underline"
            href="mailto:support@stellarindex.io"
          >
            support@stellarindex.io
          </a>{' '}
          to close your account and revoke all keys.
        </Callout>
        <div className="flex items-center justify-between gap-4">
          <div className="text-sm text-ink-muted">
            Sign out of your account on this device.
          </div>
          <Button
            variant="secondary"
            onClick={handleSignOut}
            disabled={signingOut}
          >
            {signingOut ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <LogOut className="h-4 w-4" />
            )}
            {signingOut ? 'Signing out…' : 'Sign out'}
          </Button>
        </div>
      </CardBody>
    </Card>
  );
}
