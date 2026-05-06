import type { Metadata } from 'next';
import Link from 'next/link';
import { ExternalLink } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';

export const metadata: Metadata = {
  title: 'Oracles — on-chain price feeds for Stellar',
  description:
    'Reflector trio (DEX/CEX/FX), Redstone, Band — every Stellar oracle we cross-reference against the canonical Rates Engine VWAP.',
};

type OracleEntry = {
  name: string;
  source: string;
  type: string;
  blurb: string;
  notes: string[];
  discoveryDoc: string;
};

const ORACLES: OracleEntry[] = [
  {
    name: 'Reflector — DEX',
    source: 'reflector-dex',
    type: 'On-chain VWAP, SEP-40 contract',
    blurb:
      'On-chain DEX oracle reading Stellar order books. One of three Reflector contracts (DEX, CEX, FX) — they share interface but route different upstream feeds.',
    notes: [
      'No on-chain twap() or x_*() methods despite the proposal claim — we compute TWAP and cross-pair locally.',
      'Three separate contracts, not one. Each has its own price set.',
    ],
    discoveryDoc:
      '/research',
  },
  {
    name: 'Reflector — CEX',
    source: 'reflector-cex',
    type: 'CEX-fed oracle, SEP-40 contract',
    blurb:
      'Reflector CEX feed — pulls aggregated CEX prices on-chain. Excluded from our VWAP at aggregator time (would import their methodology).',
    notes: [
      'Reported alongside our prices but never priced in — the divergence worker uses it as a sanity reference, not an input.',
    ],
    discoveryDoc:
      '/research',
  },
  {
    name: 'Reflector — FX',
    source: 'reflector-fx',
    type: 'FX-rate oracle, SEP-40 contract',
    blurb:
      'Fiat-cross feed for chained pairs (e.g. EUR/USD, GBP/USD). Contributes to triangulation when an asset trades against EURC, MXNe etc.',
    notes: [
      'Used during X2.5 forex-factor snap (chained-fiat aggregation) when no direct fiat market exists for the asset.',
    ],
    discoveryDoc:
      '/research',
  },
  {
    name: 'Redstone',
    source: 'redstone',
    type: 'Adapter contract emitting batch events',
    blurb:
      'Redstone Adapter does emit events — single batched WritePrices push containing every updated feed in one transaction.',
    notes: [
      'WritePrices body has prices + timestamps but NOT feed_ids. Feed IDs live in the InvokeContract op args (write_prices(updater, feed_ids, payload)).',
      'Adapter zip-merges feed_ids and updated_feeds when their lengths match; otherwise the whole event is skipped (ErrFeedIDCountMismatch).',
    ],
    discoveryDoc:
      '/research',
  },
  {
    name: 'Band',
    source: 'band',
    type: 'Soroban contract — operation-args ingest',
    blurb:
      "Band's Soroban contract emits ZERO events. We observe the relay()/force_relay() InvokeContract calls instead — same dispatcher hook used by any future event-less storage-mutating Soroban contract.",
    notes: [
      'Band stores pair rates at E18 scale; relayed single-asset rates at E9. Decoder normalises to canonical fixed-point.',
      "Topic-match alone never fires Band — match by (contract_id, function_name) using the dispatcher's ContractCallDecoder interface.",
    ],
    discoveryDoc:
      '/research',
  },
];

export default function OraclesPage() {
  return (
    <div className="mx-auto max-w-6xl space-y-6 p-6">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Oracles</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Every on-chain Stellar oracle we ingest and cross-reference.
          Oracles are reported alongside our independent VWAP but never
          included in it — mixing them would import their methodology,
          and would double-count whichever upstream markets they read.
        </p>
      </header>

      <Panel
        title="SEP-40 compatibility"
        hint="Drop-in oracle interface"
        source={asExample('/v1/oracle/lastprice', { asset: 'native' })}
        bodyClassName="space-y-2 text-sm text-slate-600 dark:text-slate-400"
      >
        <p>
          We expose three SEP-40 endpoints —{' '}
          <code className="font-mono text-xs">/v1/oracle/lastprice</code>,{' '}
          <code className="font-mono text-xs">/v1/oracle/prices</code>,{' '}
          <code className="font-mono text-xs">/v1/oracle/x_last_price</code>
          — that match the SEP-40 contract trait on-chain consumers
          already integrate against.
        </p>
        <p>
          Routing your existing on-chain{' '}
          <code className="font-mono text-xs">lastprice()</code> calls
          through Rates Engine swaps in independent VWAP-backed prices
          without touching the calling contract.
        </p>
      </Panel>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {ORACLES.map((o) => (
          <OracleCard key={o.source} entry={o} />
        ))}
      </div>

      <Panel
        title="Divergence monitoring"
        hint="Cross-check vs the canonical VWAP"
        source={asExample('/v1/oracle/latest', { asset: 'native' })}
        bodyClassName="space-y-2 text-sm text-slate-600 dark:text-slate-400"
      >
        <p>
          The divergence worker continuously compares each oracle&apos;s
          published price against our computed VWAP. A persistent gap
          flips{' '}
          <code className="font-mono text-xs">flags.divergence_warning</code>{' '}
          on the canonical{' '}
          <Link
            href="/assets"
            className="underline decoration-dotted hover:text-brand-600"
          >
            coin pages
          </Link>{' '}
          and writes a row to{' '}
          <code className="font-mono text-xs">divergence_observations</code>
          {' '}for the historical trail.
        </p>
      </Panel>
    </div>
  );
}

function OracleCard({ entry }: { entry: OracleEntry }) {
  return (
    <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-800 dark:bg-slate-900">
      <div className="space-y-1">
        <h2 className="text-lg font-semibold tracking-tight">{entry.name}</h2>
        <p className="text-xs uppercase tracking-wider text-slate-500">
          {entry.type}
        </p>
      </div>
      <p className="mt-3 text-sm text-slate-700 dark:text-slate-300">
        {entry.blurb}
      </p>
      <ul className="mt-3 space-y-1.5 text-xs text-slate-600 dark:text-slate-400">
        {entry.notes.map((n, i) => (
          <li key={i} className="flex gap-2">
            <span className="text-slate-400">•</span>
            <span>{n}</span>
          </li>
        ))}
      </ul>
      <div className="mt-4 flex flex-wrap gap-3 text-xs">
        <a
          href={entry.discoveryDoc}
          className="inline-flex items-center gap-1 text-brand-600 hover:underline"
          target="_blank"
          rel="noreferrer"
        >
          Discovery notes
          <ExternalLink className="h-3 w-3" />
        </a>
      </div>
    </div>
  );
}
