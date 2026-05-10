import type { Metadata } from 'next';
import Link from 'next/link';

import { SignInForm } from './SignInForm';

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
      <SignInForm mode="signin" />
      <p className="text-center text-sm text-slate-500">
        Don&apos;t have an account?{' '}
        <Link href="/signup" className="text-brand-600 hover:underline">
          Create one
        </Link>
      </p>
    </div>
  );
}
