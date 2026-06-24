'use client';

import { useRouter } from 'next/navigation';
import { useEffect, type ReactNode } from 'react';

import { useMe, type MeResponse } from '@/api/hooks';
import { Container, Section, Skeleton } from '@/components/ui';

// AccountGate is the client auth gate for every /dashboard/* page. It
// reuses `useMe()` — the same cookie-authed `/v1/account/me` probe the
// navbar uses — so there's a single source of truth for "is this
// visitor signed in". The explorer is a static export, so this runs
// client-side: the static shell paints, then the gate resolves.
//
// We do NOT render an app-shell / sidebar here — ConsoleShell already
// wraps the whole site (and the logged-in Account nav group already
// links to these routes). The gate only handles the three states:
//
//   - loading → a Container of skeletons (keeps the shell stable)
//   - signed-out → client redirect to /signin
//   - signed-in → render children with the resolved `me`
//
// `signedIn` mirrors the navbar's check (Sidebar.tsx): a magic-link
// session populates `me.user.email`; an API-key caller populates
// `me.key_id`. Only the magic-link session carries the account
// identity the dashboard renders, but either counts as authenticated.
function isSignedIn(me: MeResponse | null | undefined): boolean {
  return !!(me && (me.user?.email || me.key_id));
}

export function AccountGate({
  children,
}: {
  children: (me: MeResponse) => ReactNode;
}) {
  const router = useRouter();
  const me = useMe();
  const signedIn = isSignedIn(me.data);

  useEffect(() => {
    // Once the probe settles and the visitor isn't signed in, bounce
    // to the magic-link sign-in. While `isLoading` we hold (the cookie
    // may yet resolve); `me.data === null` after settle = anonymous.
    if (!me.isLoading && !signedIn) {
      router.replace('/signin');
    }
  }, [me.isLoading, signedIn, router]);

  if (me.isLoading) {
    return <AccountGateSkeleton />;
  }

  if (!signedIn || !me.data) {
    // Redirect is in flight — render the skeleton rather than flashing
    // page content for a frame.
    return <AccountGateSkeleton />;
  }

  return <>{children(me.data)}</>;
}

function AccountGateSkeleton() {
  return (
    <Container>
      <Section className="space-y-6">
        <div className="space-y-2">
          <Skeleton className="h-8 w-64" />
          <Skeleton className="h-4 w-96 max-w-full" />
        </div>
        <div className="grid grid-cols-1 gap-px overflow-hidden rounded-card border border-line bg-line sm:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="bg-surface p-5">
              <Skeleton className="h-3 w-24" />
              <Skeleton className="mt-2 h-8 w-16" />
            </div>
          ))}
        </div>
        <Skeleton className="h-48 w-full rounded-card" />
      </Section>
    </Container>
  );
}
