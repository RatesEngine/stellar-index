'use client';

import { AuthedRoute } from '@/components/AuthedRoute';

export default function KeysPage() {
  return (
    <AuthedRoute>
      <div className="space-y-6">
        <header>
          <h1 className="text-2xl font-semibold tracking-tight text-ink">
            API keys
          </h1>
          <p className="mt-1 text-sm text-ink-muted">
            Mint and manage the keys your apps use to authenticate
            against api.ratesengine.net.
          </p>
        </header>

        <div className="rounded-md border border-dashed border-surface-line bg-surface p-8 text-center">
          <p className="text-sm text-ink-muted">
            Key management UI ships in Week 4. Today the keys live in
            Redis (created via POST /v1/account/keys) — the
            dashboard&apos;s Postgres-backed store + create-key flow is
            the next slice.
          </p>
        </div>
      </div>
    </AuthedRoute>
  );
}
