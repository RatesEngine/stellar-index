import type { Metadata } from 'next';
import Link from 'next/link';

export const metadata: Metadata = {
  title: 'Developer docs — Stellar Index API',
  description:
    'Get started with the Stellar Index API: base URL, authentication, rate limits, the core pricing/asset/market endpoints, SSE streaming, errors, and conventions.',
  alternates: { canonical: '/docs' },
  openGraph: {
    title: 'Stellar Index API — Developer docs',
    description:
      'Base URL, auth, rate limits, pricing/asset/market endpoints, SSE streaming, errors, and conventions for the Stellar Index API.',
    url: 'https://stellarindex.io/docs',
    type: 'website',
  },
};

const BASE = 'https://api.stellarindex.io';

function Code({ children }: { children: string }) {
  return (
    <pre className="overflow-x-auto rounded-lg border border-line bg-surface-subtle p-4 text-[13px] leading-relaxed text-ink">
      <code className="font-mono">{children}</code>
    </pre>
  );
}

function Section({
  id,
  title,
  children,
}: {
  id: string;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section id={id} className="space-y-4 scroll-mt-24">
      <h2 className="text-xl font-semibold tracking-tight text-ink">
        <a href={`#${id}`} className="hover:text-brand-600">
          {title}
        </a>
      </h2>
      <div className="space-y-4 text-[15px] leading-relaxed text-ink-body">
        {children}
      </div>
    </section>
  );
}

const ENDPOINTS: { group: string; rows: [string, string][] }[] = [
  {
    group: 'Pricing',
    rows: [
      ['GET /v1/price', 'Latest closed-bucket VWAP for a pair.'],
      ['GET /v1/price/tip', 'Rolling-window (live) price for a pair.'],
      ['GET /v1/price/batch', 'Many pairs in one request.'],
      ['GET /v1/vwap, /v1/twap', 'Volume- / time-weighted average price.'],
      ['GET /v1/ohlc, /v1/chart, /v1/history', 'OHLC candles, chart series, full history.'],
    ],
  },
  {
    group: 'Assets & markets',
    rows: [
      ['GET /v1/assets, /v1/assets/verified', 'Asset catalogue + the verified set.'],
      ['GET /v1/assets/{asset_id}', 'Per-asset detail (price, supply, holders).'],
      ['GET /v1/markets, /v1/markets/sources', 'Aggregate markets + per-source breakdown.'],
      ['GET /v1/issuers, /v1/issuers/{g}', 'Issuer directory + per-issuer detail.'],
    ],
  },
  {
    group: 'Protocols & network',
    rows: [
      ['GET /v1/protocols, /v1/protocols/{name}', 'Per-protocol analytics.'],
      ['GET /v1/lending/pools, /v1/pools', 'Lending pools + reserves.'],
      ['GET /v1/network/stats, /v1/network/throughput', 'Network-wide stats.'],
      ['GET /v1/mev, /v1/anomalies, /v1/divergence', 'Integrity + monitoring feeds.'],
    ],
  },
  {
    group: 'Streaming (SSE)',
    rows: [
      ['GET /v1/price/stream, /v1/price/tip/stream', 'Server-Sent Events price feeds.'],
      ['GET /v1/observations/stream, /v1/oracle/streams', 'Raw observation + oracle streams.'],
      ['GET /v1/ledger/stream', 'Live ledger tip stream.'],
    ],
  },
];

