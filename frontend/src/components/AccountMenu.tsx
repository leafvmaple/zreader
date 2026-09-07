import { useCallback, useEffect, useState } from 'react';
import * as api from '../api/client';
import { useAuth } from '../auth/AuthContext';
import type { Account, Role } from '../types/api';
import { Dialog } from './Dialog';
import './AccountMenu.css';

// The signed-in account's own controls, plus account management for admins.
//
// Both dialogs live here rather than in ShelfPage because they are about the
// person using the app, not about the library — and ShelfPage is already the
// largest file in the project.

function UserIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle cx="12" cy="8.5" r="3.6" stroke="currentColor" strokeWidth="1.7" />
      <path d="M4.8 20a7.4 7.4 0 0 1 14.4 0" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
    </svg>
  );
}

export function AccountMenu() {
  const { account, signOut } = useAuth();
  const [open, setOpen] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [showAccounts, setShowAccounts] = useState(false);

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false);
    document.addEventListener('mousedown', close);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', close);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div className="shelf__menu" onMouseDown={(e) => e.stopPropagation()}>
      <button
        type="button"
        className={`shelf__btn shelf__btn--icon shelf__btn--ghost${open ? ' is-open' : ''}`}
        onClick={() => setOpen((v) => !v)}
        aria-label={`账号：${account.username}`}
        aria-haspopup="menu"
        aria-expanded={open}
        title={account.username}
      >
        <UserIcon />
      </button>

      {open && (
        <div className="shelf__menu-pop" role="menu">
          <div className="account-menu__who">
            <b>{account.username}</b>
            <span>{account.role === 'admin' ? '管理员' : '普通用户'}</span>
          </div>
          <button type="button" role="menuitem" onClick={() => { setShowPassword(true); setOpen(false); }}>
            修改密码
          </button>
          {account.role === 'admin' && (
            <button type="button" role="menuitem" onClick={() => { setShowAccounts(true); setOpen(false); }}>
              用户管理
            </button>
          )}
          <button type="button" role="menuitem" onClick={() => void signOut()}>
            退出登录
          </button>
        </div>
      )}

      {showPassword && <PasswordDialog onClose={() => setShowPassword(false)} />}
      {showAccounts && <AccountsDialog onClose={() => setShowAccounts(false)} />}
    </div>
  );
}

function PasswordDialog({ onClose }: { onClose: () => void }) {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  const save = useCallback(async () => {
    if (next !== confirm) {
      setMsg('两次输入的新密码不一致');
      return;
    }
    setBusy(true);
    setMsg(null);
    try {
      await api.changeOwnPassword(current, next);
      setDone(true);
    } catch (err) {
      setMsg(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, [confirm, current, next]);

  return (
    <Dialog
      title="修改密码"
      compact
      onClose={onClose}
      busy={busy}
      footer={
        done ? (
          <button type="button" className="shelf__btn shelf__btn--primary" onClick={onClose}>
            完成
          </button>
        ) : (
          <>
            <button type="button" className="shelf__btn" onClick={onClose} disabled={busy}>
              取消
            </button>
            <button
              type="button"
              className="shelf__btn shelf__btn--primary"
              onClick={() => void save()}
              disabled={busy || !current || !next}
            >
              {busy ? '保存中…' : '保存'}
            </button>
          </>
        )
      }
    >
      {done ? (
        <p className="confirm-text">
          密码已更新。其他设备上的登录状态已全部失效，需要重新登录。
        </p>
      ) : (
        <>
          <label className="field">
            <span>当前密码</span>
            <input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} autoComplete="current-password" disabled={busy} />
          </label>
          <label className="field">
            <span>新密码</span>
            <input type="password" value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" disabled={busy} />
          </label>
          <label className="field">
            <span>确认新密码</span>
            <input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" disabled={busy} />
          </label>
          <p className="account-menu__note">
            修改后，除当前这个浏览器外的所有登录都会失效。
          </p>
          {msg && <div className="form-error">{msg}</div>}
        </>
      )}
    </Dialog>
  );
}

function AccountsDialog({ onClose }: { onClose: () => void }) {
  const { account } = useAuth();
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<Role>('user');

  const load = useCallback(async () => {
    try {
      setAccounts(await api.listAccounts());
      setMsg(null);
    } catch (err) {
      setMsg(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const run = useCallback(
    async (fn: () => Promise<unknown>) => {
      setBusy(true);
      setMsg(null);
      try {
        await fn();
        await load();
      } catch (err) {
        setMsg(err instanceof Error ? err.message : String(err));
      } finally {
        setBusy(false);
      }
    },
    [load],
  );

  return (
    <Dialog title="用户管理" onClose={onClose} busy={busy}>
      <ul className="account-list">
        {accounts.map((a) => (
          <li key={a.id}>
            <div className="account-list__who">
              <b>{a.username}</b>
              <span>
                {a.role === 'admin' ? '管理员' : '普通用户'}
                {a.id === account.id && ' · 当前登录'}
              </span>
            </div>
            <div className="account-list__tools">
              <select
                value={a.role}
                onChange={(e) => void run(() => api.updateAccount(a.id, { role: e.target.value as Role }))}
                disabled={busy}
                aria-label={`${a.username} 的角色`}
              >
                <option value="user">普通用户</option>
                <option value="admin">管理员</option>
              </select>
              <button
                type="button"
                className="account-list__delete"
                onClick={() => void run(() => api.deleteAccount(a.id))}
                disabled={busy || a.id === account.id}
                title={a.id === account.id ? '不能删除当前登录的账号' : '删除账号'}
              >
                删除
              </button>
            </div>
          </li>
        ))}
      </ul>

      <p className="account-menu__note">
        删除账号会一并删除该账号的阅读进度和书签。书库本身不受影响。
      </p>

      <div className="account-new">
        <span className="export__label">添加账号</span>
        <div className="field-row">
          <label className="field">
            <span>用户名</span>
            <input value={username} onChange={(e) => setUsername(e.target.value)} disabled={busy} />
          </label>
          <label className="field">
            <span>密码</span>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" disabled={busy} />
          </label>
        </div>
        <div className="field-row">
          <label className="field">
            <span>角色</span>
            <select value={role} onChange={(e) => setRole(e.target.value as Role)} disabled={busy}>
              <option value="user">普通用户</option>
              <option value="admin">管理员</option>
            </select>
          </label>
          <button
            type="button"
            className="shelf__btn shelf__btn--primary account-new__add"
            onClick={() =>
              void run(async () => {
                await api.createAccount(username, password, role);
                setUsername('');
                setPassword('');
              })
            }
            disabled={busy || !username || !password}
          >
            添加
          </button>
        </div>
      </div>

      {msg && <div className="form-error">{msg}</div>}
    </Dialog>
  );
}
