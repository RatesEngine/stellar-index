import { twMerge } from 'tailwind-merge';
import type { RequestExample } from '@/api/client';
import { RequestReveal } from './RequestReveal';

export type PanelProps = {
  /** Title rendered in the panel header. Optional — some panels render their own. */
  title?: string;
  /**
   * Sub-label / context shown next to the title. Strings are the
   * common case; React nodes allow inline components (e.g. live-
   * updating freshness chips).
   */
  hint?: React.ReactNode;
  /** API request that produced this panel's data. */
  source?: RequestExample;
  /** Anchor id for deep-linking (e.g. `#confidence-card`). */
  panelId?: string;
  className?: string;
  bodyClassName?: string;
  children: React.ReactNode;
};

/**
 * Panel — every visible card on the showcase composes one of these.
 * Provides:
 *   - Optional title + hint
 *   - The `<>` reveal tucked top-right
 *   - An anchor id so the article system (Phase 12) can deep-link
 *     `<RatesPanel anchorId="confidence-card" />` into the page.
 *
 * The component is intentionally thin — it renders chrome around
 * `children` and never touches data.
 */
export function Panel({
  title,
  hint,
  source,
  panelId,
  className,
  bodyClassName,
  children,
}: PanelProps) {
  return (
    <section
      id={panelId}
      className={twMerge(
        'relative rounded-lg border border-slate-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900',
        className,
      )}
    >
      {(title || source) && (
        <header className="mb-3 flex items-start justify-between gap-2">
          {title && (
            <div>
              <h3 className="text-sm font-medium">{title}</h3>
              {hint && (
                <p className="text-xs text-slate-500 dark:text-slate-400">
                  {hint}
                </p>
              )}
            </div>
          )}
          {source && <RequestReveal example={source} position="inline" />}
        </header>
      )}
      <div className={twMerge('text-sm', bodyClassName)}>{children}</div>
    </section>
  );
}
