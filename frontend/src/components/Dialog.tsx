import { useCallback, useEffect, useRef } from 'react';
import type { ReactNode } from 'react';
import './Dialog.css';

// Dialog is the shared modal shell. The shelf's three dialogs used to
// hand-roll their own markup and none of them closed on Escape or on a
// backdrop click — the only way out was the × button, which is not where
// anyone looks first. Centralising it also gives every dialog the same
// focus handling and scroll lock.

type Props = {
  title: string;
  onClose: () => void;
  /** Blocks Escape and backdrop dismissal while a request is in flight. */
  busy?: boolean;
  /** Narrower panel for short confirmations. */
  compact?: boolean;
  children: ReactNode;
  footer?: ReactNode;
};

export function Dialog({ title, onClose, busy, compact, children, footer }: Props) {
  const panelRef = useRef<HTMLDivElement | null>(null);
  const close = useCallback(() => {
    if (!busy) onClose();
  }, [busy, onClose]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        close();
      }
    };
    document.addEventListener('keydown', onKey);
    // Move focus into the dialog so Escape and Tab act on it rather than
    // on whatever button happened to open it.
    panelRef.current?.focus();
    const { overflow } = document.body.style;
    document.body.style.overflow = 'hidden';
    return () => {
      document.removeEventListener('keydown', onKey);
      document.body.style.overflow = overflow;
    };
  }, [close]);

  return (
    <div className="dlg" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div
        ref={panelRef}
        tabIndex={-1}
        className={`dlg__panel${compact ? ' dlg__panel--compact' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-label={title}
      >
        <header className="dlg__header">
          <h2>{title}</h2>
          <button type="button" className="dlg__close" onClick={close} disabled={busy} aria-label="关闭">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
              <path d="M6 6l12 12M18 6L6 18" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
            </svg>
          </button>
        </header>
        <div className="dlg__body">{children}</div>
        {footer && <footer className="dlg__actions">{footer}</footer>}
      </div>
    </div>
  );
}
