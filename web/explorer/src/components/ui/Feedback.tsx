import type { ReactNode } from 'react';

import { cn } from '@/lib/cn';

/** EmptyState — centered icon + title + description for no-data surfaces. */
export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
}: {
  icon?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center rounded-card border border-dashed border-line-strong bg-surface-muted px-6 py-12 text-center',
        className,
      )}
    >
      {icon && (
        <div className="mb-3 flex h-11 w-11 items-center justify-center rounded-full bg-surface text-ink-faint ring-1 ring-line">
          {icon}
        </div>
      )}
      <h3 className="text-sm font-semibold text-ink">{title}</h3>
      {description && (
        <p className="mt-1 max-w-sm text-sm text-ink-muted">{description}</p>
      )}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

/** Skeleton — a pulsing placeholder block for loading states. */
export function Skeleton({ className }: { className?: string }) {
  return <div className={cn('animate-pulse rounded-md bg-surface-subtle', className)} />;
}

/** Inline alert / callout for info, warnings, and errors. */
export function Callout({
  tone = 'info',
  title,
  children,
  className,
}: {
  tone?: 'info' | 'warn' | 'bad' | 'ok';
  title?: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  const tones = {
    info: 'border-brand-200 bg-brand-50 text-brand-900',
    warn: 'border-warn-300 bg-warn-50 text-warn-900',
    bad: 'border-bad-300 bg-bad-50 text-bad-900',
    ok: 'border-ok-300 bg-ok-50 text-ok-700',
  }[tone];
  // LC-052: announce to assistive tech. bad/warn are errors → assertive
  // role=alert (interrupts); ok/info are status → polite. Callouts that
  // render dynamically after a form submit (sign-in, key create/revoke) are
  // now spoken instead of silently appearing.
  const urgent = tone === 'bad' || tone === 'warn';
  return (
    <div
      role={urgent ? 'alert' : 'status'}
      aria-live={urgent ? 'assertive' : 'polite'}
      className={cn('rounded-lg border px-4 py-3 text-sm', tones, className)}
    >
      {title && <div className="mb-0.5 font-semibold">{title}</div>}
      {children}
    </div>
  );
}
