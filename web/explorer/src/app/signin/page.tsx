import type { Metadata } from 'next';
import Link from 'next/link';

import { SignInForm } from './SignInForm';

export const metadata: Metadata = {
  // Auth form — no SEO value; keep it out of the index (and the sitemap).
  robots: { index: false, follow: false },
  title: 'Sign in — Stellar Index',
  description:
    'Sign in to your Stellar Index account. Magic-link email auth — no passwords.',
};

export default function SignInPage() {
  return (
    <div className="mx-auto max-w-md space-y-6 px-6 py-16">
      <header className="space-y-2 text-center">
        <h1 className="text-3xl font-semibold tracking-tight">Sign in</h1>
        <p className="text-sm text-ink-body">
          Magic-link email — no passwords.
        </p>
      </header>
      <SignInForm mode="signin" />
      <p className="text-center text-sm text-ink-muted">
        Don&apos;t have an account?{' '}
        <Link href="/signup" className="text-brand-600 hover:underline">
          Create one
        </Link>
      </p>
    </div>
  );
}
