import type { Metadata } from 'next';
import Link from 'next/link';
import { ExternalLink } from 'lucide-react';

import { API_BASE_URL } from '@/api/client';

export const metadata: Metadata = {
  title: 'API docs',
  description:
    'Full reference for the Rates Engine v1 API — every endpoint, every parameter, with copy-paste curl examples.',
};

type Group = {
  name: string;
  description: string;
  endpoints: { method: 'GET' | 'POST'; path: string; summary: string }[];
};

const GROUPS: Group[] = [
  {
    name: 'Pricing',
    description:
      'Latest VWAP, rolling tip, observations, batch lookups. Closed-bucket-only per ADR-0015.',
    endpoints: [
      { method: 'GET', path: '/v1/price', summary: 'Latest closed-bucket VWAP for one asset' },
      { method: 'GET', path: '/v1/price/tip', summary: 'Rolling-window tip price (NOT cross-region consistent)' },
      { method: 'GET', path: '/v1/price/tip/stream', summary: 'SSE counterpart of /v1/price/tip' },
      { method: 'GET', path: '/v1/price/stream', summary: 'Closed-bucket SSE — strict ADR-0015 consistency' },
      { method: 'GET', path: '/v1/price/batch', summary: 'Batch up to 100 assets per request' },
      { method: 'POST', path: '/v1/price/batch', summary: 'Batch via JSON body, ceiling 1000 assets' },
      { method: 'GET', path: '/v1/observations', summary: 'Per-source raw observations (rawest surface)' },
      { method: 'GET', path: '/v1/observations/stream', summary: 'SSE per-source tick stream' },
    ],
  },
  {
    name: 'History & charts',
    description:
      'Trade history, OHLC, VWAP/TWAP windows, rolling chart series.',
    endpoints: [
      { method: 'GET', path: '/v1/history', summary: 'Trade history within a time window' },
      { method: 'GET', path: '/v1/history/since-inception', summary: 'CAGG-served full history at a granularity' },
      { method: 'GET', path: '/v1/chart', summary: 'Rolling timeframe / granularity series (Freighter RFP shape)' },
      { method: 'GET', path: '/v1/ohlc', summary: 'Single OHLC bar over a window' },
      { method: 'GET', path: '/v1/vwap', summary: 'Volume-weighted average price' },
      { method: 'GET', path: '/v1/twap', summary: 'Time-weighted average price' },
    ],
  },
  {
    name: 'Asset & coin catalogue',
    description:
      'Asset registry with SEP-1 metadata overlay, coin directory, issuer detail.',
    endpoints: [
      { method: 'GET', path: '/v1/assets', summary: 'Distinct assets observed' },
      { method: 'GET', path: '/v1/assets/{asset_id}', summary: 'One asset with supply / volume_24h_usd / change_24h_pct' },
      { method: 'GET', path: '/v1/assets/{asset_id}/metadata', summary: 'SEP-1 metadata for an asset' },
      { method: 'GET', path: '/v1/coins', summary: 'Coin directory ranked by observation count' },
      { method: 'GET', path: '/v1/issuers/{g_strkey}', summary: 'Issuer detail + every asset they\'ve minted' },
    ],
  },
  {
    name: 'Markets & change summary',
    description:
      'Active trading pairs, multi-window delta strip per entity.',
    endpoints: [
      { method: 'GET', path: '/v1/markets', summary: 'Active markets (last 14 days), cursor-paginated' },
      { method: 'GET', path: '/v1/pairs', summary: 'Single (base, quote) activity summary' },
      { method: 'GET', path: '/v1/changes/{entity_type}/{id}', summary: 'Multi-window delta strip + ATH/ATL + streak + acceleration' },
    ],
  },
  {
    name: 'Oracles (SEP-40)',
    description:
      'Drop-in oracle interface — same contract trait that on-chain consumers integrate against.',
    endpoints: [
      { method: 'GET', path: '/v1/oracle/lastprice', summary: 'SEP-40 lastprice() — quote fixed at fiat:USD' },
      { method: 'GET', path: '/v1/oracle/prices', summary: 'SEP-40 prices() — recent retentions' },
      { method: 'GET', path: '/v1/oracle/x_last_price', summary: 'SEP-40 x_last_price(base, quote)' },
      { method: 'GET', path: '/v1/oracle/latest', summary: 'Per-source latest oracle readings (debug surface)' },
    ],
  },
  {
    name: 'Sources & diagnostics',
    description:
      'Source registry + operator diagnostics for the showcase /diagnostics page.',
    endpoints: [
      { method: 'GET', path: '/v1/sources', summary: 'Source catalogue with class + IncludeInVWAP metadata' },
      { method: 'GET', path: '/v1/diagnostics/cursors', summary: 'Per-source ingest cursor positions' },
    ],
  },
  {
    name: 'Account & signup',
    description:
      'Self-service signup, authenticated identity, key listing + rotation, paid-tier upgrades.',
    endpoints: [
      { method: 'POST', path: '/v1/signup', summary: 'Mint a first API key by email (Starter tier)' },
      { method: 'GET', path: '/v1/account/me', summary: 'Authenticated subject details' },
      { method: 'GET', path: '/v1/account/usage', summary: 'Rate-limit usage for the current key' },
      { method: 'GET', path: '/v1/account/keys', summary: 'List every key your identifier owns' },
      { method: 'POST', path: '/v1/account/keys', summary: 'Issue a new API key (rotation)' },
      { method: 'POST', path: '/v1/webhooks/stripe', summary: 'Stripe webhook — paid-tier rate-limit upgrade' },
    ],
  },
  {
    name: 'SEP-10 web auth',
    description:
      'Stellar SEP-10 challenge/response — bootstraps a JWT from a public Stellar G-strkey.',
    endpoints: [
      { method: 'GET', path: '/v1/auth/sep10/challenge', summary: 'SEP-10 challenge transaction' },
      { method: 'POST', path: '/v1/auth/sep10/token', summary: 'Exchange signed challenge for JWT' },
    ],
  },
  {
    name: 'Health & status',
    description:
      'Probes for liveness, readiness, and the customer-facing system-health rollup. /metrics is the unversioned Prometheus scrape.',
    endpoints: [
      { method: 'GET', path: '/v1/healthz', summary: 'Shallow liveness probe' },
      { method: 'GET', path: '/v1/readyz', summary: 'Deep readiness — pings every dependency' },
      { method: 'GET', path: '/v1/version', summary: 'Binary version, build date, VCS info' },
      { method: 'GET', path: '/v1/status', summary: 'System-health rollup (heartbeats / latency / freshness / incidents)' },
    ],
  },
];

