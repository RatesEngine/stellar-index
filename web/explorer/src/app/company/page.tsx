import type { Metadata } from 'next';
import Link from 'next/link';

export const metadata: Metadata = {
  title: 'Company — who we are',
  description:
    'Rates Engine — purpose-built pricing infrastructure for the Stellar network.',
};

export default function CompanyPage() {
  return (
    <div className="mx-auto max-w-3xl space-y-6 px-6 py-12">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Company</h1>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          Rates Engine is a public, vendor-neutral pricing surface for
          the Stellar network — built against the SDF and Freighter RFPs
          and the awarded CTX proposal. Apache-2.0, pre-v1.
        </p>
      </header>
      <div className="rounded-lg border border-slate-200 bg-slate-50 p-6 text-sm text-slate-700 dark:border-slate-800 dark:bg-slate-900/40 dark:text-slate-300">
        <p className="font-medium">Full company page in progress.</p>
        <p className="mt-2">
          In the meantime: design rationale lives in{' '}
          <Link href="/research" className="text-brand-600 hover:underline">
            /research
          </Link>
          , the methodology behind every price is documented at{' '}
          <Link href="/methodology" className="text-brand-600 hover:underline">
            /methodology
          </Link>
          , and we&apos;re reachable at{' '}
          <Link href="/contact" className="text-brand-600 hover:underline">
            /contact
          </Link>
          .
        </p>
      </div>
    </div>
  );
}
