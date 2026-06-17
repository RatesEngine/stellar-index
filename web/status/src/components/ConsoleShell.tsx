'use client';

import { Menu, TrendingUp, X } from 'lucide-react';
import { useState, type ReactNode } from 'react';

import { Sidebar, SidebarNav } from './Sidebar';

const MAIN = process.env.NEXT_PUBLIC_SITE_URL ?? 'https://stellarindex.io';

/**
 * ConsoleShell — the exact same app frame as the explorer (persistent left
 * sidebar, no top bar), so the status page reads as part of the main product
 * even though it's on a subdomain. The status content renders in the main
 * column; the nav links back to the main site.
 */
export function ConsoleShell({ children }: { children: ReactNode }) {
  const [drawer, setDrawer] = useState(false);
  return (
    <div className="flex min-h-screen">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-14 items-center justify-between border-b border-line bg-surface px-4 lg:hidden">
          <a
            href={MAIN}
            className="flex items-center gap-2 text-sm font-semibold tracking-tight text-ink"
          >
            <span className="flex h-6 w-6 items-center justify-center rounded-md bg-brand-600 text-white">
              <TrendingUp className="h-3.5 w-3.5" />
            </span>
            Stellar Index
          </a>
          <button
            type="button"
            onClick={() => setDrawer(true)}
            aria-label="Open navigation"
            className="-mr-1 inline-flex items-center justify-center rounded-md p-2 text-ink-body hover:bg-surface-subtle"
          >
            <Menu className="h-5 w-5" />
          </button>
        </div>
        <main id="main" className="flex-1">
          {children}
        </main>
      </div>

      {drawer && (
        <div className="fixed inset-0 z-50 lg:hidden">
          <div
            className="absolute inset-0 bg-ink/30 backdrop-blur-sm"
            onClick={() => setDrawer(false)}
            aria-hidden
          />
          <div className="absolute left-0 top-0 h-full w-72 max-w-[85vw] border-r border-line shadow-elevated">
            <button
              type="button"
              onClick={() => setDrawer(false)}
              aria-label="Close navigation"
              className="absolute right-2 top-3 z-10 inline-flex items-center justify-center rounded-md p-2 text-ink-body hover:bg-surface-subtle"
            >
              <X className="h-5 w-5" />
            </button>
            <SidebarNav onNavigate={() => setDrawer(false)} />
          </div>
        </div>
      )}
    </div>
  );
}