export default function DocsPage() {
  return (
    <div className="mx-auto max-w-5xl space-y-8 p-6">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">API docs</h1>
        <p className="max-w-3xl text-base text-slate-600 dark:text-slate-400">
          The Rates Engine v1 API. Public tier requires no auth, no
          API key — the example URLs below work straight from your
          terminal. The full reference (every parameter, every
          response shape) is auto-generated from the OpenAPI spec.
        </p>
        <div className="flex flex-wrap gap-3 pt-2">
          <a
            href="https://docs.ratesengine.net"
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-brand-700"
          >
            Full reference
            <ExternalLink className="h-3.5 w-3.5" />
          </a>
          <a
            href="https://github.com/RatesEngine/rates-engine/blob/main/openapi/rates-engine.v1.yaml"
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 rounded-md border border-slate-300 px-3.5 py-2 text-sm font-medium text-slate-700 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:text-slate-300"
          >
            OpenAPI spec
            <ExternalLink className="h-3.5 w-3.5" />
          </a>
        </div>
      </header>

      <section className="space-y-1 rounded-xl border border-slate-200 bg-white p-4 text-sm dark:border-slate-800 dark:bg-slate-900">
        <p className="font-medium">Base URL</p>
        <code className="block rounded bg-slate-950 px-2 py-1.5 font-mono text-[11px] text-slate-100">
          {API_BASE_URL}
        </code>
        <p className="text-xs text-slate-500">
          Every response is wrapped in a standard envelope:{' '}
          <code className="font-mono text-[11px]">
            {'{ data, as_of, flags, pagination? }'}
          </code>
          . Bad inputs return RFC 7807 problem+json with a stable{' '}
          <code className="font-mono text-[11px]">type</code> URI.
        </p>
      </section>

      <section className="space-y-6">
        {GROUPS.map((g) => (
          <GroupCard key={g.name} group={g} />
        ))}
      </section>

      <section className="rounded-xl border border-slate-200 bg-white p-5 text-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-base font-semibold">Streaming endpoints</h2>
        <p className="mt-2 text-slate-600 dark:text-slate-400">
          Three SSE surfaces with deliberately different consistency
          guarantees: <Link href="/coins" className="underline decoration-dotted">/v1/price/stream</Link>{' '}
          (closed-bucket, cross-region consistent),{' '}
          <code className="font-mono text-xs">/v1/price/tip/stream</code>{' '}
          (rolling-window tip, per-connection tick), and{' '}
          <code className="font-mono text-xs">/v1/observations/stream</code>{' '}
          (rawest per-source). See ADR-0018 for which to choose for
          which use case.
        </p>
      </section>
    </div>
  );
}

function GroupCard({ group }: { group: Group }) {
  return (
    <div className="space-y-3">
      <div className="space-y-0.5">
        <h2 className="text-xl font-semibold tracking-tight">{group.name}</h2>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          {group.description}
        </p>
      </div>
      <div className="overflow-hidden rounded-lg border border-slate-200 dark:border-slate-800">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {group.endpoints.map((ep) => (
              <tr
                key={`${ep.method} ${ep.path}`}
                className="bg-white hover:bg-slate-50 dark:bg-slate-900 dark:hover:bg-slate-800/50"
              >
                <td className="px-3 py-2 align-top">
                  <span
                    className={`rounded px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider ${
                      ep.method === 'GET'
                        ? 'bg-up-soft text-up-strong'
                        : 'bg-brand-100 text-brand-700 dark:bg-brand-900 dark:text-brand-200'
                    }`}
                  >
                    {ep.method}
                  </span>
                </td>
                <td className="px-3 py-2">
                  <code className="font-mono text-xs">{ep.path}</code>
                </td>
                <td className="px-3 py-2 text-xs text-slate-600 dark:text-slate-400">
                  {ep.summary}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
