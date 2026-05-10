'use client';

import { Check, Code2, Copy, ExternalLink, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { twMerge } from 'tailwind-merge';

import type { RequestExample } from '@/api/client';

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
        'inline-flex items-center gap-1 rounded-md border border-slate-200 bg-white px-1.5 py-0.5 text-[10px] font-medium text-slate-500 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-400',
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
          className="fixed inset-0 z-50 flex items-end justify-center bg-slate-900/60 p-4 sm:items-center"
          onClick={() => setOpen(false)}
          role="dialog"
          aria-modal
        >
          <div
            className="w-full max-w-2xl rounded-lg bg-white p-6 shadow-2xl dark:bg-slate-900"
            onClick={(e) => e.stopPropagation()}
          >
            <header className="mb-4 flex items-center justify-between">
              <h3 className="font-medium">API request</h3>
              <button
                type="button"
                onClick={() => setOpen(false)}
                className="text-slate-400 hover:text-slate-700 dark:hover:text-slate-200"
                aria-label="Close"
              >
                <X className="h-4 w-4" />
              </button>
            </header>

            <div className="space-y-4">
              <Block label="cURL">
                <pre className="overflow-x-auto whitespace-pre rounded-md bg-slate-100 p-3 font-mono text-xs leading-relaxed dark:bg-slate-800">
                  {curl}
                </pre>
                <CopyButton
                  text={curl}
                  onCopy={() => setCopied('curl')}
                  copied={copied === 'curl'}
                />
              </Block>

              <Block label="URL">
                <code className="break-all text-xs text-slate-700 dark:text-slate-300">
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
                    className="inline-flex items-center gap-1 rounded border border-slate-200 px-2 py-1 text-xs text-slate-600 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:text-slate-300"
                  >
                    <ExternalLink className="h-3 w-3" />
                    Open
                  </a>
                </div>
              </Block>

              <p className="text-[11px] text-slate-500">
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
      <div className="mb-1 text-[11px] font-medium uppercase tracking-wider text-slate-500">
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
      className="mt-2 inline-flex items-center gap-1 rounded border border-slate-200 px-2 py-1 text-xs text-slate-600 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:text-slate-300"
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
