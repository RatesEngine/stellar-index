'use client';

import { Check, Code2, Copy, ExternalLink, X } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import { twMerge } from 'tailwind-merge';

import type { RequestExample } from '@/api/client';
import { useDialog } from '@/lib/useDialog';

export type RequestRevealProps = {
  example: RequestExample;
  /** Position the trigger button — top-right is the canonical place. */
  position?: 'top-right' | 'inline';
  className?: string;
};

/**
 * `<>` reveal — every panel exposes its underlying API request.
 *
 * Per data-inventory §3 + §6.10 every panel on the showcase must
 * carry one of these. Click → tray slides in showing the cURL form
 * + a copy button + a link to the live URL. `Cmd-/` toggles every
 * reveal on the page (handled by the surrounding KeyboardShortcuts
 * component once we add it).
 */
export function RequestReveal({
  example,
  position = 'top-right',
  className,
}: RequestRevealProps) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState<'curl' | 'url' | null>(null);
  // LC-050: Escape + focus-trap + focus move-in/restore for the dialog.
  const close = useCallback(() => setOpen(false), []);
  const dialogRef = useDialog<HTMLDivElement>(open, close);

  // Reset the "Copied!" indicator after a short pause so multiple
  // copies in quick succession each show the green check.
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(null), 1400);
    return () => clearTimeout(t);
  }, [copied]);

  const curl = renderCurl(example);

  const trigger = (
    <button
      type="button"
      onClick={() => setOpen(true)}
      className={twMerge(
        'inline-flex items-center gap-1 rounded-md border border-line bg-surface px-1.5 py-0.5 text-[10px] font-medium text-ink-muted hover:border-brand-500 hover:text-brand-600',
        position === 'top-right' && 'absolute right-2 top-2',
        className,
      )}
      aria-label="Show API request"
      title="Show API request"
    >
      <Code2 className="h-3 w-3" aria-hidden />
    </button>
  );

  return (
    <>
      {trigger}
      {open && (
        <div
          className="fixed inset-0 z-50 flex items-end justify-center bg-ink/60 p-4 sm:items-center"
          onClick={() => setOpen(false)}
        >
          <div
            ref={dialogRef}
            tabIndex={-1}
            role="dialog"
            aria-modal="true"
            aria-labelledby="request-reveal-title"
            className="w-full max-w-2xl rounded-lg bg-surface p-6 shadow-2xl outline-none"
            onClick={(e) => e.stopPropagation()}
          >
            <header className="mb-4 flex items-center justify-between">
              <h3 id="request-reveal-title" className="font-medium">
                API request
              </h3>
              <button
                type="button"
                onClick={() => setOpen(false)}
                className="text-ink-faint hover:text-ink-body"
                aria-label="Close"
              >
                <X className="h-4 w-4" />
              </button>
            </header>

            <div className="space-y-4">
              <Block label="cURL">
                <pre className="overflow-x-auto whitespace-pre rounded-md bg-surface-subtle p-3 font-mono text-xs leading-relaxed">
                  {curl}
                </pre>
                <CopyButton
                  text={curl}
                  onCopy={() => setCopied('curl')}
                  copied={copied === 'curl'}
                />
              </Block>

              <Block label="URL">
                <code className="break-all text-xs text-ink-body">
                  {example.url}
                </code>
                <div className="mt-2 flex gap-2">
                  <CopyButton
                    text={example.url}
                    onCopy={() => setCopied('url')}
                    copied={copied === 'url'}
                  />
                  <a
                    href={example.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1 rounded border border-line px-2 py-1 text-xs text-ink-body hover:border-brand-500 hover:text-brand-600"
                  >
                    <ExternalLink className="h-3 w-3" />
                    Open
                  </a>
                </div>
              </Block>

              <p className="text-[11px] text-ink-muted">
                Anonymous tier — no auth required for this endpoint.
              </p>
            </div>
          </div>
        </div>
      )}
    </>
  );
}

function Block({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 text-[11px] font-medium uppercase tracking-wider text-ink-muted">
        {label}
      </div>
      {children}
    </div>
  );
}

function CopyButton({
  text,
  onCopy,
  copied,
}: {
  text: string;
  onCopy: () => void;
  copied: boolean;
}) {
  return (
    <button
      type="button"
      onClick={async () => {
        await navigator.clipboard.writeText(text);
        onCopy();
      }}
      className="mt-2 inline-flex items-center gap-1 rounded border border-line px-2 py-1 text-xs text-ink-body hover:border-brand-500 hover:text-brand-600"
    >
      {copied ? (
        <>
          <Check className="h-3 w-3 text-up-strong" />
          Copied
        </>
      ) : (
        <>
          <Copy className="h-3 w-3" />
          Copy
        </>
      )}
    </button>
  );
}

function renderCurl(example: RequestExample): string {
  const headerArgs = Object.entries(example.headers ?? {})
    .map(([k, v]) => `  -H '${k}: ${v}'`)
    .join(' \\\n');
  return [
    `curl -fsSL '${example.url}'`,
    headerArgs,
  ]
    .filter(Boolean)
    .join(' \\\n');
}
