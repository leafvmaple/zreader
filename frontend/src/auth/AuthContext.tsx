import { createContext, useCallback, useContext, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import * as api from '../api/client';
import { APP_NAME } from '../brand';
import type { Account } from '../types/api';

// The signed-in account, and the two operations every screen needs: sign
// out, and re-read who we are after something changes it.
//
// There is deliberately no "signed out" state exposed to the app: AuthGate
// renders the login screen instead of children, so anything inside can treat
// `account` as always present.

type AuthValue = {
  account: Account;
  refresh: () => Promise<void>;
  signOut: () => Promise<void>;
};

const AuthCtx = createContext<AuthValue | null>(null);

export function useAuth(): AuthValue {
  const v = useContext(AuthCtx);
  if (!v) throw new Error('useAuth used outside AuthGate');
  return v;
}

type Phase =
  | { kind: 'loading' }
  | { kind: 'setup' }
  | { kind: 'login' }
  | { kind: 'ready'; account: Account }
  | { kind: 'error'; message: string };

export function AuthGate({
  children,
  renderAuth,
}: {
  children: ReactNode;
  renderAuth: (mode: 'setup' | 'login', onDone: (a: Account) => void) => ReactNode;
}) {
  const [phase, setPhase] = useState<Phase>({ kind: 'loading' });

  const load = useCallback(async () => {
    try {
      const status = await api.authStatus();
      if (status.setup_required) setPhase({ kind: 'setup' });
      else if (status.user) setPhase({ kind: 'ready', account: status.user });
      else setPhase({ kind: 'login' });
    } catch (err) {
      // A server that can't answer /auth/status is down or unreachable;
      // showing a login form would only produce a second, more confusing
      // failure.
      setPhase({ kind: 'error', message: err instanceof Error ? err.message : String(err) });
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // A session can end while the app is open — it expires, an admin removes
  // the account, a password rotates elsewhere. The API client reports those
  // as 401s; drop straight back to the login screen rather than letting
  // every panel render its own "load failed".
  useEffect(() => {
    const onUnauthorised = () => setPhase({ kind: 'login' });
    window.addEventListener('zreader:unauthenticated', onUnauthorised);
    return () => window.removeEventListener('zreader:unauthenticated', onUnauthorised);
  }, []);

  const signOut = useCallback(async () => {
    try {
      await api.logout();
    } finally {
      setPhase({ kind: 'login' });
    }
  }, []);

  if (phase.kind === 'loading') {
    return (
      <div className="auth-boot">
        <span className="shelf__spinner" aria-hidden="true" />
      </div>
    );
  }
  if (phase.kind === 'error') {
    return (
      <div className="auth-boot">
        <p>无法连接到{APP_NAME}</p>
        <p className="auth-boot__sub">{phase.message}</p>
        <button type="button" className="shelf__btn" onClick={() => void load()}>
          重试
        </button>
      </div>
    );
  }
  if (phase.kind !== 'ready') {
    return <>{renderAuth(phase.kind, (account) => setPhase({ kind: 'ready', account }))}</>;
  }

  return (
    <AuthCtx.Provider value={{ account: phase.account, refresh: load, signOut }}>
      {children}
    </AuthCtx.Provider>
  );
}
