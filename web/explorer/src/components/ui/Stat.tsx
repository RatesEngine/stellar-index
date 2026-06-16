import type { ReactNode } from 'react';

import { cn } from '@/lib/cn';

type StatProps = {
  label: ReactNode;
  value: ReactNode;
  /** Optional supporting line under the value (e.g. a delta or unit). */
  sub?: ReactNode;
  /** Optional leading icon. */
  icon?: ReactNode;
  /** Tighter type for dense grids. */
  size?: 'md' | 'lg';
  className?: string;
};

/**
 * Stat is the canonical metric display: a small uppercase label over a large
 * tabular-figure value, with an optional sub line. Used across the home strip,
 * dashboard, protocol pages, and diagnostics.
 */
export function Stat({ label, value, sub, icon, size = 'md', className }: StatProps) {
  return (
    <div className={cn('min-w-0', className)}>
      <div className="flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-wider text-ink-muted">
        {icon && <span className="text-ink-faint">{icon}</span>}
        <span className="truncate">{label}</span>
      </div>
      <div
        className={cn(
          'mt-1 font-semibold tracking-tight tnum text-ink',
          size === 'lg' ? 'text-3xl' : 'text-2xl',
        )}
      >
        {value}
      </div>
      {sub && <div className="mt-0.5 text-sm text-ink-muted tnum">{sub}</div>}
    </div>
  );
}

/** A grid of stats with hairline dividers — the standard metric strip. */
export function StatGrid({
  children,
  cols = 4,
  className,
}: {
  children: ReactNode;
  cols?: 2 | 3 | 4 | 5;
  className?: string;
}) {
  const colClass = {
    2: 'sm:grid-cols-2',
    3: 'sm:grid-cols-3',
    4: 'sm:grid-cols-2 lg:grid-cols-4',
    5: 'sm:grid-cols-3 lg:grid-cols-5',
  }[cols];
  return (
    <div className={cn('grid grid-cols-1 gap-px overflow-hidden rounded-card border border-line bg-line', colClass, className)}>
      {children}
    </div>
  );
}

/** A single cell inside a StatGrid (white background, padded). */
export function StatCell({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('bg-surface p-5', className)}>{children}</div>;
}
