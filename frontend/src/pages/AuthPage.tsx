import { useState } from 'react';
import * as api from '../api/client';
import { APP_NAME } from '../brand';
import type { Account } from '../types/api';
import './AuthPage.css';

// One screen serving both first-run setup and ordinary login. They differ
// only in the endpoint and the copy — splitting them into two components
// would duplicate the form, the error handling and the styling to express
// a one-line difference.

export function AuthPage({
  mode,
  onDone,
}: {
  mode: 'setup' | 'login';
  onDone: (a: Account) => void;
}) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const isSetup = mode === 'setup';

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (isSetup && password !== confirm) {
      setError('两次输入的密码不一致');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const account = isSetup
        ? await api.setupFirstAccount(username, password)
        : await api.login(username, password);
      onDone(account);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="auth">
      <form className="auth__card" onSubmit={submit}>
        <h1 className="auth__brand">{APP_NAME}</h1>
        <p className="auth__lede">
          {isSetup
            ? '这是第一次启动，创建一个管理员账号。之前的阅读进度会归到这个账号下。'
            : '登录后继续阅读。'}
        </p>

        <label className="field">
          <span>用户名</span>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoFocus
            required
            disabled={busy}
          />
        </label>

        <label className="field">
          <span>密码</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete={isSetup ? 'new-password' : 'current-password'}
            required
            disabled={busy}
          />
        </label>

        {isSetup && (
          <label className="field">
            <span>确认密码</span>
            <input
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              autoComplete="new-password"
              required
              disabled={busy}
            />
          </label>
        )}

        {error && <div className="form-error">{error}</div>}

        <button type="submit" className="shelf__btn shelf__btn--primary auth__submit" disabled={busy}>
          {busy ? '请稍候…' : isSetup ? '创建账号' : '登录'}
        </button>

        {isSetup && (
          <p className="auth__hint">
            密码至少 8 个字符，最多 72 字节（约 24 个汉字）。
          </p>
        )}
      </form>
    </main>
  );
}
