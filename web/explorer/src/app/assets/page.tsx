import type { Metadata } from 'next';
import { Suspense } from 'react';

import { Container, PageHeader, Skeleton } from '@/components/ui';
import { AssetsTable } from './AssetsTable';
import {
  VerifiedCurrenciesStrip,
  fetchVerifiedCurrencies,
} from './VerifiedCurrenciesStrip';

export const metadata: Metadata = {
  title: 'Assets — every token on Stellar',
  description:
    'Browse every classic and Soroban asset observed on Stellar — live price, 24h volume, market cap, supply, issuer. The canonical Stellar asset directory.',
};

/**
 * /assets — the explorer's asset directory.
 *
 * Server-component shell wraps a client-side table in Suspense so
 * the static export can pre-render the page chrome while the
 * client reads `?cursor=` / `?limit=` / `?issuer=` from the URL.
 */
export default async function AssetsPage() {
  // Single server-side fetch of the verified-currency catalogue
  // shared between the strip (renders each entry as a chip) and the
  // table (marks each row whose slug is in the catalogue with a
  // green check). One round-trip, no double fetch.
  const verified = await fetchVerifiedCurrencies();
  const verifiedSlugs = verified.map((v) => v.slug);

  return (
    <Container className="space-y-8 py-8 sm:py-10">
      <PageHeader
        eyebrow="Directory"
        title="Assets"
        description="Every classic + Soroban asset observed on Stellar. Live price via VWAP across on-chain DEXes, classic SDEX, and major off-chain venues. Click through for live charts, recent trades, supply detail, and issuer profile."
      />
      <VerifiedCurrenciesStrip verified={verified} />
      <Suspense fallback={<Skeleton className="h-96 w-full" />}>
        <AssetsTable verifiedSlugs={verifiedSlugs} />
      </Suspense>
    </Container>
  );
}
