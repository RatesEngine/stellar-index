import type { Metadata } from 'next';

import { IssuersTable } from './IssuersTable';

export const metadata: Metadata = {
  title: 'Issuers — every G-account that mints classic assets on Stellar',
  description:
    'The issuer directory ranked by total observation count. Each row is a G-strkey that has minted at least one classic asset, with home_domain (when SEP-1 has resolved it) and per-asset counts.',
};

export default function IssuersPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Issuers</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Every G-account that has minted at least one classic asset
          on Stellar, ranked by total observation count across their
          issued assets. The home_domain column populates as the
          SEP-1 fetcher worker resolves stellar.toml for each issuer.
        </p>
      </header>

      <IssuersTable />
    </div>
  );
}
