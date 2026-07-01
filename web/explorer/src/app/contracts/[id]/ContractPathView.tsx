'use client';

import { useLastPathSegment } from '@/lib/useLastPathSegment';

import { ContractView } from '../../contract/ContractView';

// Reads the real contract ID (C-strkey, case-sensitive) from the path at
// runtime. The CF Function serves the one built shell for any /contracts/{id};
// active contracts get pre-rendered + indexed later (SEO plan D6).
export function ContractPathView() {
  const id = useLastPathSegment();
  return <ContractView id={id} />;
}
