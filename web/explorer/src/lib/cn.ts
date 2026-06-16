import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

/**
 * cn merges Tailwind class strings with conflict resolution. clsx handles
 * conditional/array/object class inputs; twMerge dedupes conflicting Tailwind
 * utilities (the last wins), so callers can layer overrides on a base class
 * without specificity fights. This is the single class-composition helper for
 * the whole design system — every UI primitive uses it.
 *
 *   cn('px-3 py-2', isActive && 'bg-brand-600', className)
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
