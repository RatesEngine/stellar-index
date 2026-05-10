import type { Metadata } from 'next';
import Link from 'next/link';

import { CopyableSnippet } from './CopyableSnippet';

export const metadata: Metadata = {
  title: 'Widgets — embeddable price cards',
  description:
    'Drop-in iframe widgets for embedding live Rates Engine prices into wallets, dashboards, and product pages. No script, no API key, no build step.',
  alternates: { canonical: '/widgets' },
};

const SITE_URL = 'https://ratesengine.net';

const ASSET_EXAMPLES: { slug: string; label: string }[] = [
  { slug: 'XLM', label: 'XLM (native)' },
  {
    slug: 'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
    label: 'USDC (Centre)',
  },
  {
    slug: 'AQUA-GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA',
    label: 'AQUA',
  },
];

const PAIR_EXAMPLES: { pair: string; label: string }[] = [
  {
    pair: 'native~USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
    label: 'XLM / USDC',
  },
  { pair: 'native~fiat:USD', label: 'XLM / USD' },
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
        <p className="font-mono text-xs uppercase tracking-widest text-brand-600 dark:text-brand-400">
          Embed
        </p>
        <h1 className="text-3xl font-semibold tracking-tight">Widgets</h1>
        <p className="max-w-3xl text-base text-slate-600 dark:text-slate-400">
          Drop-in iframe widgets for live Rates Engine prices. Paste
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
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            Live price, 24h change, and a sparkline for one asset.
            Source path is{' '}
            <code className="rounded bg-slate-100 px-1 py-0.5 font-mono text-xs dark:bg-slate-800">
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
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            Live VWAP for a (base, quote) pair. Source path is{' '}
            <code className="rounded bg-slate-100 px-1 py-0.5 font-mono text-xs dark:bg-slate-800">
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
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            Live USD-base rate + 7d change for one fiat currency.
            Source path is{' '}
            <code className="rounded bg-slate-100 px-1 py-0.5 font-mono text-xs dark:bg-slate-800">
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

      <section className="rounded-xl border border-slate-200 bg-white p-5 text-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-base font-semibold">Notes</h2>
        <ul className="mt-3 space-y-2 text-slate-600 dark:text-slate-400">
          <li>
            <strong>No auth, no API key.</strong> The widgets read
            from the public tier of the Rates Engine API. Sites with
            extreme traffic should host their own copy or use{' '}
            <Link href="/signup" className="text-brand-600 hover:underline">
              a Starter key
            </Link>{' '}
            for the higher rate-limit.
          </li>
          <li>
            <strong>Light + dark.</strong> The widgets follow the
            embedding page&apos;s color scheme via{' '}
            <code className="rounded bg-slate-100 px-1 py-0.5 font-mono text-xs dark:bg-slate-800">
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
            <code className="rounded bg-slate-100 px-1 py-0.5 font-mono text-xs dark:bg-slate-800">
              ratesengine.net
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
    <div className="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
      <div className="border-b border-slate-200 bg-slate-50 px-4 py-2 text-xs uppercase tracking-wider text-slate-500 dark:border-slate-800 dark:bg-slate-950">
        {label}
      </div>
      <div className="flex h-[210px] items-center justify-center bg-slate-50 p-4 dark:bg-slate-950/40">
        <iframe
          src={src}
          width="100%"
          height="180"
          frameBorder="0"
          loading="lazy"
          title={label}
          className="rounded border border-slate-200 dark:border-slate-800"
        />
      </div>
      <div className="border-t border-slate-200 dark:border-slate-800">
        <CopyableSnippet snippet={snippet} />
      </div>
    </div>
  );
}
