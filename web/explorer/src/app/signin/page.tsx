import type { Metadata } from 'next';
import Link from 'next/link';

export const metadata: Metadata = {
  title: 'Sign in — Rates Engine',
  description:
    'Sign in to your Rates Engine account. Magic-link email auth — no passwords.',
};

export default function SignInPage() {
  return (
    <div className="mx-auto max-w-md space-y-6 px-6 py-16">
      <header className="space-y-2 text-center">
        <h1 className="text-3xl font-semibold tracking-tight">Sign in</h1>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          Magic-link email — no passwords.
        </p>
      </header>
      <div className="rounded-lg border border-slate-200 bg-slate-50 p-6 text-sm text-slate-700 dark:border-slate-800 dark:bg-slate-900/40 dark:text-slate-300">
        <p className="font-medium">Magic-link login is being built.</p>
        <p className="mt-2">
          The current API uses static API keys. We&apos;re replacing
          that with a proper user-account system — email magic link to
          sign in, then API keys are scoped to your account. Until that
          ships, request a key via{' '}
          <Link href="/contact" className="text-brand-600 hover:underline">
            /contact
          </Link>
          .
        </p>
      </div>
    </div>
  );
}
