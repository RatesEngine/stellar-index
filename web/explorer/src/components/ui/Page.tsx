import Link from 'next/link';
import type { ComponentProps, ReactNode } from 'react';

import { cn } from '@/lib/cn';

/** The standard content frame: centered, max-w-page, responsive padding. */
export function Container({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div className={cn('mx-auto w-full max-w-page px-4 sm:px-6', className)} {...props} />
  );
}

/** Vertical section rhythm — generous, consistent whitespace between blocks. */
export function Section({ className, ...props }: ComponentProps<'section'>) {
  return <section className={cn('py-6 sm:py-8', className)} {...props} />;
}

export type Crumb = { label: string; href?: string };

/**
 * PageHeader is the consistent top-of-page block: optional breadcrumb +
 * eyebrow, an h1 title, a description, and a right-aligned actions slot.
 */
export function PageHeader({
  title,
  description,
  eyebrow,
  breadcrumbs,
  actions,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  eyebrow?: ReactNode;
  breadcrumbs?: Crumb[];
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn('flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between', className)}>
      <div className="min-w-0">
        {breadcrumbs && breadcrumbs.length > 0 && <Breadcrumbs items={breadcrumbs} />}
        {eyebrow && (
          <div className="mb-1.5 text-xs font-medium uppercase tracking-wider text-brand-600">
            {eyebrow}
          </div>
        )}
        <h1 className="text-h1 font-semibold text-ink">{title}</h1>
        {description && (
          <p className="mt-2 max-w-prose text-[15px] leading-relaxed text-ink-muted">
            {description}
          </p>
        )}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Breadcrumbs({ items }: { items: Crumb[] }) {
  return (
    <nav aria-label="Breadcrumb" className="mb-2 flex flex-wrap items-center gap-1.5 text-xs text-ink-muted">
      {items.map((c, i) => (
        <span key={`${c.label}-${i}`} className="flex items-center gap-1.5">
          {i > 0 && <span aria-hidden className="text-ink-faint">/</span>}
          {c.href ? (
            <Link href={c.href} className="transition-colors hover:text-brand-600">
              {c.label}
            </Link>
          ) : (
            <span className="text-ink-body">{c.label}</span>
          )}
        </span>
      ))}
    </nav>
  );
}

/** A lightweight section heading used inside pages (between cards). */
export function SectionHeader({
  title,
  description,
  actions,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn('mb-4 flex items-end justify-between gap-4', className)}>
      <div className="min-w-0">
        <h2 className="text-h3 font-semibold text-ink">{title}</h2>
        {description && <p className="mt-1 text-sm text-ink-muted">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  );
}
