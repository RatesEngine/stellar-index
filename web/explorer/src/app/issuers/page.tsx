import type { Metadata } from 'next';

import { Container, PageHeader } from '@/components/ui';
import { IssuersTable } from './IssuersTable';

export const metadata: Metadata = {
  alternates: { canonical: '/issuers' },
  title: 'Issuers — every G-account that mints classic assets on Stellar',
  description:
    'The issuer directory ranked by total observation count. Each row is a G-strkey that has minted at least one classic asset, with home_domain (when SEP-1 has resolved it) and per-asset counts.',
};

export default function IssuersPage() {
  return (
    <Container className="space-y-8 py-8 sm:py-10">
      <PageHeader
        eyebrow="Directory"
        title="Issuers"
        description="Every G-account that has minted at least one classic asset on Stellar, ranked by total observation count across their issued assets. The home_domain column populates as the SEP-1 fetcher resolves stellar.toml for each issuer."
      />
      <IssuersTable />
    </Container>
  );
}
