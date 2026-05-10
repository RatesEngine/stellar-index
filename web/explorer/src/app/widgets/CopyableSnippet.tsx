'use client';

import { Check, Copy } from 'lucide-react';
import { useState } from 'react';

/**
 * Snippet block with a Copy button. Lifted out of WidgetsPage so
 * the parent can stay a server component (file reads, no client
 * state) while just this island opts into the browser bundle.
 */
export function CopyableSnippet({ snippet }: { snippet: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="relative">
      <pre className="overflow-x-auto bg-slate-950 px-3 py-2.5 text-[11px] leading-5 text-slate-100">
        <code>{snippet}</code>
      </pre>
      <button
        type="button"
        aria-label="Copy snippet"
        onClick={() => {
          navigator.clipboard
            .writeText(snippet)
            .then(() => {
              setCopied(true);
              setTimeout(() => setCopied(false), 1500);
            })
            .catch(() => {});
        }}
        className="absolute right-2 top-2 rounded p-1 text-slate-400 hover:bg-slate-800 hover:text-slate-100"
      >
        {copied ? (
          <Check className="h-3.5 w-3.5 text-emerald-400" />
        ) : (
          <Copy className="h-3.5 w-3.5" />
        )}
      </button>
    </div>
  );
}
