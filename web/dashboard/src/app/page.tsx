'use client';

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { useAuth } from '@/lib/auth';

// Root route — bounce based on auth state.
//
//   loading → render a brief "checking session..." card
//   anon    → push to /signin/
//   authed  → push to /keys/ (the default landed-page is the
//             keys list; usage / settings live in the side nav)
export default function RootPage() {
  const router = useRouter();
  const { state } = useAuth();

  useEffect(() => {
    if (state.kind === 'anon') router.replace('/signin/');
    if (state.kind === 'authed') router.replace('/keys/');
  }, [state, router]);

  return (
    <div className="flex min-h-screen items-center justify-center text-ink-muted">
      <p className="text-sm">Checking your session…</p>
    </div>
  );
}
