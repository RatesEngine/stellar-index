import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Section-level metadata for the in-site account dashboard. The pages
// themselves are client components (they gate on the magic-link session
// cookie via useMe), so the static title lives here on the server
// layout rather than per-page. `robots: noindex` keeps the
// authenticated surface out of search results.
export const metadata: Metadata = {
  title: 'Account',
  description:
    'Manage your Stellar Index account, API keys, usage, and plan — inside the explorer.',
  robots: { index: false, follow: false },
};

export default function AccountLayout({ children }: { children: ReactNode }) {
  return <>{children}</>;
}
