'use client';

import { AlertCircle, Check, Copy, KeyRound, Loader2 } from 'lucide-react';
import { useState } from 'react';

import { API_BASE_URL } from '@/api/client';

type SignupSuccess = {
  data: {
    plaintext: string;
    key_id: string;
    identifier: string;
    label: string;
    tier: string;
    rate_limit_per_min: number;
  };
};

type SignupError = {
  type?: string;
  title?: string;
  detail?: string;
  status?: number;
};

type State =
  | { kind: 'idle' }
  | { kind: 'submitting' }
  | { kind: 'error'; message: string }
  | { kind: 'success'; result: SignupSuccess['data'] };

/**
 * Self-service signup form. POSTs to /v1/signup; on success
 * displays the plaintext key with a copy button + a clear
 * "shown once" warning + a copy-paste curl example.
 *
 * The server-side handler (internal/api/v1/signup.go) handles
 * all validation; this UI just relays the response.
 */
export function SignupForm() {
  const [state, setState] = useState<State>({ kind: 'idle' });
  const [email, setEmail] = useState('');
  const [label, setLabel] = useState('');
  const [copiedKey, setCopiedKey] = useState(false);
  const [copiedCurl, setCopiedCurl] = useState(false);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!email.trim()) return;
    setState({ kind: 'submitting' });

    try {
      const res = await fetch(`${API_BASE_URL}/v1/signup`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          email: email.trim(),
          ...(label.trim() ? { label: label.trim() } : {}),
        }),
      });
      if (!res.ok) {
        let problem: SignupError = {};
        try {
          problem = (await res.json()) as SignupError;
        } catch {
          // body wasn't problem+json — fall back to the status text.
        }
        const msg =
          problem.detail || problem.title || `${res.status} ${res.statusText}`;
        setState({ kind: 'error', message: msg });
        return;
      }
      const body = (await res.json()) as SignupSuccess;
      setState({ kind: 'success', result: body.data });
    } catch (err) {
      setState({
        kind: 'error',
        message: err instanceof Error ? err.message : 'network error',
      });
    }
  }

  function copy(value: string, setter: (v: boolean) => void) {
    void navigator.clipboard.writeText(value);
    setter(true);
    setTimeout(() => setter(false), 1800);
  }

  if (state.kind === 'success') {
    const { result } = state;
    const curl = `curl -H "Authorization: Bearer ${result.plaintext}" \\\n     ${API_BASE_URL}/v1/account/me`;
    return (
      <div className="space-y-6">
        <div className="flex items-start gap-3 rounded-lg border border-emerald-200 bg-emerald-50 p-4 dark:border-emerald-800 dark:bg-emerald-900/20">
          <Check className="mt-0.5 h-5 w-5 shrink-0 text-emerald-600 dark:text-emerald-400" />
          <div>
            <p className="font-semibold text-emerald-900 dark:text-emerald-200">
              Account created — here&rsquo;s your key.
            </p>
            <p className="mt-1 text-sm text-emerald-800 dark:text-emerald-300">
              <strong>Copy it now.</strong> The plaintext key is shown
              once and is unrecoverable. We hash it before storage,
              so even our operators can&rsquo;t read it back.
            </p>
          </div>
        </div>

        <div>
          <label
            htmlFor="key-display"
            className="mb-2 block text-sm font-medium text-slate-700 dark:text-slate-300"
          >
            Your API key
          </label>
          <div className="flex items-stretch gap-2">
            <input
              id="key-display"
              readOnly
              value={result.plaintext}
              onFocus={(e) => e.currentTarget.select()}
              className="flex-1 rounded-md border border-slate-300 bg-slate-50 px-3 py-2 font-mono text-sm text-slate-900 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100"
            />
            <button
              type="button"
              onClick={() => copy(result.plaintext, setCopiedKey)}
              className="inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-3 py-2 text-sm font-medium text-white hover:bg-brand-700"
            >
              {copiedKey ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
              {copiedKey ? 'Copied' : 'Copy'}
            </button>
          </div>
        </div>

        <dl className="grid grid-cols-2 gap-4 rounded-lg border border-slate-200 bg-slate-50 p-4 text-sm dark:border-slate-800 dark:bg-slate-800/50">
          <div>
            <dt className="text-xs uppercase tracking-wider text-slate-500">Key ID</dt>
            <dd className="mt-0.5 font-mono text-slate-900 dark:text-slate-100">
              {result.key_id}
            </dd>
          </div>
          <div>
            <dt className="text-xs uppercase tracking-wider text-slate-500">Tier</dt>
            <dd className="mt-0.5 font-mono text-slate-900 dark:text-slate-100">
              {result.tier}
            </dd>
          </div>
          <div>
            <dt className="text-xs uppercase tracking-wider text-slate-500">
              Rate limit
            </dt>
            <dd className="mt-0.5 text-slate-900 dark:text-slate-100">
              {result.rate_limit_per_min.toLocaleString()} req/min
            </dd>
          </div>
          <div>
            <dt className="text-xs uppercase tracking-wider text-slate-500">
              Identifier
            </dt>
            <dd className="mt-0.5 font-mono text-xs text-slate-900 dark:text-slate-100">
              {result.identifier}
            </dd>
          </div>
        </dl>

        <div>
          <label
            htmlFor="curl-example"
            className="mb-2 block text-sm font-medium text-slate-700 dark:text-slate-300"
          >
            Try it
          </label>
          <div className="flex items-stretch gap-2">
            <pre
              id="curl-example"
              className="flex-1 overflow-x-auto rounded-md border border-slate-300 bg-slate-900 p-3 font-mono text-xs text-slate-100"
            >
              {curl}
            </pre>
            <button
              type="button"
              onClick={() => copy(curl, setCopiedCurl)}
              className="inline-flex items-center gap-1.5 rounded-md bg-slate-200 px-3 py-2 text-sm font-medium text-slate-900 hover:bg-slate-300 dark:bg-slate-700 dark:text-slate-100 dark:hover:bg-slate-600"
            >
              {copiedCurl ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
              {copiedCurl ? 'Copied' : 'Copy'}
            </button>
          </div>
          <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">
            Same key works on every <code className="font-mono">/v1/*</code>{' '}
            endpoint. Send it as <code className="font-mono">Authorization: Bearer &lt;key&gt;</code>.
          </p>
        </div>
      </div>
    );
  }

  return (
    <form onSubmit={onSubmit} className="space-y-5">
      <div>
        <label
          htmlFor="email"
          className="mb-1.5 block text-sm font-medium text-slate-700 dark:text-slate-300"
        >
          Email
        </label>
        <input
          id="email"
          type="email"
          required
          autoComplete="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="you@example.com"
          disabled={state.kind === 'submitting'}
          className="w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 placeholder:text-slate-400 focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/20 disabled:opacity-60 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 dark:placeholder:text-slate-500"
        />
        <p className="mt-1.5 text-xs text-slate-500 dark:text-slate-400">
          One signup per email. We use it as your account identifier
          and to send rate-limit / billing notices later.
        </p>
      </div>

      <div>
        <label
          htmlFor="label"
          className="mb-1.5 block text-sm font-medium text-slate-700 dark:text-slate-300"
        >
          Label{' '}
          <span className="font-normal text-slate-500 dark:text-slate-400">
            (optional)
          </span>
        </label>
        <input
          id="label"
          type="text"
          maxLength={128}
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          placeholder="my-trading-bot"
          disabled={state.kind === 'submitting'}
          className="w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 placeholder:text-slate-400 focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/20 disabled:opacity-60 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 dark:placeholder:text-slate-500"
        />
        <p className="mt-1.5 text-xs text-slate-500 dark:text-slate-400">
          Shown in <code className="font-mono">/v1/account/me</code> so you can
          tell keys apart later. 128 char max.
        </p>
      </div>

      {state.kind === 'error' && (
        <div className="flex items-start gap-3 rounded-lg border border-rose-200 bg-rose-50 p-3 dark:border-rose-900/50 dark:bg-rose-900/20">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-rose-600 dark:text-rose-400" />
          <p className="text-sm text-rose-800 dark:text-rose-300">{state.message}</p>
        </div>
      )}

      <button
        type="submit"
        disabled={state.kind === 'submitting' || !email.trim()}
        className="inline-flex w-full items-center justify-center gap-2 rounded-md bg-brand-600 px-4 py-2.5 text-sm font-semibold text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-60 sm:w-auto"
      >
        {state.kind === 'submitting' ? (
          <>
            <Loader2 className="h-4 w-4 animate-spin" />
            Creating account…
          </>
        ) : (
          <>
            <KeyRound className="h-4 w-4" />
            Get my API key
          </>
        )}
      </button>

      <p className="text-xs text-slate-500 dark:text-slate-400">
        By signing up you agree to use the API in accordance with our
        <a href="https://docs.ratesengine.net" className="underline ml-1">
          terms
        </a>
        . No credit card required.
      </p>
    </form>
  );
}

