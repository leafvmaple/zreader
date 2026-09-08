import { useRef } from 'react';
import type { ReactNode } from 'react';
import { useModalFocus } from '../hooks/useModalFocus';

// ReaderDrawer is the side panel the reader's contents, search, bookmarks
// and settings all live in. They were four copies of the same markup, and
// the accessibility gaps were four copies too: no aria-modal, no focus
// handling, and a close button whose only label was a ✕ glyph.

type Props = {
  title: string;
  onClose: () => void;
  /** Narrower panel, used by the contents list. */
  narrow?: boolean;
  children: ReactNode;
};

export function ReaderDrawer({ title, onClose, narrow, children }: Props) {
  const panelRef = useRef<HTMLElement | null>(null);
  useModalFocus(panelRef);

  return (
    <div className="drawer" onClick={onClose}>
      <aside
        ref={panelRef}
        tabIndex={-1}
        className={`drawer__panel${narrow ? ' drawer__panel--narrow' : ''}`}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={title}
      >
        <header className="drawer__header">
          <h3>{title}</h3>
          <button className="drawer__close" onClick={onClose} aria-label="关闭">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
              <path d="M6 6l12 12M18 6L6 18" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
            </svg>
          </button>
        </header>
        {children}
      </aside>
    </div>
  );
}
