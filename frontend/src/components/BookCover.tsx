import { useState } from 'react';
import type { Book } from '../types/api';
import { coverURL } from '../api/client';
import './BookCover.css';

// BookCover renders a book's art at a fixed 2:3 portrait ratio.
//
// EPUB (and converted MOBI/AZW) sources carry real cover art, extracted
// into the cached EPUB at scan time and served from /books/{id}/cover.
// Everything else — the TXT novels that make up most of a Chinese
// library — gets a generated cover: a flat field in the book's hashed
// hue with the title and author set on it, instead of the single giant
// character the shelf used to draw.
//
// `book.has_cover` gates the network request: a library with no EPUBs
// issues no image requests at all rather than one 404 per book.

type Props = {
  book: Book;
  /** Extra class for size/placement; the cover always fills its box. */
  className?: string;
};

// splitTitle breaks a title into display lines. CJK titles have no
// spaces to wrap on and `word-break: break-all` would hyphenate mid
// word for the Latin ones, so we chunk explicitly: CJK every 6
// characters, Latin on word boundaries. Four lines max — anything
// longer is a filename, not a title, and gets an ellipsis.
function splitTitle(title: string): string[] {
  const clean = title.trim();
  if (!clean) return [];
  const isCJK = /[㐀-鿿豈-﫿]/.test(clean);
  const lines: string[] = [];
  if (isCJK) {
    for (let i = 0; i < clean.length && lines.length < 4; i += 6) {
      lines.push(clean.slice(i, i + 6));
    }
  } else {
    let line = '';
    for (const word of clean.split(/\s+/)) {
      if (line && (line + ' ' + word).length > 12) {
        lines.push(line);
        if (lines.length === 4) break;
        line = word;
      } else {
        line = line ? `${line} ${word}` : word;
      }
    }
    if (line && lines.length < 4) lines.push(line);
  }
  if (lines.length === 4 && clean.length > (isCJK ? 24 : 48)) {
    lines[3] = lines[3].slice(0, -1) + '…';
  }
  return lines;
}

export function BookCover({ book, className }: Props) {
  // Reset on id change so navigating between books never shows the
  // previous book's failure state.
  const [failed, setFailed] = useState<number | null>(null);
  const showArt = book.has_cover && failed !== book.id;

  return (
    <div
      className={`book-cover${className ? ` ${className}` : ''}`}
      style={{ '--cc': book.cover_color || '#596070' } as React.CSSProperties}
    >
      {showArt ? (
        <img
          className="book-cover__art"
          src={coverURL(book.id)}
          alt=""
          loading="lazy"
          decoding="async"
          onError={() => setFailed(book.id)}
        />
      ) : (
        <div className="book-cover__generated" aria-hidden="true">
          {/* Shown only at thumbnail sizes, where the full title would
              be too small to read — see the container query in the CSS. */}
          <div className="book-cover__label">{book.cover_label || book.title.slice(0, 1)}</div>
          <div className="book-cover__title">
            {splitTitle(book.title).map((line, i) => (
              <span key={i}>{line}</span>
            ))}
          </div>
          <div className="book-cover__rule" />
          <div className="book-cover__author">{book.author ?? ''}</div>
        </div>
      )}
    </div>
  );
}
