import { Fragment, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import * as api from '../api/client';
import { useThrottledProgress } from '../hooks/useThrottledProgress';
import type { Book, Chapter, Progress } from '../types/api';
import './ReaderPage.css';

type Theme = 'beige' | 'white' | 'grey' | 'dark';
type FontSize = 'sm' | 'md' | 'lg' | 'xl';
type FontFamily = 'system' | 'songti' | 'wenkai';

type Settings = { theme: Theme; size: FontSize; font: FontFamily };

const DEFAULT_SETTINGS: Settings = { theme: 'grey', size: 'md', font: 'songti' };

const FONT_LABELS: Record<FontFamily, string> = {
  system: '系统默认',
  songti: '思源宋体',
  wenkai: '霞鹜文楷',
};
const SETTINGS_KEY = 'zreader.settings';

// Server caps content slice at 50k chars per request.
const CHUNK = 50_000;

// How close (in viewport heights) to a loaded edge before we fetch the
// adjacent chapter.
const PREFETCH_TRIGGER = 1.0;

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

// chapterCharRange returns [start, len) in char-offset units for a chapter
// idx — the slice the reader needs to ask the backend for. The last
// chapter's length is bounded by book.char_count.
function chapterCharRange(
  idx: number,
  chapters: Chapter[],
  total: number,
): { start: number; len: number } | null {
  const i = chapters.findIndex((c) => c.idx === idx);
  if (i < 0) return null;
  const start = chapters[i].char_offset;
  const end = i + 1 < chapters.length ? chapters[i + 1].char_offset : total;
  return { start, len: Math.max(0, end - start) };
}

// fetchChapter pulls one chapter's text, chunked across CHUNK-sized slices
// when needed. Returns the concatenated content with the chapter title
// line as paragraph 0.
async function fetchChapter(
  bookId: number,
  idx: number,
  chapters: Chapter[],
  total: number,
): Promise<string> {
  const range = chapterCharRange(idx, chapters, total);
  if (!range || range.len === 0) return '';
  let buf = '';
  let cursor = range.start;
  const endChar = range.start + range.len;
  while (cursor < endChar) {
    const slice = await api.getContent(bookId, cursor, Math.min(CHUNK, endChar - cursor));
    buf += slice.text;
    cursor = slice.from + slice.len;
    if (slice.eof || slice.len === 0) break;
  }
  return buf;
}

function chapterIdxAtOffset(offset: number, chapters: Chapter[]): number {
  if (chapters.length === 0) return 1;
  let idx = chapters[0].idx;
  for (const c of chapters) {
    if (c.char_offset <= offset) idx = c.idx;
    else break;
  }
  return idx;
}

// renderChapter splits a chapter's formatted text into a title heading
// plus paragraph nodes. The backend's format pass emits chapters as
// `<title>\n\n<para>\n\n<para>…`, so the first paragraph (split on blank
// lines, trimmed) is the title.
function renderChapter(
  idx: number,
  text: string,
  fallbackTitle: string,
): React.ReactNode {
  const paragraphs = text.split(/\n+/).map((p) => p.trim()).filter(Boolean);
  const titleText = paragraphs[0] ?? fallbackTitle;
  const bodyParas = paragraphs.slice(1);
  return (
    <Fragment key={idx}>
      <h2 id={`chap-${idx}`} className="reader__chapter">
        {titleText}
      </h2>
      {bodyParas.length > 0 && (
        <div className="reader__body">
          {bodyParas.map((p, i) => (
            <p key={i}>{p}</p>
          ))}
        </div>
      )}
    </Fragment>
  );
}

export function ReaderPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const bookId = Number(id);

  const [book, setBook] = useState<Book | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);

  // Per-chapter content cache. We never evict — re-visiting a previously
  // read chapter via scroll-up or TOC jump is a memory hit, not a refetch.
  const [chapterText, setChapterText] = useState<Map<number, string>>(new Map());
  // Currently rendered contiguous window of chapter idxs. Null until the
  // first chapter lands. Always a contiguous range (TOC jumps reset it).
  const [loadedRange, setLoadedRange] = useState<{ lo: number; hi: number } | null>(null);

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
  // Track in-flight chapter fetches to dedupe scroll-burst extension calls.
  const fetchingRef = useRef<Set<number>>(new Set());
  // Char-offset waiting to be applied to scrollTop after the target
  // chapter's DOM is committed. Used by both cold start and cross-window
  // TOC jumps; the useLayoutEffect below consumes it when the right
  // chapter is in the DOM.
  const pendingScrollRef = useRef<number | null>(null);

  const { report, flush } = useThrottledProgress({
    bookId,
    onConflict: (server) => {
      // Server has a newer position from another device — adopt it.
      // Falls back to a chapter jump (precise within-chapter offset is
      // skipped for simplicity).
      void jumpToOffset(server.char_offset);
    },
  });

  // --- Initial load -------------------------------------------------------
  // Loads only the chapter containing the saved progress (or chapter 1 if
  // no saved progress). Adjacent chapters are pre-fetched in the
  // background after first paint.

  useEffect(() => {
    if (!Number.isFinite(bookId)) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    setChapterText(new Map());
    setLoadedRange(null);
    initialised.current = false;
    fetchingRef.current.clear();

    (async () => {
      try {
        const [{ book, chapters }, progress] = await Promise.all([
          api.getBook(bookId),
          api.getProgress(bookId).catch(() => null as Progress | null),
        ]);
        if (cancelled) return;
        setBook(book);
        setChapters(chapters);

        if (chapters.length === 0) {
          setLoading(false);
          initialised.current = true;
          return;
        }

        const total = book.char_count ?? 0;
        const targetIdx = progress
          ? chapterIdxAtOffset(progress.char_offset, chapters)
          : chapters[0].idx;

        // Queue the saved scroll position; the useLayoutEffect below
        // applies it once the initial chapter is committed to the DOM.
        // Doing this via raf before commit would race React's render and
        // land at scrollTop=0 (the symptom of "opens at chapter top").
        if (progress && progress.char_offset > 0) {
          pendingScrollRef.current = progress.char_offset;
        } else {
          initialised.current = true;
        }

        const text = await fetchChapter(bookId, targetIdx, chapters, total);
        if (cancelled) return;
        setChapterText(new Map([[targetIdx, text]]));
        setLoadedRange({ lo: targetIdx, hi: targetIdx });
        setCurrentChapter(targetIdx);
        setLoading(false);

        // Pre-fetch ±1 into the cache so adjacent navigation feels
        // instant. We deliberately DO NOT extend loadedRange on the
        // prev side here — prepending DOM above the current scroll
        // position would push the reader visibly downward (the "jumps
        // to chapter 1" bug). Instead, extendUp consults the cache on
        // first upward scroll and prepends *with* scrollTop
        // compensation in one frame, no visible jump.
        const prefetchCache = async (idx: number) => {
          if (cancelled || idx === targetIdx) return;
          if (chapters.findIndex((c) => c.idx === idx) < 0) return;
          if (fetchingRef.current.has(idx)) return;
          fetchingRef.current.add(idx);
          try {
            const t = await fetchChapter(bookId, idx, chapters, total);
            if (cancelled) return;
            setChapterText((prev) => {
              if (prev.has(idx)) return prev;
              const next = new Map(prev);
              next.set(idx, t);
              return next;
            });
            // Only auto-extend on the trailing edge — appending below
            // current scroll never jumps the user's viewport.
            if (idx === targetIdx + 1) {
              setLoadedRange((r) => (r && r.hi + 1 === idx ? { ...r, hi: idx } : r));
            }
          } finally {
            fetchingRef.current.delete(idx);
          }
        };
        void prefetchCache(targetIdx + 1);
        void prefetchCache(targetIdx - 1);
      } catch (err) {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : String(err));
        setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bookId]);

  // --- Persist settings ---------------------------------------------------

  useEffect(() => {
    saveSettings(settings);
  }, [settings]);

  // --- Scroll-to-offset, single source of truth --------------------------
  // Walks the DOM for the chapter that contains `charOffset` (must be in
  // the currently loaded window) and sets el.scrollTop based on the
  // chapter's measured top + within-chapter ratio. Returns false when
  // anchoring isn't possible yet (DOM not committed / out of window) so
  // the caller can keep the pending request alive.

  const applyScrollFromOffset = useCallback(
    (charOffset: number): boolean => {
      if (!book || chapters.length === 0 || !loadedRange) return false;
      const el = scrollRef.current;
      if (!el) return false;

      const targetIdx = chapterIdxAtOffset(charOffset, chapters);
      if (targetIdx < loadedRange.lo || targetIdx > loadedRange.hi) return false;

      const anchor = document.getElementById(`chap-${targetIdx}`);
      if (!anchor) return false;

      const total = book.char_count ?? 0;
      const ch = chapters.find((c) => c.idx === targetIdx);
      const range = chapterCharRange(targetIdx, chapters, total);
      const next = document.getElementById(`chap-${targetIdx + 1}`);
      const chapterTop = anchor.offsetTop;
      const chapterBottom = next ? next.offsetTop : el.scrollHeight;
      const chapterHeight = Math.max(0, chapterBottom - chapterTop);
      const within =
        ch && range && range.len > 0
          ? Math.max(0, Math.min(1, (charOffset - ch.char_offset) / range.len))
          : 0;

      // Back off by the container's top padding (which exists to clear
      // the absolute-positioned chrome bar). Without this the anchor
      // lands at viewport y=0, behind the chrome — the symptom the user
      // sees as "TOC jumps to the wrong place" (title hidden, body
      // appears to start mid-chapter).
      const padTop = parseFloat(getComputedStyle(el).paddingTop) || 0;
      const target = Math.max(0, chapterTop + within * chapterHeight - padTop);
      // behavior: 'instant' bypasses the container's CSS scroll-behavior:
      // smooth. A bare `scrollTop = X` starts a smooth animation that
      // overlaps the subsequent onScroll → extendUp → compensate chain;
      // the animation then wins and compensate is lost, leaving the
      // reader rendered one chapter short of the saved offset.
      el.scrollTo({ top: target, behavior: 'instant' });
      return true;
    },
    [book, chapters, loadedRange],
  );

  // --- Apply pending scroll after DOM commit -----------------------------
  // Single layout effect handles both cold start AND cross-window TOC
  // jumps. Anything that needs to scroll to a specific char_offset
  // *after* the target chapter is rendered just sets pendingScrollRef
  // and triggers a state change; this effect picks it up.
  //
  // useLayoutEffect (not rAF) is critical here: it runs synchronously
  // after React's commit and before the browser paints, so the new
  // chapter's anchor and scrollHeight are measurable. rAF in async
  // contexts can race the commit and was the root cause of "opens at
  // chapter top" + "TOC jumps to wrong chapter".

  useLayoutEffect(() => {
    if (pendingScrollRef.current === null) return;
    const applied = applyScrollFromOffset(pendingScrollRef.current);
    if (applied) {
      pendingScrollRef.current = null;
      initialised.current = true;
    }
  }, [loadedRange, chapters, book, applyScrollFromOffset]);

  // --- Window extension on scroll ----------------------------------------
  // When the viewport gets within PREFETCH_TRIGGER × clientHeight of an
  // edge, fetch the adjacent chapter and grow the window. extendUp also
  // restores scrollTop so prepending content doesn't visibly jump the
  // reader downward.

  const extendDown = useCallback(async () => {
    if (!book || !loadedRange) return;
    const total = book.char_count ?? 0;
    const next = loadedRange.hi + 1;
    if (chapters.findIndex((c) => c.idx === next) < 0) return;
    if (fetchingRef.current.has(next)) return;
    fetchingRef.current.add(next);
    try {
      const text = await fetchChapter(bookId, next, chapters, total);
      setChapterText((prev) => new Map(prev).set(next, text));
      setLoadedRange((r) => (r && r.hi + 1 === next ? { ...r, hi: next } : r));
    } finally {
      fetchingRef.current.delete(next);
    }
  }, [bookId, book, chapters, loadedRange]);

  const extendUp = useCallback(async () => {
    if (!book || !loadedRange) return;
    const total = book.char_count ?? 0;
    const prev = loadedRange.lo - 1;
    if (chapters.findIndex((c) => c.idx === prev) < 0) return;
    if (fetchingRef.current.has(prev)) return;

    const el = scrollRef.current;
    const prevScrollHeight = el?.scrollHeight ?? 0;
    const prevScrollTop = el?.scrollTop ?? 0;

    // The compensation pass runs after React commits the prepended
    // chapter. We schedule it via rAF so the new layout is measurable;
    // useLayoutEffect would be tighter, but only fires after the next
    // dep change, which is exactly the loadedRange transition we
    // trigger here — too brittle, easier to stick with rAF.
    //
    // `behavior: 'instant'` is critical — without it the container's
    // CSS `scroll-behavior: smooth` overlaps any concurrent programmatic
    // scroll (e.g. cold-start applyScroll → triggers onScroll →
    // triggers extendUp), and the in-flight animation overwrites this
    // compensation — leaving the reader rendered one chapter above its
    // intended position.
    const compensate = () => {
      const after = scrollRef.current;
      if (!after) return;
      const delta = after.scrollHeight - prevScrollHeight;
      if (delta > 0) {
        after.scrollTo({ top: prevScrollTop + delta, behavior: 'instant' });
      }
    };

    // Cache hit — no network round trip, just unhide the chapter.
    if (chapterText.has(prev)) {
      setLoadedRange((r) => (r && r.lo - 1 === prev ? { ...r, lo: prev } : r));
      requestAnimationFrame(compensate);
      return;
    }

    fetchingRef.current.add(prev);
    try {
      const text = await fetchChapter(bookId, prev, chapters, total);
      setChapterText((p) => new Map(p).set(prev, text));
      setLoadedRange((r) => (r && r.lo - 1 === prev ? { ...r, lo: prev } : r));
      requestAnimationFrame(compensate);
    } finally {
      fetchingRef.current.delete(prev);
    }
  }, [bookId, book, chapters, chapterText, loadedRange]);

  // --- Jump to absolute char offset (TOC click, conflict resolution) ------

  const jumpToOffset = useCallback(
    async (charOffset: number) => {
      if (!book || chapters.length === 0) return;
      const total = book.char_count ?? 0;
      const targetIdx = chapterIdxAtOffset(charOffset, chapters);

      const inWindow =
        loadedRange && targetIdx >= loadedRange.lo && targetIdx <= loadedRange.hi;
      if (inWindow) {
        // Chapter is already in DOM — scroll synchronously, no render trip.
        applyScrollFromOffset(charOffset);
        return;
      }

      // Target chapter is outside the loaded window. Queue the scroll,
      // then mutate state to render the new chapter. The layout effect
      // fires after React commits the new chapter and applies the scroll
      // — using rAF here was racing the commit and landed on stale DOM
      // (the "click 5, see 2" bug).
      pendingScrollRef.current = charOffset;
      setLoading(true);
      const text = await fetchChapter(bookId, targetIdx, chapters, total);
      setChapterText(new Map([[targetIdx, text]]));
      setLoadedRange({ lo: targetIdx, hi: targetIdx });
      setLoading(false);
    },
    [bookId, book, chapters, loadedRange, applyScrollFromOffset],
  );

  // --- Scroll → progress + extension triggers ----------------------------

  const onScroll = useCallback(() => {
    if (!initialised.current || !book || !loadedRange) return;
    const el = scrollRef.current;
    if (!el) return;

    // The "reading position" is the scroll-content row that sits just
    // below the chrome bar — i.e. scrollTop + paddingTop. Using bare
    // scrollTop here would report progress for a row that's behind the
    // chrome, and pair with applyScrollFromOffset's padTop offset to
    // misclassify the active chapter right after a TOC jump.
    const viewport = el.clientHeight;
    const scrollTop = el.scrollTop;
    const padTop = parseFloat(getComputedStyle(el).paddingTop) || 0;
    const readingPos = scrollTop + padTop;
    const total = book.char_count ?? 0;

    let activeIdx = loadedRange.lo;
    let activeTop = 0;
    let activeHeight = el.scrollHeight;
    for (let idx = loadedRange.lo; idx <= loadedRange.hi; idx++) {
      const anchor = document.getElementById(`chap-${idx}`);
      if (!anchor) continue;
      const next = document.getElementById(`chap-${idx + 1}`);
      const top = anchor.offsetTop;
      const bottom = next ? next.offsetTop : el.scrollHeight;
      if (readingPos + 1 < bottom) {
        activeIdx = idx;
        activeTop = top;
        activeHeight = Math.max(1, bottom - top);
        break;
      }
    }
    setCurrentChapter(activeIdx);

    if (total > 0) {
      const ch = chapters.find((c) => c.idx === activeIdx);
      if (ch) {
        const within = Math.max(0, Math.min(1, (readingPos - activeTop) / activeHeight));
        const range = chapterCharRange(activeIdx, chapters, total);
        const len = range?.len ?? 0;
        const absOffset = Math.min(total, ch.char_offset + Math.floor(within * len));
        setPct(Math.round((absOffset / total) * 100));
        report({
          char_offset: absOffset,
          chapter_idx: activeIdx,
          chapter_offset: Math.max(0, absOffset - ch.char_offset),
        });
      }
    }

    // Trigger window extension when within PREFETCH_TRIGGER viewports of
    // an edge.
    if (el.scrollHeight - (scrollTop + viewport) < viewport * PREFETCH_TRIGGER) {
      void extendDown();
    }
    if (scrollTop < viewport * PREFETCH_TRIGGER) {
      void extendUp();
    }
  }, [book, chapters, loadedRange, report, extendDown, extendUp]);

  // --- Keyboard nav -------------------------------------------------------

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

  const onChapterClick = useCallback(
    (idx: number) => {
      setShowTOC(false);
      const target = chapters.find((c) => c.idx === idx);
      if (!target) return;
      void jumpToOffset(target.char_offset);
    },
    [chapters, jumpToOffset],
  );

  const onResetSettings = useCallback(() => {
    try {
      localStorage.removeItem(SETTINGS_KEY);
    } catch {
      /* ignore */
    }
    setSettings(DEFAULT_SETTINGS);
  }, []);

  // --- Render -------------------------------------------------------------

  const themeClass = `reader reader--theme-${settings.theme} reader--size-${settings.size} reader--font-${settings.font}`;
  const currentChapterTitle = chapters.find((c) => c.idx === currentChapter)?.title ?? '';

  const renderedChapters: React.ReactNode[] = [];
  if (loadedRange) {
    for (let idx = loadedRange.lo; idx <= loadedRange.hi; idx++) {
      const text = chapterText.get(idx);
      if (text === undefined) continue;
      const meta = chapters.find((c) => c.idx === idx);
      renderedChapters.push(renderChapter(idx, text, meta?.title ?? ''));
    }
  }

  const atBookEnd =
    loadedRange !== null &&
    chapters.length > 0 &&
    loadedRange.hi === chapters[chapters.length - 1].idx;

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
          <button
            className="reader__icon-btn"
            onClick={() => setShowSettings(true)}
            aria-label="阅读设置"
            title="设置"
          >
            ⚙
          </button>
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
            {renderedChapters}
            {atBookEnd && <div className="reader__end">— 完 —</div>}
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
              <button
                type="button"
                className="settings__reset"
                onClick={onResetSettings}
              >
                恢复默认设置
              </button>
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
              <div className="settings__row">
                <span className="settings__label">字体</span>
                <div className="settings__fonts">
                  {(['system', 'songti', 'wenkai'] as FontFamily[]).map((f) => (
                    <button
                      key={f}
                      className={`font-btn font-btn--${f}${settings.font === f ? ' is-active' : ''}`}
                      onClick={() => setSettings((s) => ({ ...s, font: f }))}
                    >
                      {FONT_LABELS[f]}
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
