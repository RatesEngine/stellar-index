import type { Metadata } from 'next';
import Link from 'next/link';

import { CopyableSnippet } from './CopyableSnippet';

export const metadata: Metadata = {
  title: 'Widgets — embeddable price cards',
  description:
    'Drop-in iframe widgets for embedding live Stellar Index prices into wallets, dashboards, and product pages. No script, no API key, no build step.',
  alternates: { canonical: '/widgets' },
};

const SITE_URL = 'https://stellarindex.io';

// Slug forms — what `generateStaticParams` in the embed routes
// actually pre-renders (per the /v1/coins listing's `slug` field).
// The full asset_id form (`USDC-GA5Z…`) is also a valid input on the
// API but isn't currently in the static-params output, so iframes
// pointing at it 404. The showcase uses slugs to stay aligned with
// what's actually built.
const ASSET_EXAMPLES: { slug: string; label: string }[] = [
  { slug: 'XLM', label: 'XLM (native)' },
  { slug: 'USDC', label: 'USDC (Centre)' },
  { slug: 'AQUA', label: 'AQUA' },
];

// Pair embeds key on `<base-asset-id>~<quote-asset-id>` and the
// static-params builder enumerates real trading pairs from
// /v1/markets — synthetic stablecoin-fiat pairs (e.g.
// `native~fiat:USD`, an aggregator-time construct) aren't pre-rendered
// because no row exists for them in /v1/markets. We feature one real
// XLM/USDC pair instead; the explorer's stablecoin-proxy expansion is
// what exposes the same XLM/USD VWAP via the /v1/price API for users
// who want fiat-quoted figures.
const PAIR_EXAMPLES: { pair: string; label: string }[] = [
  {
    pair: 'native~USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
    label: 'XLM / USDC',
  },
];

const CURRENCY_EXAMPLES: { ticker: string; label: string }[] = [
  { ticker: 'EUR', label: 'EUR (Euro)' },
  { ticker: 'GBP', label: 'GBP (British Pound)' },
  { ticker: 'JPY', label: 'JPY (Japanese Yen)' },
];

