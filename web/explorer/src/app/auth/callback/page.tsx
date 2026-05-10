import type { Metadata } from 'next';

import { CallbackHandler } from './CallbackHandler';

export const metadata: Metadata = {
  title: 'Signing you in… — Rates Engine',
  description:
    'Verifying your magic-link sign-in. You will be redirected to your account in a moment.',
  robots: { index: false, follow: false },
};

/**
 * /auth/callback is the landing page for magic-link emails.
 * Renders a small "verifying" placeholder; the client component
 * forwards the request to the API's /v1/auth/callback handler
 * (which verifies the token, sets the session cookie, and 303-
 * redirects to /account or the requested `next`).
 *
 * This page is only used when the operator has configured
 * DashboardBaseURL to ratesengine.net (so the magic-link emails
 * point here). When DashboardBaseURL points elsewhere (e.g. a
 * dedicated dashboard SPA), users land there instead and never
 * see this page.
 */
export default function AuthCallbackPage() {
  return (
    <div className="mx-auto flex min-h-[60vh] max-w-md flex-col items-center justify-center px-6 py-16 text-center">
      <CallbackHandler />
    </div>
  );
}