export default function DocsPage() {
  return (
    <div className="mx-auto max-w-4xl space-y-10 px-6 py-10">
      <header className="space-y-3">
        <h1 className="text-3xl font-semibold tracking-tight">Developer docs</h1>
        <p className="text-base text-ink-body">
          The Stellar Index API serves verified, per-protocol Stellar pricing and
          on-chain data over REST + SSE. This page is the quickstart; the full
          machine-readable contract is the{' '}
          <a className="text-brand-600 hover:underline" href="/openapi/stellar-index.v1.yaml">
            OpenAPI spec
          </a>
          .
        </p>
      </header>

      <Section id="base-url" title="Base URL & versioning">
        <p>
          All endpoints live under a single versioned base. v1 is stable; additive
          changes bump the minor version, breaking changes the major.
        </p>
        <Code>{`${BASE}/v1`}</Code>
        <Code>{`curl ${BASE}/v1/price?asset=native&quote=fiat:USD`}</Code>
      </Section>

      <Section id="auth" title="Authentication">
        <p>
          Public endpoints work without a key (subject to rate limits). An API key
          raises your limits and is required for account/usage endpoints. Keys are{' '}
          <code className="font-mono text-sm">sip_*</code> tokens (legacy{' '}
          <code className="font-mono text-sm">rek_*</code> still accepted), minted
          in the{' '}
          <Link className="text-brand-600 hover:underline" href="/dashboard/keys">
            dashboard
          </Link>
          , and passed as a bearer token:
        </p>
        <Code>{`curl -H "Authorization: Bearer sip_your_key_here" \\
  ${BASE}/v1/price?asset=native&quote=fiat:USD`}</Code>
      </Section>

      <Section id="rate-limits" title="Rate limits">
        <p>
          Every response carries{' '}
          <code className="font-mono text-sm">X-RateLimit-Limit</code> and{' '}
          <code className="font-mono text-sm">X-RateLimit-Remaining</code>. When the
          quota is exhausted the API returns{' '}
          <code className="font-mono text-sm">429</code> with a{' '}
          <code className="font-mono text-sm">Retry-After</code> header (seconds
          until you can retry). Back off and retry — do not hammer.
        </p>
      </Section>

      <Section id="endpoints" title="Core endpoints">
        <div className="space-y-6">
          {ENDPOINTS.map((g) => (
            <div key={g.group} className="space-y-2">
              <h3 className="text-sm font-semibold uppercase tracking-wider text-brand-600">
                {g.group}
              </h3>
              <dl className="divide-y divide-line rounded-lg border border-line">
                {g.rows.map(([ep, desc]) => (
                  <div key={ep} className="flex flex-col gap-1 px-4 py-2.5 sm:flex-row sm:items-baseline sm:gap-4">
                    <dt className="font-mono text-[13px] text-ink sm:w-72 sm:shrink-0">{ep}</dt>
                    <dd className="text-sm text-ink-body">{desc}</dd>
                  </div>
                ))}
              </dl>
            </div>
          ))}
        </div>
      </Section>

      <Section id="streaming" title="Streaming (Server-Sent Events)">
        <p>
          The <code className="font-mono text-sm">*/stream</code> endpoints return{' '}
          <code className="font-mono text-sm">text/event-stream</code>. Connect with
          an SSE client and read price/observation/ledger events as they close:
        </p>
        <Code>{`curl -N ${BASE}/v1/price/stream?asset=native&quote=fiat:USD`}</Code>
      </Section>

      <Section id="errors" title="Errors">
        <p>
          Errors are RFC 7807 <code className="font-mono text-sm">application/problem+json</code>:
          a stable <code className="font-mono text-sm">type</code> URI,{' '}
          <code className="font-mono text-sm">title</code>,{' '}
          <code className="font-mono text-sm">status</code>, and a human{' '}
          <code className="font-mono text-sm">detail</code>. Branch on{' '}
          <code className="font-mono text-sm">type</code>, not the prose.
        </p>
        <Code>{`{
  "type": "https://api.stellarindex.io/errors/rate-limited",
  "title": "Rate limit exceeded",
  "status": 429,
  "detail": "quota exhausted; see Retry-After"
}`}</Code>
      </Section>

      <Section id="conventions" title="Conventions">
        <ul className="list-disc space-y-2 pl-5">
          <li>
            <strong>Amounts are strings.</strong> Token amounts, reserves, supplies
            and prices can exceed 2<sup>53</sup>, so they serialize as JSON strings
            (parse with a big-decimal type) — never as JSON numbers.
          </li>
          <li>
            <strong>Asset IDs.</strong> <code className="font-mono text-sm">native</code>{' '}
            (XLM), <code className="font-mono text-sm">CODE-GISSUER…</code> (classic),{' '}
            <code className="font-mono text-sm">C…</code> (Soroban),{' '}
            <code className="font-mono text-sm">crypto:BTC</code> /{' '}
            <code className="font-mono text-sm">fiat:USD</code> (reference).
          </li>
          <li>
            <strong>Closed-bucket pricing.</strong> <code className="font-mono text-sm">/v1/price</code>{' '}
            serves the latest <em>closed</em> 1-minute VWAP bucket (deterministic
            across regions); <code className="font-mono text-sm">/v1/price/tip</code>{' '}
            is the rolling live window.
          </li>
        </ul>
      </Section>

      <Section id="more" title="More">
        <ul className="list-disc space-y-2 pl-5">
          <li>
            <a className="text-brand-600 hover:underline" href="https://docs.stellarindex.io">
              Full API reference
            </a>{' '}
            — every endpoint, parameter, and schema
          </li>
          <li>
            <Link className="text-brand-600 hover:underline" href="/sdk">SDK & client libraries</Link>
          </li>
          <li>
            <Link className="text-brand-600 hover:underline" href="/methodology">Pricing methodology</Link>{' '}
            — how every number is computed
          </li>
          <li>
            <Link className="text-brand-600 hover:underline" href="/changelog">API changelog</Link>
          </li>
          <li>
            <a className="text-brand-600 hover:underline" href="/openapi/stellar-index.v1.yaml">OpenAPI spec</a>
          </li>
        </ul>
      </Section>
    </div>
  );
}
