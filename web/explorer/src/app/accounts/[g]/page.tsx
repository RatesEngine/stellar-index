import type { Metadata } from 'next';
import { Suspense } from 'react';

import { AccountPathView } from './AccountPathView';

// Shell-only for now; richlist + named accounts will be pre-rendered + indexed
// via generateStaticParams (the Go bulk-manifest endpoint, SEO plan D6). Until
// then the long tail is noindex shells served by functions/accounts/[[path]].js.
export const dynamicParams = false;

export function generateStaticParams() {
  return [{ g: 'shell' }];
}

export const metadata: Metadata = {
  title: 'Account — Stellar Index',
  description:
    'Stellar account detail: balances, trustlines, signers, and recent activity.',
  robots: { index: false, follow: true },
};

export default function AccountDetailPage() {
  return (
    <Suspense fallback={null}>
      <AccountPathView />
    </Suspense>
  );
}
