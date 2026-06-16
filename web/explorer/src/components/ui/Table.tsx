import type { ComponentProps } from 'react';

import { cn } from '@/lib/cn';

/**
 * Table primitives — a clean, hairline-ruled data table. Numbers should use
 * `align="right"` + the tnum class (Td applies tnum on right-aligned cells).
 * Wrap in TableWrap for horizontal scroll on narrow viewports.
 */
export function TableWrap({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      className={cn('overflow-x-auto rounded-card border border-line bg-surface', className)}
      {...props}
    />
  );
}

export function Table({ className, ...props }: ComponentProps<'table'>) {
  return <table className={cn('w-full border-collapse text-sm', className)} {...props} />;
}

export function THead({ className, ...props }: ComponentProps<'thead'>) {
  return (
    <thead
      className={cn(
        'border-b border-line bg-surface-muted text-left text-[11px] font-medium uppercase tracking-wider text-ink-muted',
        className,
      )}
      {...props}
    />
  );
}

export function TBody({ className, ...props }: ComponentProps<'tbody'>) {
  return <tbody className={cn('divide-y divide-line', className)} {...props} />;
}

export function TR({ className, ...props }: ComponentProps<'tr'>) {
  return <tr className={cn('transition-colors hover:bg-surface-muted/70', className)} {...props} />;
}

type CellProps = ComponentProps<'td'> & { align?: 'left' | 'right' | 'center' };

export function Th({ align = 'left', className, ...props }: ComponentProps<'th'> & { align?: 'left' | 'right' | 'center' }) {
  return (
    <th
      scope="col"
      className={cn(
        'whitespace-nowrap px-4 py-2.5 font-medium',
        align === 'right' && 'text-right',
        align === 'center' && 'text-center',
        className,
      )}
      {...props}
    />
  );
}

export function Td({ align = 'left', className, ...props }: CellProps) {
  return (
    <td
      className={cn(
        'px-4 py-3 text-ink-body',
        align === 'right' && 'text-right tnum',
        align === 'center' && 'text-center',
        className,
      )}
      {...props}
    />
  );
}
