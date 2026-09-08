import { useEffect } from 'react';
import type { RefObject } from 'react';

// useModalFocus gives a modal surface the keyboard behaviour it needs:
// focus moves in when it opens, Tab cycles inside it rather than walking
// the page behind it, and focus returns to whatever opened it on close.
//
// The last part is what makes a dialog usable from the keyboard at all.
// Without it, closing sends focus back to <body> and the next Tab starts
// from the top of the document — so opening the contents, closing it, and
// pressing Tab left you somewhere unrelated with nothing visibly focused.

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

export function useModalFocus(panelRef: RefObject<HTMLElement | null>, active = true) {
  useEffect(() => {
    if (!active) return;
    const panel = panelRef.current;
    if (!panel) return;

    const opener = document.activeElement as HTMLElement | null;
    // Prefer a real control so the first Tab continues from inside; fall
    // back to the panel itself, which carries tabIndex={-1} for this.
    const first = panel.querySelector<HTMLElement>(FOCUSABLE);
    (first ?? panel).focus();

    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Tab') return;
      const items = [...panel.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
        (el) => el.offsetParent !== null || el === document.activeElement,
      );
      if (items.length === 0) {
        e.preventDefault();
        return;
      }
      const firstItem = items[0];
      const lastItem = items[items.length - 1];
      // Focus outside the panel entirely (the opener, say) counts as
      // "before the start", so Tab enters rather than escaping.
      if (!panel.contains(document.activeElement)) {
        e.preventDefault();
        (e.shiftKey ? lastItem : firstItem).focus();
        return;
      }
      if (e.shiftKey && document.activeElement === firstItem) {
        e.preventDefault();
        lastItem.focus();
      } else if (!e.shiftKey && document.activeElement === lastItem) {
        e.preventDefault();
        firstItem.focus();
      }
    };

    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('keydown', onKey);
      // Only take focus back if it is still inside the surface being torn
      // down; if something else has claimed it, leave it alone.
      if (opener && (!document.activeElement || document.activeElement === document.body)) {
        opener.focus();
      }
    };
  }, [panelRef, active]);
}
