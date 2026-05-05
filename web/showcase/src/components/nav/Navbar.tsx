import Link from 'next/link';
import { TrendingUp } from 'lucide-react';

import { SearchModal } from './SearchModal';

/**
 * Top navigation. Section-grouped per data-inventory §4 + the
 * "split protocols by kind" decision: DEXes / Lending / Aggregators
 * / Oracles each as their own top-level entry. Operator views live
 * under a "More" submenu (footer-only at v1).
 *
 * Server-rendered. Active-route highlighting comes once we wire
 * `usePathname` (which would force this to a client component); for
 * the static-export build we keep it pure SSR.
 */
export function Navbar() {
  return (
    <nav className="border-b border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-950">
      <div className="mx-auto flex max-w-7xl items-center justify-between px-6 py-3">
        <Link
          href="/"
          className="flex items-center gap-2 text-sm font-semibold tracking-tight"
        >
          <TrendingUp className="h-5 w-5 text-brand-500" />
          <span>Rates Engine</span>
        </Link>
        <div className="hidden items-center gap-1 text-sm sm:flex">
          {SECTIONS.map((s) => (
            <Link
              key={s.href}
              href={s.href}
              className="rounded-md px-3 py-1.5 text-slate-600 hover:bg-slate-100 hover:text-brand-600 dark:text-slate-300 dark:hover:bg-slate-800"
            >
              {s.label}
            </Link>
          ))}
          <SearchModal />
          <Link
            href="/status"
            className="ml-2 inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-slate-600 hover:bg-slate-100 hover:text-brand-600 dark:text-slate-300 dark:hover:bg-slate-800"
          >
            <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" aria-hidden />
            Status
          </Link>
          <Link
            href="/signup"
            className="ml-2 rounded-md bg-brand-600 px-3 py-1.5 font-medium text-white hover:bg-brand-700"
          >
            Sign up
          </Link>
        </div>
      </div>
    </nav>
  );
}

const SECTIONS = [
  { label: 'Coins', href: '/coins' },
  { label: 'Markets', href: '/markets' },
  { label: 'DEXes', href: '/dexes' },
  { label: 'Lending', href: '/lending' },
  { label: 'Aggregators', href: '/aggregators' },
  { label: 'Oracles', href: '/oracles' },
  { label: 'Network', href: '/network' },
  { label: 'Research', href: '/research' },
  { label: 'Docs', href: '/docs' },
];
