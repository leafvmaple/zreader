import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import * as api from '../api/client';
import { useThrottledProgress } from '../hooks/useThrottledProgress';
import type { Book, Chapter, Progress } from '../types/api';
import './ReaderPage.css';

type Theme = 'beige' | 'white' | 'grey' | 'dark';
type FontSize = 'sm' | 'md' | 'lg' | 'xl';

type Settings = { theme: Theme; size: FontSize };

const DEFAULT_SETTINGS: Settings = { theme: 'beige', size: 'md' };
const SETTINGS_KEY = 'zreader.settings';

function loadSettings(): Settings {
  try {
    const raw = localStorage.getItem(SETTINGS_KEY);
    if (raw) return { ...DEFAULT_SETTINGS, ...(JSON.parse(raw) as Partial<Settings>) };
  } catch {
    /* ignore */
  }
  return DEFAULT_SETTINGS;
}

function saveSettings(s: Settings) {
  try {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(s));
  } catch {
    /* ignore */
  }
}

type Segment =
  | { kind: 'chapter'; idx: number; title: string }
  | { kind: 'body'; paragraphs: string[] };

// Splits the decoded UTF-8 text into a sequence of chapter markers + body
// paragraphs. The chapter title line in the source text is consumed by the
// chapter segment so it isn't rendered twice.
//
// We slice by JS string units (UTF-16 code units). For BMP characters
// (including all common CJK), one code unit == one codepoint == one
// char_offset from the backend, so the offsets line up. Astral plane chars
// (some CJK extensions, emoji) would drift by 1 per occurrence; acceptable
// for MVP TXT content.
function buildSegments(text: string, chapters: Chapter[]): Segment[] {
  const out: Segment[] = [];
  const sorted = [...chapters].sort((a, b) => a.char_offset - b.char_offset);

  // Filter to chapters whose offset is in-range — defensive against a stale
  // chapters list pointing past the loaded content.
  const valid = sorted.filter((c) => c.char_offset <= text.length);

  let cursor = 0;
  for (const ch of valid) {
    if (ch.char_offset > cursor) {
      const body = text.slice(cursor, ch.char_offset);
      const paragraphs = body.split(/\n+/).map((p) => p.trim()).filter(Boolean);
      if (paragraphs.length > 0) out.push({ kind: 'body', paragraphs });
    }
    const nl = text.indexOf('\n', ch.char_offset);
    const titleEnd = nl < 0 ? text.length : nl;
    const titleText = text.slice(ch.char_offset, titleEnd).trim() || ch.title;
    out.push({ kind: 'chapter', idx: ch.idx, title: titleText });
    cursor = titleEnd + 1;
  }
  if (cursor < text.length) {
    const tail = text.slice(cursor).split(/\n+/).map((p) => p.trim()).filter(Boolean);
    if (tail.length > 0) out.push({ kind: 'body', paragraphs: tail });
  }
  return out;
}

