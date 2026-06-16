import type { ComponentProps, ReactNode } from 'react';

import { cn } from '@/lib/cn';

type CardProps = ComponentProps<'div'> & {
  /** `flat` drops the shadow (for nested cards / dense grids). */
  flat?: boolean;
  /** `interactive` adds a hover lift — use for clickable cards. */
  interactive?: boolean;
};

/**
 * Card is the base surface primitive — a white panel on the canvas with a
 * hairline border and a soft shadow. The whole design leans on these +
 * whitespace rather than heavy chrome. Compose with CardHeader / CardBody.
 */
export function Card({
  flat,
  interactive,
  className,
  children,
  ...props
}: CardProps) {
  return (
    <div
      className={cn(
        'rounded-card border border-line bg-surface',
        flat ? 'shadow-none' : 'shadow-card',
        interactive &&
          'transition-shadow duration-150 hover:border-line-strong hover:shadow-elevated',
        className,
      )}
      {...props}
    >
      {children}
    </div>
  );
}

type CardHeaderProps = {
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  /** An eyebrow/kicker label above the title. */
  eyebrow?: ReactNode;
  className?: string;
};

export function CardHeader({
  title,
  description,
  actions,
  eyebrow,
  className,
}: CardHeaderProps) {
  return (
    <div
      className={cn(
        'flex items-start justify-between gap-4 border-b border-line px-5 py-4',
        className,
      )}
    >
      <div className="min-w-0">
        {eyebrow && (
          <div className="mb-1 text-[11px] font-medium uppercase tracking-wider text-ink-faint">
            {eyebrow}
          </div>
        )}
        {title && (
          <h3 className="truncate text-[15px] font-semibold text-ink">
            {title}
          </h3>
        )}
        {description && (
          <p className="mt-0.5 text-sm text-ink-muted">{description}</p>
        )}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  );
}

export function CardBody({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('p-5', className)} {...props} />;
}

export function CardFooter({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      className={cn(
        'flex items-center justify-between gap-3 border-t border-line px-5 py-3 text-sm text-ink-muted',
        className,
      )}
      {...props}
    />
  );
}
