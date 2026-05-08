import type { Metadata } from 'next';
import Link from 'next/link';

import { Panel } from '@/components/reveal';

export const metadata: Metadata = {
  title: 'Divergences — cross-reference monitor',
  description:
    'Continuously cross-checks the canonical Rates Engine VWAP against external references (CoinGecko, Chainlink HTTP, Reflector, Redstone, Band). Persistent gaps flip flags.divergence_warning.',
};

type FeedRef = { pair: string; address: string };
type Reference = {
  name: string;
  type: string;
  blurb: string;
  feeds?: FeedRef[];
};
const REFERENCES: Reference[] = [
  {
    name: 'CoinGecko',
    type: 'HTTP price index',
    blurb:
      "Aggregator-of-aggregators. Useful as a sanity reference because it's not on-chain and pulls from a different upstream set.",
  },
  {
    name: 'Chainlink HTTP',
    type: 'HTTP feed (off-chain Chainlink)',
    blurb:
      'Independent price index via mainnet AggregatorV3 contracts on Ethereum. Queried over public RPC (eth.llamarpc.com). Drives the divergence worker\'s "are we wildly off" alerting threshold.',
    feeds: [
      // Operator-config from configs/ansible/.../ratesengine.toml.j2
      // [divergence.chainlink.feeds]. Verified 2026-05-08.
      { pair: 'EUR/USD', address: '0xb49f677943BC038e9857d61E7d053CaA2C1734C1' },
      { pair: 'GBP/USD', address: '0x5c0Ab2d9b5a7ed9f470386e82BB36A3613cDd4b5' },
      { pair: 'JPY/USD', address: '0xBcE206caE7f0ec07b545EddE332A47C2F75bbeb3' },
    ],
  },
  {
    name: 'Reflector (DEX/CEX/FX)',
    type: 'On-chain SEP-40 oracle',
    blurb:
      'Stellar-native oracle trio. Reflector divergence often signals an oracle update lag rather than a real price move — important to distinguish for downstream consumers like Blend.',
  },
  {
    name: 'Redstone',
    type: 'On-chain adapter contract',
    blurb:
      'Pull-style oracle on Stellar. Divergence here is rare but high-signal — Redstone batches many feeds in one transaction so divergence on one feed often precedes a wider reading update.',
  },
  {
    name: 'Band',
    type: 'On-chain Soroban contract (no events)',
    blurb:
      'Operation-args ingest (Band emits zero events). Divergence checks read the same relayed value the on-chain consumer would see.',
  },
];

export default function DivergencesPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Divergences</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Continuously cross-checks the canonical Rates Engine VWAP
          against external references. A persistent gap flips{' '}
          <code className="font-mono text-xs">flags.divergence_warning</code>{' '}
          on the canonical{' '}
          <Link href="/assets" className="underline decoration-dotted">
            coin pages
          </Link>{' '}
          and writes a row to the{' '}
          <code className="font-mono text-xs">divergence_observations</code>{' '}
          hypertable for the historical trail.
        </p>
      </header>

      <Panel
        title="Why we monitor divergence"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400 space-y-2"
      >
        <p>
          We never include external references in the canonical VWAP —
          mixing them would import their methodology and double-count
          whichever upstream markets they read. But silence on
          divergence would let a quiet decode bug or a stuck
          Reflector update skew prices for hours before anyone
          noticed.
        </p>
        <p>
          The divergence worker reconciles. For every (pair,
          reference) tuple, every refresh tick, it compares our VWAP
          to what the reference reports, persists the row, and (per{' '}
          <Link
            href="/research/adr/0019"
            className="underline decoration-dotted"
          >
            ADR-0019
          </Link>
          ) drives the multi-factor confidence score that gates the
          freeze decision.
        </p>
      </Panel>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {REFERENCES.map((r) => (
          <div
            key={r.name}
            className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-800 dark:bg-slate-900"
          >
            <h2 className="text-lg font-semibold tracking-tight">{r.name}</h2>
            <p className="mt-1 text-xs uppercase tracking-wider text-slate-500">
              {r.type}
            </p>
            <p className="mt-3 text-sm text-slate-700 dark:text-slate-300">
              {r.blurb}
            </p>
            {r.feeds && r.feeds.length > 0 && (
              <div className="mt-4 border-t border-slate-200 pt-3 dark:border-slate-800">
                <div className="text-[10px] font-medium uppercase tracking-wider text-slate-500">
                  Wired feeds
                </div>
                <ul className="mt-1.5 space-y-1 text-xs">
                  {r.feeds.map((f) => (
                    <li
                      key={f.address}
                      className="flex items-baseline justify-between gap-3"
                    >
                      <span className="font-mono text-slate-600 dark:text-slate-400">
                        {f.pair}
                      </span>
                      <a
                        href={`https://etherscan.io/address/${f.address}`}
                        target="_blank"
                        rel="noreferrer noopener"
                        className="font-mono text-[11px] text-brand-600 hover:underline"
                        title={f.address}
                      >
                        {f.address.slice(0, 8)}…{f.address.slice(-4)}
                      </a>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        ))}
      </div>

      <Panel
        title="Coming next"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400 space-y-2"
      >
        <p>
          Live per-(asset, reference) state, time-series of delta %,
          and per-incident drill-downs all come online once the
          divergence-observations endpoint ships (Phase 5). The
          underlying durable mirror is already running — see the
          freeze + divergence sinks listed in the{' '}
          <Link href="/research" className="underline decoration-dotted">
            research index
          </Link>
          . Methodology rationale lives in{' '}
          <Link
            href="/research/adr/0019"
            className="underline decoration-dotted"
          >
            ADR-0019
          </Link>
          .
        </p>
      </Panel>
    </div>
  );
}
