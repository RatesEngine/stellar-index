'use client';

import { useAuth } from '@/lib/auth';
import { AuthedRoute } from '@/components/AuthedRoute';

// Staff-only landing. Today this just gates on `is_staff` and shows
// a "no tools yet" placeholder; Phase 1.5 fills it with the customer
// look-up + impersonation surfaces from the platform spec §6.
export default function AdminPage() {
  return (
    <AuthedRoute>
      <AdminBody />
    </AuthedRoute>
  );
}

function AdminBody() {
  const { state } = useAuth();
  if (state.kind !== 'authed') return null;

  if (!state.me.user.is_staff) {
    return (
      <div className="rounded-md border border-red-200 bg-red-50 p-6 text-sm text-ink-muted">
        This area is restricted to staff users.
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight text-ink">
          Staff cockpit
        </h1>
        <p className="mt-1 text-sm text-ink-muted">
          Customer look-up, manual tier overrides, key revocation,
          incident tools.
        </p>
      </header>
      <div className="rounded-md border border-dashed border-surface-line bg-surface p-8 text-center text-sm text-ink-muted">
        Staff tools ship in Phase 1.5.
      </div>
    </div>
  );
}
