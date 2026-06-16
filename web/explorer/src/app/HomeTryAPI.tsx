'use client';

import { Check, Copy, Play } from 'lucide-react';
import { useState } from 'react';

import { API_BASE_URL } from '@/api/client';

type Lang = 'curl' | 'js' | 'python' | 'go';

const LANGS: { key: Lang; label: string }[] = [
  { key: 'curl', label: 'curl' },
  { key: 'js', label: 'JS' },
  { key: 'python', label: 'Python' },
  { key: 'go', label: 'Go' },
];

function renderSnippet(lang: Lang, base: string, path: string): string {
  const url = `${base}${path}`;
  switch (lang) {
    case 'curl':
      return `curl '${url}'`;
    case 'js':
      return `const res = await fetch('${url}');\nconst data = await res.json();\nconsole.log(data);`;
    case 'python':
      return `import requests\n\nr = requests.get('${url}')\nprint(r.json())`;
    case 'go':
      return `import (\n  "encoding/json"\n  "net/http"\n)\n\nresp, _ := http.Get("${url}")\nvar data map[string]any\njson.NewDecoder(resp.Body).Decode(&data)`;
  }
}

interface Example {
  label: string;
  // path is the relative URL (with query string). The cmd renders
  // a curl invocation; the live runner just fetches the same URL.
  path: string;
}

const EXAMPLES: Example[] = [
  {
    label: 'Latest XLM/USDC price (VWAP)',
    path: '/v1/price?asset=native&quote=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
  },
  {
    label: 'Network stats — 24h volume + market count',
    path: '/v1/network/stats',
  },
  {
    label: 'XLM asset detail',
    path: '/v1/assets/XLM',
  },
  {
    label: 'Top-10 assets',
    path: '/v1/assets?limit=10',
  },
  {
    label: 'Top-10 markets by 24h volume',
    path: '/v1/markets?limit=10&order_by=volume_24h_usd_desc',
  },
  {
    label: 'Per-source breakdown for XLM/USDC',
    path: '/v1/pools?base=native&quote=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
  },
  {
    label: 'Sources with 24h trade counts',
    path: '/v1/sources?include=stats',
  },
  {
    label: 'World verified currencies (catalogue)',
    path: '/v1/assets/verified',
  },
  {
    label: 'EUR cross-chain identity + Stellar network detail',
    path: '/v1/assets/euro',
  },
  {
    label: 'Latest oracle streams (Reflector / Redstone / Band)',
    path: '/v1/oracle/streams',
  },
  {
    label: 'Lending pools (Blend)',
    path: '/v1/lending/pools',
  },
  {
    label: 'Recent XLM/USDC trades',
    path: '/v1/history?base=native&quote=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN&limit=5',
  },
  {
    label: 'Per-source ingest cursors',
    path: '/v1/diagnostics/cursors',
  },
  {
    label: 'Customer incident history',
    path: '/v1/incidents',
  },
];

/**
 * Inline "Try the API" panel for the home page. Tabs through a few
 * canonical examples — copy-button on each so a visitor can paste
 * straight into a terminal, AND a Run-it button that fetches the
 * same URL inline and renders the JSON response.
 *
 * The `path` is shared between the curl command + the live fetch
 * so what you read in the box is exactly what you get when you
 * Run it.
 */
