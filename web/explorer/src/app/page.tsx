import Link from 'next/link';
import { ArrowRight, Activity } from 'lucide-react';

import { ButtonLink, Container } from '@/components/ui';
import { HomeBlogStrip } from './HomeBlogStrip';
import { HomeCurrencies } from './HomeCurrencies';
import { HomeHeroChart } from './HomeHeroChart';
import { NetworkLivePanel, SystemHealthLivePanel } from './HomeLivePanels';
import { HomeNetworkStrip } from './HomeNetworkStrip';
import { HomeRecentChanges } from './HomeRecentChanges';
import { HomeRecentTrades } from './HomeRecentTrades';
import { HomeTopAssets } from './HomeTopAssets';
import { HomeTopMarkets } from './HomeTopMarkets';
import { HomeTopMovers } from './HomeTopMovers';
import { HomeTryAPI } from './HomeTryAPI';

export default function HomePage() {
  return (
    <Container className="space-y-12 py-10 sm:py-14">
      <header className="max-w-3xl space-y-5">
        <p className="inline-flex items-center gap-2 rounded-full border border-line bg-surface px-3 py-1 text-xs font-medium text-ink-muted">
          <span className="h-1.5 w-1.5 rounded-full bg-up" />
          Independent · open · public-tier free
        </p>
        <h1 className="text-display-sm font-semibold text-ink md:text-display">
          The protocol explorer for the Stellar network.
        </h1>
        <p className="max-w-2xl text-lg leading-relaxed text-ink-muted">
          Every contract, every event, and every trade across Stellar
          protocols — CEXes, on-chain DEXes, and lending — served as verified
          per-protocol data plus a single VWAP price through a public REST
          API, alongside live world fiat rates. Every panel below shows the
          exact API call that produced it.
        </p>
        <div className="flex flex-wrap items-center gap-3 pt-1">
          <ButtonLink href="/assets" size="lg">
            Browse assets
            <ArrowRight className="h-4 w-4" />
          </ButtonLink>
          <ButtonLink href="https://docs.stellarindex.io" variant="secondary" size="lg">
            API docs
          </ButtonLink>
          <Link
            href="/methodology"
            className="px-2 text-sm font-medium text-ink-muted transition-colors hover:text-brand-600"
          >
            How it works →
          </Link>
        </div>
      </header>

      <HomeNetworkStrip />

      <HomeHeroChart />

      <section className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <NetworkLivePanel />
        <SystemHealthLivePanel />
        <Link
          href="/diagnostics"
          className="group flex h-full flex-col justify-between rounded-card border border-line bg-surface p-5 shadow-card transition-all hover:border-line-strong hover:shadow-elevated"
        >
          <div>
            <p className="flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-wider text-ink-muted">
              <Activity className="h-3.5 w-3.5 text-ink-faint" />
              Diagnostics
            </p>
            <p className="mt-2 text-h3 font-semibold text-ink">
              Watch the indexer tick.
            </p>
            <p className="mt-1 text-sm text-ink-muted">
              Per-source ingest cursors, refreshed every 15 seconds — see
              every backfill chunk advance in real time.
            </p>
          </div>
          <p className="mt-4 inline-flex items-center gap-1 text-sm font-medium text-brand-600">
            Open diagnostics{' '}
            <ArrowRight className="h-3.5 w-3.5 transition-transform group-hover:translate-x-0.5" />
          </p>
        </Link>
      </section>

      <HomeTopAssets />

      <HomeCurrencies />

      <HomeTopMarkets />

      <HomeTopMovers />

      <HomeRecentTrades />

      <HomeRecentChanges />

      <HomeBlogStrip />

      <section className="space-y-4">
        <div className="space-y-1">
          <h2 className="text-h2 font-semibold text-ink">Try the API</h2>
          <p className="text-[15px] text-ink-muted">
            Public, no auth, no API key. Pick an example and paste it
            straight into a terminal.
          </p>
        </div>
        <HomeTryAPI />
      </section>
    </Container>
  );
}
