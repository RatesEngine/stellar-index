'use client';

import { Check, Copy } from 'lucide-react';
import { useState } from 'react';

import { API_BASE_URL } from '@/api/client';

const EXAMPLES = [
  {
    label: 'Latest XLM/USDC price (VWAP)',
    cmd: (base: string) =>
      `curl ${base}/v1/price?asset=native\\&quote=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN`,
  },
  {
    label: 'Top-100 coins',
    cmd: (base: string) => `curl ${base}/v1/coins?limit=100`,
  },
  {
    label: 'Active markets (last 14d)',
    cmd: (base: string) => `curl ${base}/v1/markets?limit=20`,
  },
  {
    label: 'Per-source ingest cursors',
    cmd: (base: string) => `curl ${base}/v1/diagnostics/cursors`,
  },
];

/**
 * Inline "Try the API" panel for the home page. Tabs through a few
 * canonical examples — copy-button on each so a visitor can paste
 * straight into a terminal. The base URL adapts to whatever the
 * site is configured against (production, r1 dev, local).
 */
export function HomeTryAPI() {
  const [activeIx, setActiveIx] = useState(0);
  const [copied, setCopied] = useState(false);
  const cmd = EXAMPLES[activeIx]!.cmd(API_BASE_URL);

  return (
    <div className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm dark:border-slate-800 dark:bg-slate-900">
      <div className="mb-3 flex flex-wrap gap-1">
        {EXAMPLES.map((ex, i) => (
          <button
            key={ex.label}
            type="button"
            onClick={() => setActiveIx(i)}
            className={`rounded-md px-2.5 py-1 text-xs ${
              i === activeIx
                ? 'bg-brand-600 text-white'
                : 'bg-slate-100 text-slate-700 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700'
            }`}
          >
            {ex.label}
          </button>
        ))}
      </div>
      <div className="relative rounded-lg bg-slate-950 px-3 py-2.5 font-mono text-[11px] text-slate-100">
        <pre className="overflow-x-auto whitespace-pre-wrap break-all pr-10">
          <code>$ {cmd}</code>
        </pre>
        <button
          type="button"
          aria-label="Copy command"
          onClick={() => {
            navigator.clipboard
              .writeText(cmd)
              .then(() => {
                setCopied(true);
                setTimeout(() => setCopied(false), 1500);
              })
              .catch(() => {});
          }}
          className="absolute right-2 top-2 rounded p-1 text-slate-400 hover:bg-slate-800 hover:text-slate-100"
        >
          {copied ? (
            <Check className="h-3.5 w-3.5 text-up-DEFAULT" />
          ) : (
            <Copy className="h-3.5 w-3.5" />
          )}
        </button>
      </div>
      <p className="mt-2 text-[11px] text-slate-500">
        No auth needed for the public tier — every endpoint here
        responds in milliseconds.
      </p>
    </div>
  );
}
