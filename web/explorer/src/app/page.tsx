import Link from 'next/link';
import { ArrowRight } from 'lucide-react';

import { NetworkLivePanel, SystemHealthLivePanel } from './HomeLivePanels';
import { HomeTopAssets } from './HomeTopAssets';
import { HomeTryAPI } from './HomeTryAPI';

export default function HomePage() {
  return (
    <div className="mx-auto max-w-7xl space-y-12 px-6 py-10">
      <header className="space-y-4 pb-2 pt-4">
        <p className="font-mono text-xs uppercase tracking-widest text-brand-600 dark:text-brand-400">
          Independent · open · public-tier free
        </p>
        <h1 className="text-4xl font-semibold tracking-tight md:text-5xl">
          Pricing for every asset on Stellar.
        </h1>
        <p className="max-w-2xl text-base text-slate-600 dark:text-slate-400 md:text-lg">
          Rates Engine ingests every trade on the Stellar network — on-chain
          DEXes, classic SDEX, and major exchanges — and serves a single
          VWAP through a public REST API. Every panel below shows the exact
          API call that produced it.
        </p>
        <div className="flex flex-wrap gap-3 pt-2">
          <Link
            href="/assets"
            className="inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-brand-700"
          >
            Browse assets
            <ArrowRight className="h-3.5 w-3.5" />
          </Link>
          <Link
            href="/markets"
            className="inline-flex items-center gap-1.5 rounded-md border border-slate-300 px-3.5 py-2 text-sm font-medium text-slate-700 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:text-slate-300"
          >
            Browse markets
          </Link>
          <Link
            href="/docs"
            className="inline-flex items-center gap-1.5 rounded-md border border-slate-300 px-3.5 py-2 text-sm font-medium text-slate-700 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:text-slate-300"
          >
            API docs
          </Link>
        </div>
      </header>

      <section className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <NetworkLivePanel />
        <SystemHealthLivePanel />
        <Link
          href="/diagnostics"
          className="flex h-full flex-col justify-between rounded-xl border border-slate-200 bg-white p-4 text-sm shadow-sm hover:border-brand-500 dark:border-slate-800 dark:bg-slate-900"
        >
          <div>
            <p className="text-[11px] font-medium uppercase tracking-wider text-slate-500">
              Diagnostics
            </p>
            <p className="mt-2 text-xl font-semibold tracking-tight">
              Watch the indexer tick.
            </p>
            <p className="mt-1 text-xs text-slate-500">
              Per-source ingest cursors, refreshed every 15 seconds. See
              every backfill chunk advance in real time.
            </p>
          </div>
          <p className="mt-3 inline-flex items-center gap-1 text-xs text-brand-600">
            Open diagnostics <ArrowRight className="h-3 w-3" />
          </p>
        </Link>
      </section>

      <HomeTopAssets />

      <section className="space-y-3">
        <div className="space-y-1">
          <h2 className="text-2xl font-semibold tracking-tight">
            Try the API
          </h2>
          <p className="text-sm text-slate-600 dark:text-slate-400">
            Public, no auth, no API key. Pick an example and paste it
            straight into a terminal.
          </p>
        </div>
        <HomeTryAPI />
      </section>
    </div>
  );
}