export default function WidgetsPage() {
  return (
    <div className="mx-auto max-w-5xl space-y-12 px-6 py-10">
      <header className="space-y-3">
        <p className="font-mono text-xs uppercase tracking-widest text-brand-600">
          Embed
        </p>
        <h1 className="text-3xl font-semibold tracking-tight">Widgets</h1>
        <p className="max-w-3xl text-base text-ink-body">
          Drop-in iframe widgets for live Stellar Index prices. Paste
          one snippet — no script, no API key, no build step. Each
          widget renders the same data the explorer pulls from the
          public API; sizes auto-adjust to fit their container.
        </p>
      </header>

      <section className="space-y-4">
        <div>
          <h2 className="text-xl font-semibold tracking-tight">
            Asset card
          </h2>
          <p className="mt-1 text-sm text-ink-body">
            Live price, 24h change, and a sparkline for one asset.
            Source path is{' '}
            <code className="rounded-sm bg-surface-subtle px-1 py-0.5 font-mono text-xs">
              /embed/asset/&lt;slug&gt;
            </code>
            .
          </p>
        </div>
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
          {ASSET_EXAMPLES.map((ex) => (
            <WidgetCard
              key={ex.slug}
              label={ex.label}
              src={`${SITE_URL}/embed/asset/${ex.slug}`}
              snippet={`<iframe
  src="${SITE_URL}/embed/asset/${ex.slug}"
  width="320"
  height="180"
  frameborder="0"
  loading="lazy"
  title="${ex.label} price card"
></iframe>`}
            />
          ))}
        </div>
      </section>

      <section className="space-y-4">
        <div>
          <h2 className="text-xl font-semibold tracking-tight">
            Pair card
          </h2>
          <p className="mt-1 text-sm text-ink-body">
            Live VWAP for a (base, quote) pair. Source path is{' '}
            <code className="rounded-sm bg-surface-subtle px-1 py-0.5 font-mono text-xs">
              /embed/pair/&lt;base&gt;~&lt;quote&gt;
            </code>{' '}
            (URL-encode the tilde when embedding from servers that
            require strict path encoding).
          </p>
        </div>
        <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
          {PAIR_EXAMPLES.map((ex) => (
            <WidgetCard
              key={ex.pair}
              label={ex.label}
              src={`${SITE_URL}/embed/pair/${encodeURIComponent(ex.pair)}`}
              snippet={`<iframe
  src="${SITE_URL}/embed/pair/${encodeURIComponent(ex.pair)}"
  width="380"
  height="200"
  frameborder="0"
  loading="lazy"
  title="${ex.label} price card"
></iframe>`}
            />
          ))}
        </div>
      </section>

      <section className="space-y-4">
        <div>
          <h2 className="text-xl font-semibold tracking-tight">Currency card</h2>
          <p className="mt-1 text-sm text-ink-body">
            Live USD-base rate + 7d change for one fiat currency.
            Source path is{' '}
            <code className="rounded-sm bg-surface-subtle px-1 py-0.5 font-mono text-xs">
              /embed/currency/&lt;ticker&gt;
            </code>
            .
          </p>
        </div>
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
          {CURRENCY_EXAMPLES.map((ex) => (
            <WidgetCard
              key={ex.ticker}
              label={ex.label}
              src={`${SITE_URL}/embed/currency/${ex.ticker}`}
              snippet={`<iframe
  src="${SITE_URL}/embed/currency/${ex.ticker}"
  width="320"
  height="180"
  frameborder="0"
  loading="lazy"
  title="${ex.label} rate card"
></iframe>`}
            />
          ))}
        </div>
      </section>

      <section className="rounded-xl border border-line bg-surface p-5 text-sm">
        <h2 className="text-base font-semibold">Notes</h2>
        <ul className="mt-3 space-y-2 text-ink-body">
          <li>
            <strong>No auth, no API key.</strong> The widgets read
            from the public tier of the Stellar Index API. Sites with
            extreme traffic should host their own copy or use{' '}
            <Link href="/signup" className="text-brand-600 hover:underline">
              a Starter key
            </Link>{' '}
            for the higher rate-limit.
          </li>
          <li>
            <strong>Light + dark.</strong> The widgets follow the
            embedding page&apos;s color scheme via{' '}
            <code className="rounded-sm bg-surface-subtle px-1 py-0.5 font-mono text-xs">
              prefers-color-scheme
            </code>
            . Tested against light, dark, and system-default
            backgrounds.
          </li>
          <li>
            <strong>Sandboxed.</strong> The iframe runs in a
            sandboxed browsing context. The widget cannot read your
            page&apos;s cookies, localStorage, or DOM.
          </li>
          <li>
            <strong>Apex domain only.</strong> Embed against the apex
            (
            <code className="rounded-sm bg-surface-subtle px-1 py-0.5 font-mono text-xs">
              stellarindex.io
            </code>
            ), not a preview deployment. Cloudflare-Pages preview URLs
            are firewalled from external embedding.
          </li>
        </ul>
      </section>
    </div>
  );
}

function WidgetCard({
  label,
  src,
  snippet,
}: {
  label: string;
  src: string;
  snippet: string;
}) {
  return (
    <div className="overflow-hidden rounded-xl border border-line bg-surface shadow-sm">
      <div className="border-b border-line bg-surface-muted px-4 py-2 text-xs uppercase tracking-wider text-ink-muted">
        {label}
      </div>
      <div className="flex h-[210px] items-center justify-center bg-surface-muted p-4">
        <iframe
          src={src}
          width="100%"
          height="180"
          frameBorder="0"
          loading="lazy"
          title={label}
          className="rounded-sm border border-line"
        />
      </div>
      <div className="border-t border-line">
        <CopyableSnippet snippet={snippet} />
      </div>
    </div>
  );
}