export function HomeTryAPI() {
  const [activeIx, setActiveIx] = useState(0);
  const [lang, setLang] = useState<Lang>('curl');
  const [copied, setCopied] = useState(false);
  const [running, setRunning] = useState(false);
  const [response, setResponse] = useState<string | null>(null);
  const [responseTone, setResponseTone] = useState<'ok' | 'err' | null>(null);

  const example = EXAMPLES[activeIx]!;
  const cmd = renderSnippet(lang, API_BASE_URL, example.path);

  function runLive() {
    setRunning(true);
    setResponse(null);
    setResponseTone(null);
    fetch(`${API_BASE_URL}${example.path}`, { cache: 'no-store' })
      .then(async (r) => {
        const body = await r.text();
        let pretty = body;
        try {
          pretty = JSON.stringify(JSON.parse(body), null, 2);
        } catch {
          // Non-JSON; show raw.
        }
        setResponse(pretty.slice(0, 4000));
        setResponseTone(r.ok ? 'ok' : 'err');
      })
      .catch((e) => {
        setResponse(e instanceof Error ? e.message : 'Network error');
        setResponseTone('err');
      })
      .finally(() => setRunning(false));
  }

  function pickExample(i: number) {
    setActiveIx(i);
    setResponse(null);
    setResponseTone(null);
  }

  return (
    <div className="rounded-xl border border-line bg-surface p-4 shadow-sm">
      <div className="mb-3 flex flex-wrap gap-1">
        {EXAMPLES.map((ex, i) => (
          <button
            key={ex.label}
            type="button"
            onClick={() => pickExample(i)}
            className={`rounded-md px-2.5 py-1 text-xs ${
              i === activeIx
                ? 'bg-brand-600 text-white'
                : 'bg-surface-subtle text-ink-body hover:bg-line'
            }`}
          >
            {ex.label}
          </button>
        ))}
      </div>
      <div className="mb-2 flex items-center gap-1 border-b border-line pb-2">
        {LANGS.map((l) => (
          <button
            key={l.key}
            type="button"
            onClick={() => setLang(l.key)}
            className={`rounded px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider ${
              lang === l.key
                ? 'bg-line text-ink'
                : 'text-ink-muted hover:text-ink'
            }`}
          >
            {l.label}
          </button>
        ))}
      </div>
      <div className="relative rounded-lg bg-ink px-3 py-2.5 font-mono text-[11px] text-ink-faint">
        <pre className="overflow-x-auto whitespace-pre-wrap break-all pr-20">
          <code>{lang === 'curl' ? '$ ' : ''}{cmd}</code>
        </pre>
        <div className="absolute right-2 top-2 flex gap-1">
          <button
            type="button"
            aria-label="Run live"
            onClick={runLive}
            disabled={running}
            className="rounded p-1 text-ink-faint hover:bg-ink hover:text-up disabled:opacity-50"
          >
            <Play className="h-3.5 w-3.5" />
          </button>
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
            className="rounded p-1 text-ink-faint hover:bg-ink hover:text-ink-faint"
          >
            {copied ? (
              <Check className="h-3.5 w-3.5 text-up-DEFAULT" />
            ) : (
              <Copy className="h-3.5 w-3.5" />
            )}
          </button>
        </div>
      </div>
      {response != null && (
        <div className="mt-2 overflow-hidden rounded-lg border border-line bg-surface-muted">
          <div
            className={`flex items-center justify-between px-3 py-1 text-[10px] uppercase tracking-wider ${
              responseTone === 'ok'
                ? 'bg-up-subtle/40 text-up'
                : 'bg-down-subtle/40 text-down'
            }`}
          >
            <span>response</span>
            <span>
              {responseTone === 'ok' ? 'OK' : 'error'} · {response.length}b
              {response.length === 4000 && ' (truncated)'}
            </span>
          </div>
          <pre className="max-h-72 overflow-auto px-3 py-2 font-mono text-[11px] text-ink-body">
            {response}
          </pre>
        </div>
      )}
      <p className="mt-2 text-[11px] text-ink-muted">
        No auth needed for the public tier — every endpoint here
        responds in milliseconds. Hit ▶ to run live; click any
        example tab above to see the curl.
        {lang === 'go' && (
          <>
            {' '}
            For idiomatic Go using the official SDK, see{' '}
            <a
              href="/sdk"
              className="text-brand-600 hover:underline"
            >
              /sdk
            </a>
            .
          </>
        )}
      </p>
    </div>
  );
}
