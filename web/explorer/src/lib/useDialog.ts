'use client';

import { useEffect, useRef } from 'react';

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'textarea:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

/**
 * useDialog wires the WCAG modal-dialog contract that every reveal/drawer was
 * missing (LC-050 / LC-051):
 *
 *  - **Escape** closes the dialog.
 *  - **Focus moves in** when it opens (first focusable, else the container).
 *  - **Focus is trapped** — Tab / Shift-Tab wrap within the dialog.
 *  - **Focus is restored** to whatever was focused before (the trigger) on close.
 *
 * Attach the returned ref to the dialog container (give it `tabIndex={-1}` so it
 * can receive focus, `role="dialog"` and `aria-modal="true"`). `onClose` MUST be
 * stable (wrap in useCallback) or the effect re-runs and re-steals focus.
 */
export function useDialog<T extends HTMLElement>(
  open: boolean,
  onClose: () => void,
) {
  const ref = useRef<T>(null);
  const restoreRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!open) return;

    const node = ref.current;
    restoreRef.current = (document.activeElement as HTMLElement) ?? null;

    const focusables = (): HTMLElement[] =>
      node
        ? Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
            (el) => el.offsetParent !== null,
          )
        : [];

    // Move focus into the dialog.
    (focusables()[0] ?? node)?.focus();

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
        return;
      }
      if (e.key !== 'Tab' || !node) return;
      const items = focusables();
      if (items.length === 0) {
        e.preventDefault();
        node.focus();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      const active = document.activeElement;
      if (e.shiftKey && (active === first || active === node)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && active === last) {
        e.preventDefault();
        first.focus();
      }
    };

    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      restoreRef.current?.focus?.();
    };
  }, [open, onClose]);

  return ref;
}
