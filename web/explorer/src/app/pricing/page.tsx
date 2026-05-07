import type { Metadata } from 'next';
import Link from 'next/link';

export const metadata: Metadata = {
  title: 'Pricing — plans, quotas, SLAs',
  description:
    'Rates Engine pricing — plan tiers, request quotas, SLA targets, billing.',
};

export default function PricingPage() {
  return (
    <div className="mx-auto max-w-3xl space-y-6 px-6 py-12">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Pricing</h1>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          Plan tiers, request quotas, SLA targets.
        </p>
      </header>
      <div className="rounded-lg border border-slate-200 bg-slate-50 p-6 text-sm text-slate-700 dark:border-slate-800 dark:bg-slate-900/40 dark:text-slate-300">
        <p className="font-medium">Pricing is being finalised.</p>
        <p className="mt-2">
          We&apos;re pre-v1, and the public API is currently free to
          read at low rate limits. To talk about commercial terms,
          custom SLAs, or higher quotas, reach out via{' '}
          <Link href="/contact" className="text-brand-600 hover:underline">
            /contact
          </Link>
          .
        </p>
      </div>
    </div>
  );
}
