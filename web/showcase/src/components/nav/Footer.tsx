import Link from 'next/link';

/**
 * Footer with the operator/researcher views the navbar doesn't
 * top-level — diagnostics, anomalies, divergences, MEV, sources,
 * status. Per impl plan §17 these are useful but not load-bearing
 * for typical browse traffic; tucking them in the footer keeps the
 * navbar focused on "browse Stellar's market."
 */
export function Footer() {
  return (
    <footer className="mt-16 border-t border-slate-200 bg-white py-8 dark:border-slate-800 dark:bg-slate-950">
      <div className="mx-auto max-w-7xl px-6 text-xs text-slate-500">
        <div className="grid grid-cols-2 gap-8 sm:grid-cols-4">
          <FooterColumn
            title="Browse"
            links={[
              { label: 'Coins', href: '/coins' },
              { label: 'Markets', href: '/markets' },
              { label: 'Issuers', href: '/issuers' },
              { label: 'Sources', href: '/sources' },
            ]}
          />
          <FooterColumn
            title="Protocols"
            links={[
              { label: 'DEXes', href: '/dexes' },
              { label: 'Lending', href: '/lending' },
              { label: 'Aggregators', href: '/aggregators' },
              { label: 'Oracles', href: '/oracles' },
            ]}
          />
          <FooterColumn
            title="Forensics"
            links={[
              { label: 'Anomalies', href: '/anomalies' },
              { label: 'Divergences', href: '/divergences' },
              { label: 'MEV', href: '/mev' },
              { label: 'Network', href: '/network' },
            ]}
          />
          <FooterColumn
            title="System"
            links={[
              { label: 'Sign up', href: '/signup' },
              { label: 'Account', href: '/account' },
              { label: 'Status', href: '/status' },
              { label: 'Diagnostics', href: '/diagnostics' },
              { label: 'API docs', href: '/docs' },
              { label: 'Research', href: '/research' },
            ]}
          />
        </div>
        <div className="mt-8 flex flex-wrap items-center justify-between gap-3 border-t border-slate-200 pt-4 dark:border-slate-800">
          <div className="flex flex-wrap items-center gap-4">
            <span>
              API:{' '}
              <a
                href="https://api.ratesengine.net"
                className="font-mono hover:text-slate-700 dark:hover:text-slate-300"
              >
                api.ratesengine.net
              </a>
            </span>
            <a
              href="https://github.com/RatesEngine/rates-engine"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-slate-700 dark:hover:text-slate-300"
            >
              GitHub
            </a>
            <a
              href="https://github.com/RatesEngine/rates-engine/blob/main/CHANGELOG.md"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-slate-700 dark:hover:text-slate-300"
            >
              Changelog
            </a>
          </div>
          <span>Apache-2.0</span>
        </div>
      </div>
    </footer>
  );
}

function FooterColumn({
  title,
  links,
}: {
  title: string;
  links: { label: string; href: string; external?: boolean }[];
}) {
  return (
    <div className="space-y-2">
      <h4 className="text-[11px] font-medium uppercase tracking-wider text-slate-400">
        {title}
      </h4>
      <ul className="space-y-1">
        {links.map((l) =>
          l.external ? (
            <li key={l.href}>
              <a
                href={l.href}
                target="_blank"
                rel="noopener noreferrer"
                className="hover:text-slate-700 dark:hover:text-slate-300"
              >
                {l.label}
              </a>
            </li>
          ) : (
            <li key={l.href}>
              <Link
                href={l.href}
                className="hover:text-slate-700 dark:hover:text-slate-300"
              >
                {l.label}
              </Link>
            </li>
          ),
        )}
      </ul>
    </div>
  );
}
