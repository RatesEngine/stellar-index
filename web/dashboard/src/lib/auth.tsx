'use client';

import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from 'react';
import { ApiError, fetchMe, type AccountMe } from './api';

// AuthState collapses three cases the dashboard cares about:
//   - 'loading' — first render, /me request in flight
//   - 'anon'    — /me returned 401, redirect to /signin/
//   - 'authed'  — /me returned 200, render the dashboard
//
// We deliberately don't try to restore from localStorage —
// the cookie IS the source of truth, and a stale cache could
// have us render a stale account name. One fetch per mount.
export type AuthState =
  | { kind: 'loading' }
  | { kind: 'anon' }
  | { kind: 'authed'; me: AccountMe };

const Ctx = createContext<{ state: AuthState; refresh: () => void }>({
  state: { kind: 'loading' },
  refresh: () => {},
});

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ kind: 'loading' });
  const [tick, setTick] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    fetchMe(controller.signal)
      .then((me) => setState({ kind: 'authed', me }))
      .catch((err) => {
        if (controller.signal.aborted) return;
        if (err instanceof ApiError && err.status === 401) {
          setState({ kind: 'anon' });
        } else {
          // Network failure / 5xx — surface as anon so the user
          // gets pushed to /signin and can re-auth. Logging is the
          // operator's job (Loki).
          setState({ kind: 'anon' });
        }
      });
    return () => controller.abort();
  }, [tick]);

  return (
    <Ctx.Provider value={{ state, refresh: () => setTick((t) => t + 1) }}>
      {children}
    </Ctx.Provider>
  );
}

export function useAuth() {
  return useContext(Ctx);
}
