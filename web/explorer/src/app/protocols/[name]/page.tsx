import type { Metadata } from 'next';
import { Suspense } from 'react';

import { SITE_OG_IMAGES, SITE_TWITTER_IMAGES } from '@/lib/seo';
import { ProtocolView } from './ProtocolView';
import { PROTOCOLS, protocolMeta } from '../registry';

// Static-export dynamic route. The protocol name set is the bounded
// registry (../registry.ts, mirrored from internal/api/v1/
// protocols_registry.go), so we pre-render exactly those slugs and 404
// anything else — there is no unbounded long-tail here (unlike the
// contract/tx/ledger explorer pages, whose entities are unbounded and
// therefore use query-param pages instead).
export function generateStaticParams() {
  return PROTOCOLS.map((p) => ({ name: p.name }));
}

// Bounded registry → reject unknown slugs at build, don't client-render
// a fetch that would 404.
export const dynamicParams = false;

type Params = Promise<{ name: string }>;

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { name } = await params;
  const meta = protocolMeta(name);
  const canonical = `https://stellarindex.io/protocols/${encodeURIComponent(name)}`;
  const title = meta
    ? `${meta.label} — protocol analytics · Stellar Index`
    : `${name} — protocol analytics · Stellar Index`;
  const description = meta
    ? `${meta.description} Every contract, event type and on-chain activity for ${meta.label}, verified against the certified ledger lake.`
    : `On-chain analytics, contract roster and event-type breakdown for ${name}.`;
  return {
    title,
    description,
    alternates: { canonical },
    openGraph: {
      title,
      description,
      url: canonical,
      type: 'website',
      images: SITE_OG_IMAGES,
    },
    twitter: {
      card: 'summary_large_image',
      title,
      description,
      images: SITE_TWITTER_IMAGES,
    },
  };
}

export default async function ProtocolDetailPage({
  params,
}: {
  params: Params;
}) {
  const { name } = await params;
  const meta = protocolMeta(name);

  // Schema.org BreadcrumbList — Home → Protocols → <name>.
  const breadcrumbLD = {
    '@context': 'https://schema.org',
    '@type': 'BreadcrumbList',
    itemListElement: [
      { '@type': 'ListItem', position: 1, name: 'Home', item: 'https://stellarindex.io' },
      { '@type': 'ListItem', position: 2, name: 'Protocols', item: 'https://stellarindex.io/protocols' },
      {
        '@type': 'ListItem',
        position: 3,
        name: meta?.label ?? name,
        item: `https://stellarindex.io/protocols/${name}`,
      },
    ],
  };

  return (
    <>
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(breadcrumbLD) }}
      />
      <Suspense
        fallback={
          <div className="mx-auto max-w-7xl px-6 py-16 text-sm text-slate-500">
            Loading {meta?.label ?? name}…
          </div>
        }
      >
        <ProtocolView name={name} label={meta?.label ?? name} />
      </Suspense>
    </>
  );
}
