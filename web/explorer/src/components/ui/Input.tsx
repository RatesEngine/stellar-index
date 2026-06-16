import type { ComponentProps, ReactNode } from 'react';

import { cn } from '@/lib/cn';

const fieldBase =
  'w-full rounded-lg border border-line-strong bg-surface px-3 text-sm text-ink shadow-xs transition-colors placeholder:text-ink-faint focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-600/30 disabled:cursor-not-allowed disabled:bg-surface-subtle disabled:opacity-70';

export function Input({ className, ...props }: ComponentProps<'input'>) {
  return <input className={cn(fieldBase, 'h-9', className)} {...props} />;
}

export function Textarea({ className, ...props }: ComponentProps<'textarea'>) {
  return <textarea className={cn(fieldBase, 'py-2', className)} {...props} />;
}

export function Select({ className, ...props }: ComponentProps<'select'>) {
  return <select className={cn(fieldBase, 'h-9 pr-8', className)} {...props} />;
}

/** Field wraps a labelled control with optional hint + error text. */
export function Field({
  label,
  htmlFor,
  hint,
  error,
  required,
  children,
  className,
}: {
  label: ReactNode;
  htmlFor?: string;
  hint?: ReactNode;
  error?: ReactNode;
  required?: boolean;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn('space-y-1.5', className)}>
      <label htmlFor={htmlFor} className="block text-sm font-medium text-ink">
        {label}
        {required && <span className="ml-0.5 text-bad-500">*</span>}
      </label>
      {children}
      {error ? (
        <p className="text-xs text-bad-700">{error}</p>
      ) : hint ? (
        <p className="text-xs text-ink-muted">{hint}</p>
      ) : null}
    </div>
  );
}
