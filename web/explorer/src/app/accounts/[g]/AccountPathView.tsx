'use client';

import { useLastPathSegment } from '@/lib/useLastPathSegment';

import { AccountView } from '../AccountView';

// Reads the real account ID (G-strkey) from the path at runtime. G-strkeys are
// case-sensitive base32 — we never lowercase. The CF Function serves the one
// built shell for any /accounts/{g}; richlist/named accounts get pre-rendered
// + indexed later (SEO plan D6).
export function AccountPathView() {
  const id = useLastPathSegment();
  return <AccountView id={id} />;
}