export function ReaderPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const bookId = Number(id);

  const [book, setBook] = useState<Book | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [text, setText] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [settings, setSettings] = useState<Settings>(loadSettings);
  const [showSettings, setShowSettings] = useState(false);
  const [showTOC, setShowTOC] = useState(false);
  const [showChrome, setShowChrome] = useState(true);

  const [currentChapter, setCurrentChapter] = useState(1);
  const [pct, setPct] = useState(0);

  const scrollRef = useRef<HTMLDivElement | null>(null);
  // Set after the initial scroll-to-saved-position fires, so the scroll
  // listener doesn't immediately overwrite the server progress with 0.
  const initialised = useRef(false);

  const { report, flush } = useThrottledProgress({
    bookId,
    onConflict: (server) => {
      // Another device is ahead — adopt its position.
      const total = book?.char_count ?? 0;
      if (total > 0 && scrollRef.current) {
        const el = scrollRef.current;
        const target = (server.char_offset / total) * (el.scrollHeight - el.clientHeight);
        el.scrollTo({ top: target, behavior: 'smooth' });
      }
    },
  });

  // --- Load the book + saved progress -------------------------------------

  useEffect(() => {
    if (!Number.isFinite(bookId)) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    setText('');
    initialised.current = false;

    (async () => {
      try {
        const [{ book, chapters }, progress] = await Promise.all([
          api.getBook(bookId),
          api.getProgress(bookId).catch(() => null as Progress | null),
        ]);
        if (cancelled) return;
        setBook(book);
        setChapters(chapters);

        // Pull content in 50k-char chunks (server cap) and concatenate.
        const total = book.char_count ?? 0;
        const CHUNK = 50_000;
        let buffer = '';
        let cursor = 0;
        while (cursor < total || total === 0) {
          const slice = await api.getContent(bookId, cursor, CHUNK);
          if (cancelled) return;
          buffer += slice.text;
          cursor = slice.from + slice.len;
          if (slice.eof) break;
          // Defensive: tiny books that report total=0 still terminate via eof.
          if (slice.len === 0) break;
        }
        if (cancelled) return;
        setText(buffer);

        // Defer scroll to next frame so the DOM has measured the content.
        requestAnimationFrame(() => {
          const el = scrollRef.current;
          if (!el || !progress) {
            initialised.current = true;
            return;
          }
          if (progress.char_offset > 0 && total > 0) {
            const ratio = progress.char_offset / total;
            el.scrollTop = Math.max(0, ratio * (el.scrollHeight - el.clientHeight));
          }
          initialised.current = true;
        });
      } catch (err) {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : String(err));
        setLoading(false);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [bookId]);

  // --- Persist settings ----------------------------------------------------

  useEffect(() => {
    saveSettings(settings);
  }, [settings]);

  // --- Scroll → progress updates ------------------------------------------

  const segments = useMemo(() => buildSegments(text, chapters), [text, chapters]);

  const onScroll = useCallback(() => {
    if (!initialised.current || !book) return;
    const el = scrollRef.current;
    if (!el) return;
    const scrollable = Math.max(1, el.scrollHeight - el.clientHeight);
    const ratio = Math.min(1, Math.max(0, el.scrollTop / scrollable));
    setPct(Math.round(ratio * 100));
    const total = book.char_count ?? 0;
    if (total > 0) {
      const charOffset = Math.round(ratio * total);
      // Figure out the current chapter index by walking chapters in order.
      let idx = 1;
      for (const c of chapters) {
        if (c.char_offset <= charOffset) idx = c.idx;
        else break;
      }
      setCurrentChapter(idx);
      const ch = chapters.find((c) => c.idx === idx);
      report({
        char_offset: charOffset,
        chapter_idx: idx,
        chapter_offset: ch ? Math.max(0, charOffset - ch.char_offset) : 0,
      });
    }
  }, [book, chapters, report]);

  // --- Keyboard nav --------------------------------------------------------

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const el = scrollRef.current;
      if (!el) return;
      if (e.key === 'PageDown' || e.key === ' ' || e.key === 'ArrowDown') {
        el.scrollBy({ top: el.clientHeight * 0.9, behavior: 'smooth' });
        e.preventDefault();
      } else if (e.key === 'PageUp' || e.key === 'ArrowUp') {
        el.scrollBy({ top: -el.clientHeight * 0.9, behavior: 'smooth' });
        e.preventDefault();
      } else if (e.key === 'Escape') {
        if (showSettings || showTOC) {
          setShowSettings(false);
          setShowTOC(false);
        } else {
          void flush();
          navigate('/');
        }
      } else if (e.key === 'Home') {
        el.scrollTo({ top: 0, behavior: 'smooth' });
      } else if (e.key === 'End') {
        el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [flush, navigate, showSettings, showTOC]);

  // --- Click anywhere to toggle chrome ------------------------------------

  const onContentClick = useCallback(() => {
    setShowChrome((v) => !v);
  }, []);

  const onChapterClick = useCallback((idx: number) => {
    setShowTOC(false);
    requestAnimationFrame(() => {
      const anchor = document.getElementById(`chap-${idx}`);
      anchor?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    });
  }, []);

  // --- Render --------------------------------------------------------------

  const themeClass = `reader reader--theme-${settings.theme} reader--size-${settings.size}`;
  const currentChapterTitle = chapters.find((c) => c.idx === currentChapter)?.title ?? '';

  return (
    <div className={themeClass}>
      {showChrome && (
        <header className="reader__top">
          <Link
            to="/"
            className="reader__icon-btn"
            onClick={() => void flush()}
            aria-label="返回书架"
          >
            ←
          </Link>
          <div className="reader__top-title">
            <div className="reader__book-title">{book?.title ?? ''}</div>
            <div className="reader__chap-title">{currentChapterTitle}</div>
          </div>
          <div className="reader__icon-btn reader__pct">{pct}%</div>
        </header>
      )}

      <div
        ref={scrollRef}
        className="reader__content"
        onScroll={onScroll}
        onClick={onContentClick}
      >
        {error && <p className="reader__error">加载失败：{error}</p>}
        {loading && !error && <p className="reader__loading">加载中…</p>}
        {!loading && !error && (
          <article className="reader__article">
            {segments.map((seg, i) =>
              seg.kind === 'chapter' ? (
                <h2 key={`c-${i}`} id={`chap-${seg.idx}`} className="reader__chapter">
                  {seg.title}
                </h2>
              ) : (
                <div key={`b-${i}`} className="reader__body">
                  {seg.paragraphs.map((p, j) => (
                    <p key={j}>{p}</p>
                  ))}
                </div>
              ),
            )}
            <div className="reader__end">— 完 —</div>
          </article>
        )}
      </div>

      {showChrome && (
        <footer className="reader__bottom">
          <button
            className="reader__icon-btn"
            onClick={(e) => {
              e.stopPropagation();
              setShowTOC(true);
            }}
            aria-label="章节目录"
          >
            目录
          </button>
          <div className="reader__progress-track">
            <div className="reader__progress-fill" style={{ width: `${pct}%` }} />
          </div>
          <button
            className="reader__icon-btn"
            onClick={(e) => {
              e.stopPropagation();
              setShowSettings(true);
            }}
            aria-label="阅读设置"
          >
            Aa
          </button>
        </footer>
      )}

      {/* --- TOC drawer ---------------------------------------------------- */}
      {showTOC && (
        <div className="drawer" onClick={() => setShowTOC(false)}>
          <aside
            className="drawer__panel"
            onClick={(e) => e.stopPropagation()}
            role="dialog"
            aria-label="章节目录"
          >
            <header className="drawer__header">
              <h3>目录</h3>
              <button className="drawer__close" onClick={() => setShowTOC(false)}>
                ✕
              </button>
            </header>
            <ul className="toc">
              {chapters.map((c) => (
                <li
                  key={c.idx}
                  className={c.idx === currentChapter ? 'toc__item toc__item--active' : 'toc__item'}
                >
                  <button onClick={() => onChapterClick(c.idx)}>{c.title}</button>
                </li>
              ))}
            </ul>
          </aside>
        </div>
      )}

      {/* --- Settings drawer ---------------------------------------------- */}
      {showSettings && (
        <div className="drawer" onClick={() => setShowSettings(false)}>
          <aside
            className="drawer__panel drawer__panel--narrow"
            onClick={(e) => e.stopPropagation()}
            role="dialog"
            aria-label="阅读设置"
          >
            <header className="drawer__header">
              <h3>阅读设置</h3>
              <button className="drawer__close" onClick={() => setShowSettings(false)}>
                ✕
              </button>
            </header>
            <div className="settings">
              <div className="settings__row">
                <span className="settings__label">主题</span>
                <div className="settings__themes">
                  {(['beige', 'white', 'grey', 'dark'] as Theme[]).map((t) => (
                    <button
                      key={t}
                      className={`theme-swatch theme-swatch--${t}${settings.theme === t ? ' is-active' : ''}`}
                      onClick={() => setSettings((s) => ({ ...s, theme: t }))}
                      aria-label={`主题 ${t}`}
                    />
                  ))}
                </div>
              </div>
              <div className="settings__row">
                <span className="settings__label">字号</span>
                <div className="settings__sizes">
                  {(['sm', 'md', 'lg', 'xl'] as FontSize[]).map((sz) => (
                    <button
                      key={sz}
                      className={`size-btn size-btn--${sz}${settings.size === sz ? ' is-active' : ''}`}
                      onClick={() => setSettings((s) => ({ ...s, size: sz }))}
                    >
                      A
                    </button>
                  ))}
                </div>
              </div>
            </div>
          </aside>
        </div>
      )}
    </div>
  );
}
