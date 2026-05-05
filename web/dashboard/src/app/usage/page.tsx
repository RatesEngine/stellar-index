'use client';

import { AuthedRoute } from '@/components/AuthedRoute';

export default function UsagePage() {
  return (
    <AuthedRoute>
      <div className="space-y-6">
        <header>
          <h1 className="text-2xl font-semibold tracking-tight text-ink">
            Usage
          </h1>
          <p className="mt-1 text-sm text-ink-muted">
            Per-day and per-key request volume, error rates, and quota
            utilisation against your tier limits.
          </p>
        </header>

        <div className="rounded-md border border-dashed border-surface-line bg-surface p-8 text-center">
          <p className="text-sm text-ink-muted">
            Usage charts ship in Week 5. The Redis stream → Timescale
            worker that feeds them is the prerequisite slice.
          </p>
        </div>
      </div>
    </AuthedRoute>
  );
}
