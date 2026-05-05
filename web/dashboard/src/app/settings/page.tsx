'use client';

import { useAuth } from '@/lib/auth';
import { AuthedRoute } from '@/components/AuthedRoute';

export default function SettingsPage() {
  return (
    <AuthedRoute>
      <SettingsBody />
    </AuthedRoute>
  );
}

function SettingsBody() {
  const { state } = useAuth();
  if (state.kind !== 'authed') return null;
  const { user, account } = state.me;

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight text-ink">
          Settings
        </h1>
        <p className="mt-1 text-sm text-ink-muted">
          Account profile + team management. More controls land as
          Phase 1 wraps.
        </p>
      </header>

      <section className="overflow-hidden rounded-md border border-surface-line bg-surface">
        <div className="border-b border-surface-line bg-surface-subtle px-5 py-3 text-sm font-medium text-ink">
          Profile
        </div>
        <dl className="divide-y divide-surface-line text-sm">
          <Row label="Email" value={user.email} />
          <Row label="Display name" value={user.display_name || '—'} />
          <Row label="Role" value={user.role} />
          <Row label="Account" value={account.name} />
          <Row label="Slug" value={account.slug} />
          <Row label="Tier" value={account.tier} />
          <Row label="Status" value={account.status} />
        </dl>
      </section>

      <p className="text-xs text-ink-faint">
        Need to change the email on file or rename the account? Contact{' '}
        <a
          className="underline"
          href="mailto:support@ratesengine.net"
        >
          support@ratesengine.net
        </a>{' '}
        until self-service edits ship.
      </p>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between px-5 py-3">
      <dt className="text-ink-muted">{label}</dt>
      <dd className="font-mono text-ink">{value}</dd>
    </div>
  );
}
