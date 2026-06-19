import { RedirectToStatus } from './RedirectToStatus';

// Redirect stub. The real 301 happens at the Cloudflare edge via
// public/_redirects (/*  https://stellarindex.io/status/:splat  301);
// this page is the fallback a client sees only if it somehow bypasses
// that rule. RedirectToStatus does a belt-and-suspenders client-side
// replace, and the visible link covers no-JS clients.
export const dynamic = 'force-static';

const TARGET = 'https://stellarindex.io/status/';

export default function StatusMovedPage() {
  return (
    <main className="mx-auto flex min-h-screen max-w-lg flex-col items-center justify-center gap-3 px-6 text-center">
      <RedirectToStatus target={TARGET} />
      <h1 className="text-2xl font-semibold">The status page has moved</h1>
      <p className="text-sm text-ink-muted">
        It now lives on the main site. Redirecting you to{' '}
        <a href={TARGET} className="text-brand-600 underline">
          stellarindex.io/status
        </a>
        …
      </p>
    </main>
  );
}
