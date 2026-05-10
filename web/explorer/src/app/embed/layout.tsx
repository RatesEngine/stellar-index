import type { Metadata } from 'next';

export const metadata: Metadata = {
  // Embeddable widgets are not destination pages — keep them out
  // of search indices.
  robots: { index: false, follow: false },
};

/**
 * /embed/* routes use a chrome-less layout — no navbar, no footer,
 * no max-width container — so the widget fills the iframe edge to
 * edge regardless of the host page's width.
 *
 * Lives at app/embed/layout.tsx to override the root layout.tsx
 * for everything under /embed (Next.js app-router nested layouts).
 */
export default function EmbedLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return <div className="h-full w-full">{children}</div>;
}
