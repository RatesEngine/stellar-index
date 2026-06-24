import Link from 'next/link';

import { PROTOCOLS } from '@/app/protocols/registry';
import { serializeJsonLd } from '@/lib/seo';

/**
 * A category landing page (SEO plan D5): "Stellar {category} protocols". Lists
 * the protocols in a category from the static registry as cards linking to
 * their /protocols/{name} detail. Used by /amm and /yield (the categories
 * without a bespoke page); /lending, /dexes, /oracles, /bridges keep their
 * existing bespoke pages.
 */
export function CategoryHub({
  category,
  title,
  description,
  footnote,
}: {
  category: string;
  title: string;
  description: string;
  footnote?: React.ReactNode;
}) {
  const items = PROTOCOLS.filter((p) => p.category === category);

  const itemListLD = {
    '@context': 'https://schema.org',
    '@type': 'ItemList',
    name: title,
    itemListElement: items.map((p, i) => ({
      '@type': 'ListItem',
      position: i + 1,
      name: p.label,
      url: `https://stellarindex.io/protocols/${p.name}`,
    })),
  };

  return (
    <div className="mx-auto max-w-4xl space-y-8 px-6 py-10">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: serializeJsonLd(itemListLD) }}
      />
      <header className="space-y-3">
        <nav aria-label="Breadcrumb" className="text-xs text-ink-muted">
          <Link href="/" className="hover:text-brand-600">Home</Link>
          <span aria-hidden className="px-1.5 text-ink-faint">/</span>
          <Link href="/protocols" className="hover:text-brand-600">Protocols</Link>
        </nav>
        <h1 className="text-3xl font-semibold tracking-tight">{title}</h1>
        <p className="max-w-prose text-base text-ink-body">{description}</p>
      </header>

      <div className="grid gap-4 sm:grid-cols-2">
        {items.map((p) => (
          <Link
            key={p.name}
            href={`/protocols/${p.name}`}
            className="group rounded-xl border border-line bg-surface p-5 transition-colors hover:border-brand-300 hover:bg-surface-subtle"
          >
            <h2 className="text-lg font-semibold text-ink group-hover:text-brand-600">
              {p.label}
            </h2>
            <p className="mt-1.5 text-sm leading-relaxed text-ink-body">
              {p.description}
            </p>
          </Link>
        ))}
      </div>

      {footnote && (
        <p className="border-t border-line pt-5 text-sm text-ink-muted">{footnote}</p>
      )}
    </div>
  );
}
