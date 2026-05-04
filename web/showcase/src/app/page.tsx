export default function HomePage() {
  return (
    <main className="mx-auto max-w-4xl p-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">
          Rates Engine — Stellar pricing explorer
        </h1>
        <p className="text-slate-600 dark:text-slate-400">
          Skeleton placeholder. Full landing page lands in Phase 7 of{' '}
          <a
            href="https://github.com/RatesEngine/rates-engine/blob/main/docs/architecture/showcase-site-implementation-plan.md"
            className="underline decoration-dotted underline-offset-4 hover:text-brand-600"
          >
            the implementation plan
          </a>
          .
        </p>
      </header>
      <section className="mt-8 grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Card href="/coins" title="Coins" />
        <Card href="/markets" title="Markets" />
        <Card href="/dexes" title="DEXes" />
        <Card href="/lending" title="Lending" />
        <Card href="/aggregators" title="Aggregators" />
        <Card href="/oracles" title="Oracles" />
        <Card href="/network" title="Network" />
        <Card href="/research" title="Research" />
      </section>
      <footer className="mt-12 text-xs text-slate-500">
        API:{' '}
        <a
          href="https://api.ratesengine.net"
          className="font-mono hover:text-slate-700"
        >
          api.ratesengine.net
        </a>
        {' · '}
        Docs:{' '}
        <a href="/docs" className="hover:text-slate-700">
          /docs
        </a>
      </footer>
    </main>
  );
}

function Card({ href, title }: { href: string; title: string }) {
  return (
    <a
      href={href}
      className="rounded-lg border border-slate-200 p-4 hover:border-brand-500 hover:bg-brand-50 dark:border-slate-800 dark:hover:bg-slate-900"
    >
      <h2 className="font-medium">{title}</h2>
      <p className="text-xs text-slate-500">Coming soon</p>
    </a>
  );
}
