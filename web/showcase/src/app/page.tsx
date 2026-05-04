import { Panel } from '@/components/reveal';
import {
  MultiWindowDelta,
  Sparkline,
} from '@/components/primitives';
import { asExample } from '@/api/client';
import { NetworkLivePanel, SystemHealthLivePanel } from './HomeLivePanels';

export default function HomePage() {
  return (
    <div className="mx-auto max-w-6xl space-y-8 p-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">
          Stellar pricing explorer
        </h1>
        <p className="text-slate-600 dark:text-slate-400">
          Independent, comprehensive market data across every asset
          on Stellar — on-chain DEXes, classic SDEX, and major
          exchanges, all in one VWAP. Browse below; every panel
          shows the API call that produced it.
        </p>
      </header>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <NetworkLivePanel />

        <Panel
          title="Top movers — 24h"
          source={asExample('/v1/coins', { sort: 'delta_24h:desc', limit: 5 })}
        >
          <div className="text-xs text-slate-500">
            Real data lands when the change-summary worker has 24h
            of history. Stub layout for now.
          </div>
          <div className="mt-3 space-y-2">
            <Row label="XLM" value="$0.1234" delta={3.21} />
            <Row label="AQUA" value="$0.0042" delta={8.12} />
            <Row label="USDC" value="$1.0001" delta={0.02} />
          </div>
        </Panel>

        <SystemHealthLivePanel />
      </div>

      <Panel
        title="Sample composite"
        hint="every panel composes the design primitives"
        source={asExample('/v1/coins/stellar/usdc')}
        bodyClassName="space-y-4"
      >
        <div className="flex flex-wrap items-center gap-4">
          <span className="text-xl font-bold">XLM/USDC</span>
          <span className="text-xl font-mono tabular-nums">$0.1234</span>
          <Sparkline values={[0.115, 0.118, 0.12, 0.119, 0.122, 0.121, 0.1234]} width={120} height={32} />
          <MultiWindowDelta
            windows={[
              { label: '1h', deltaPct: 0.5 },
              { label: '24h', deltaPct: 3.2 },
              { label: '7d', deltaPct: -1.1 },
              { label: '30d', deltaPct: 18.4 },
            ]}
          />
        </div>
        <div className="text-xs text-slate-500">
          The full landing experience comes in subsequent PRs;
          today this composite is the proof that the primitives +
          panel chrome compose.
        </div>
      </Panel>
    </div>
  );
}

function Row({
  label,
  value,
  delta,
}: {
  label: string;
  value: string;
  delta: number;
}) {
  return (
    <div className="flex items-center justify-between">
      <span className="font-medium">{label}</span>
      <span className="font-mono tabular-nums text-slate-600 dark:text-slate-300">
        {value}
      </span>
      <span
        className={
          delta > 0
            ? 'text-up-strong'
            : delta < 0
              ? 'text-down-strong'
              : 'text-slate-500'
        }
      >
        {delta > 0 ? '+' : ''}
        {delta.toFixed(2)}%
      </span>
    </div>
  );
}

