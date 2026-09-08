import type { ReactNode } from 'react';

// The icon set, in one place.
//
// These were fifteen components split across the two pages, in two naming
// conventions and two drawing styles: the reader wrapped its paths in a
// shared <Glyph>, the shelf repeated the svg attributes in each one. The
// magnifier existed twice, drawn identically.
//
// Glyph carries the shared attributes. Size is a prop because the shelf
// draws at 16 and the reader at 18, and unifying that would be a visual
// change dressed up as a refactor.

export function Glyph({ children, size }: { children: ReactNode; size?: number }) {
  const px = size ?? 18;
  return (
    <svg
      width={px}
      height={px}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {children}
    </svg>
  );
}

export const IconBack = () => (
  <Glyph>
    <path d="M15 5l-7 7 7 7" />
  </Glyph>
);
// Size is a prop only where two callers disagree: the shelf draws this at
// 16 alongside its own icons, the reader at 18 in its chrome.
export const IconSearch = ({ size }: { size?: number } = {}) => (
  <Glyph size={size}>
    <circle cx="11" cy="11" r="7" />
    <path d="M20.5 20.5L16 16" />
  </Glyph>
);
export const IconBookmarkPlus = () => (
  <Glyph>
    <path d="M6 4h12v16l-6-4-6 4z" />
    <path d="M12 8.5v4M10 10.5h4" />
  </Glyph>
);
export const IconBookmark = () => (
  <Glyph>
    <path d="M6 4h12v16l-6-4-6 4z" />
  </Glyph>
);
export const IconList = () => (
  <Glyph>
    <path d="M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01" />
  </Glyph>
);
export const IconSettings = () => (
  <Glyph>
    <path d="M4 7h7M15 7h5" />
    <circle cx="13" cy="7" r="2" />
    <path d="M4 12h4M12 12h8" />
    <circle cx="10" cy="12" r="2" />
    <path d="M4 17h11M19 17h1" />
    <circle cx="17" cy="17" r="2" />
  </Glyph>
);
export const IconChevronLeft = () => (
  <Glyph>
    <path d="M15 6l-6 6 6 6" />
  </Glyph>
);
export const IconChevronRight = () => (
  <Glyph>
    <path d="M9 6l6 6-6 6" />
  </Glyph>
);

export function IconTheme({ mode }: { mode: 'light' | 'dark' }) {
  // Show the glyph for the mode you'd switch *to*.
  if (mode === 'light') {
    // moon
    return (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <path
          d="M21 12.8A8.5 8.5 0 1 1 11.2 3a6.6 6.6 0 0 0 9.8 9.8Z"
          stroke="currentColor"
          strokeWidth="1.7"
          strokeLinejoin="round"
        />
      </svg>
    );
  }
  // sun
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle cx="12" cy="12" r="4" stroke="currentColor" strokeWidth="1.7" />
      <path
        d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function IconView({ mode }: { mode: 'list' | 'grid' }) {
  // Show the glyph for the view you'd switch *to*.
  if (mode === 'list') {
    // grid
    return (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <rect x="3" y="3" width="7" height="7" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
        <rect x="14" y="3" width="7" height="7" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
        <rect x="3" y="14" width="7" height="7" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
        <rect x="14" y="14" width="7" height="7" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
      </svg>
    );
  }
  // list
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path
        d="M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function IconPlus() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path d="M12 5v14M5 12h14" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
    </svg>
  );
}

export function IconMore() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle cx="5" cy="12" r="1.6" fill="currentColor" />
      <circle cx="12" cy="12" r="1.6" fill="currentColor" />
      <circle cx="19" cy="12" r="1.6" fill="currentColor" />
    </svg>
  );
}

export function IconStar({ filled }: { filled: boolean }) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true">
      <path
        d="M12 3.6l2.5 5.2 5.7.8-4.1 4 1 5.7-5.1-2.7-5.1 2.7 1-5.7-4.1-4 5.7-.8z"
        fill={filled ? 'currentColor' : 'none'}
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function IconSort() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path
        d="M7 4v16m0 0l-3-3.5M7 20l3-3.5M17 20V4m0 0l-3 3.5M17 4l3 3.5"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
