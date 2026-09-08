import { Fragment, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { MouseEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import * as api from '../api/client';
import { ReaderDrawer } from '../components/ReaderDrawer';
import { TOCList } from '../components/ReaderTOC';
import {
  BUILTIN_FONTS,
  DEFAULT_SETTINGS,
  GAP_LABELS,
  HINT_KEY,
  LINE_LABELS,
  SETTINGS_KEY,
  THEME_SWATCHES,
  WIDTH_LABELS,
  loadSettings,
  resolveTheme,
  saveSettings,
} from '../reader/settings';
import type {
  FontSize,
  IndentMode,
  LineHeight,
  PageWidth,
  ParagraphGap,
  Settings,
} from '../reader/settings';
import { useThrottledProgress } from '../hooks/useThrottledProgress';
import type { Book, Bookmark, Chapter, Progress, ReadingFont, SearchMatch } from '../types/api';
import './ReaderPage.css';

// Consistent line-icon set for the reader chrome — replaces the earlier mix
// of Chinese labels, emoji (☆ ⚙), and arrows so every control reads as part
// of one toolbar.
function Glyph({ children }: { children: React.ReactNode }) {
  return (
    <svg
      width="18"
      height="18"
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

const IconBack = () => (
  <Glyph>
    <path d="M15 5l-7 7 7 7" />
  </Glyph>
);
const IconSearch = () => (
  <Glyph>
    <circle cx="11" cy="11" r="7" />
    <path d="M20.5 20.5L16 16" />
  </Glyph>
);
const IconBookmarkPlus = () => (
  <Glyph>
    <path d="M6 4h12v16l-6-4-6 4z" />
    <path d="M12 8.5v4M10 10.5h4" />
  </Glyph>
);
const IconBookmark = () => (
  <Glyph>
    <path d="M6 4h12v16l-6-4-6 4z" />
  </Glyph>
);
const IconList = () => (
  <Glyph>
    <path d="M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01" />
  </Glyph>
);
const IconSettings = () => (
  <Glyph>
    <path d="M4 7h7M15 7h5" />
    <circle cx="13" cy="7" r="2" />
    <path d="M4 12h4M12 12h8" />
    <circle cx="10" cy="12" r="2" />
    <path d="M4 17h11M19 17h1" />
    <circle cx="17" cy="17" r="2" />
  </Glyph>
);
const IconChevronLeft = () => (
  <Glyph>
    <path d="M15 6l-6 6 6 6" />
  </Glyph>
);
const IconChevronRight = () => (
  <Glyph>
    <path d="M9 6l6 6-6 6" />
  </Glyph>
);

// 'auto' follows the shelf's own light/dark choice; the rest are
// explicit surfaces. See resolveTheme.
// Built-in families all resolve from device fonts; 'custom' means a file
// the user dropped into <data>/fonts, named by Settings.customFont.
// Server caps content slice at 50k chars per request.
const CHUNK = 50_000;

// How close (in viewport heights) to a loaded edge before we fetch the
// adjacent chapter.
const PREFETCH_TRIGGER = 1.0;


// The loaded window is trimmed from the top as you read forward. Without
// this it only ever grows: 300 chapters read in one sitting is 300
// chapters mounted, tens of thousands of paragraphs, and memory that is
// never returned until you leave the page.
//
// Nothing within KEEP_ABOVE_VIEWPORTS of the reading position is ever
// removed, so scrolling back up stays instant and the trim can never take
// content that is on screen. MIN_WINDOW_CHAPTERS keeps a floor for books
// whose chapters are a paragraph each.
const MAX_WINDOW_CHAPTERS = 12;
const MIN_WINDOW_CHAPTERS = 4;
const KEEP_ABOVE_VIEWPORTS = 3;

// Chapters kept as text after they leave the DOM, so stepping back into
// one costs a re-render and no network. Several times the mounted window,
// because that is the whole point of keeping them — but bounded, since
// otherwise a long session accumulates the entire book: measured at 1.6 MB
// of JS heap per 220 chapters of the largest book in the corpus, which
// extrapolates to tens of megabytes for reading it end to end.
//
// Evicting is safe by construction: every path that needs a chapter's text
// already fetches it when the cache does not have it.
const MAX_CACHED_CHAPTERS = 60;
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

// buildTOCTree folds the flat chapter list into a nested tree using
// isTypingTarget reports whether a key event is destined for somewhere the
// user is entering text. contentEditable is included for completeness even
// though the reader has none today — the cost of missing one is a key that
// silently does nothing.
function isTypingTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  if (!el || !el.tagName) return false;
  const tag = el.tagName.toLowerCase();
  return tag === 'input' || tag === 'textarea' || tag === 'select' || el.isContentEditable;
}

// renderChapter splits a chapter's formatted text into a title heading
// plus paragraph nodes. For any chapter ParseChapters extracted from a
// real header the formatter emits the title as its own paragraph at the
// chapter offset, so paragraphs[0] == metaTitle and we peel it. The
// synthetic "正文" entry (created when no headers were detected) lives
// in metadata only — the cached text starts straight with body prose,
// so paragraphs[0] is the first sentence; peeling it would render that
// sentence as an h2 (the "bolded first line" bug). When the leading
// paragraph doesn't match metaTitle, use the metadata title for the
// heading and keep every paragraph as body.
// markTerm splits a paragraph around every occurrence of `term`, so the
// hit you jumped to is visible instead of having to be found by eye. It
// works on the term rather than the match's character offset because the
// paragraph split trims and collapses separators, and the offsets do not
// survive that — matching the text is both simpler and highlights the
// other occurrences on screen, which is what a reader expects after a
// search.
function markTerm(text: string, term: string): React.ReactNode {
  if (!term) return text;
  const haystack = text.toLowerCase();
  const needle = term.toLowerCase();
  let from = 0;
  const out: React.ReactNode[] = [];
  for (;;) {
    const at = haystack.indexOf(needle, from);
    if (at < 0) break;
    if (at > from) out.push(text.slice(from, at));
    out.push(
      <mark key={at} className="reader__hit">
        {text.slice(at, at + term.length)}
      </mark>,
    );
    from = at + term.length;
  }
  if (out.length === 0) return text;
  if (from < text.length) out.push(text.slice(from));
  return out;
}

function renderChapter(
  idx: number,
  text: string,
  metaTitle: string,
  highlight: string,
): React.ReactNode {
  const paragraphs = text.split(/\n+/).map((p) => p.trim()).filter(Boolean);
  const hasTitleLine = paragraphs.length > 0 && paragraphs[0] === metaTitle;
  const titleText = hasTitleLine ? paragraphs[0] : metaTitle || paragraphs[0] || '';
  const bodyParas = hasTitleLine ? paragraphs.slice(1) : paragraphs;
  return (
    <Fragment key={idx}>
      <h2 id={`chap-${idx}`} className="reader__chapter">
        {titleText}
      </h2>
      {bodyParas.length > 0 && (
        <div className="reader__body">
          {bodyParas.map((p, i) => (
            <p key={i}>{markTerm(p, highlight)}</p>
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

  // cacheChapter stores a chapter's text and drops whichever cached
  // chapters are furthest from the one just read, so the cache tracks
  // where the reader is rather than everywhere they have been.
  const cacheChapter = useCallback((idx: number, text: string) => {
    setChapterText((prev) => {
      const next = new Map(prev).set(idx, text);
      if (next.size <= MAX_CACHED_CHAPTERS) return next;
      // Anything the window could be showing is off limits — evicting a
      // mounted chapter's text would blank it mid-read. The arithmetic
      // already protects them (the cache is several times the window, and
      // eviction starts from the far end), but stating it means a future
      // change to either bound cannot quietly break it.
      const protectedRange = MAX_WINDOW_CHAPTERS;
      const byDistance = [...next.keys()]
        .filter((k) => Math.abs(k - idx) > protectedRange)
        .sort((a, b) => Math.abs(b - idx) - Math.abs(a - idx));
      for (const key of byDistance) {
        if (next.size <= MAX_CACHED_CHAPTERS) break;
        next.delete(key);
      }
      return next;
    });
  }, []);

  // Currently rendered contiguous window of chapter idxs. Null until the
  // first chapter lands. Always a contiguous range (TOC jumps reset it).
  const [loadedRange, setLoadedRange] = useState<{ lo: number; hi: number } | null>(null);

  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [settings, setSettings] = useState<Settings>(loadSettings);
  const [customFonts, setCustomFonts] = useState<ReadingFont[]>([]);
  const [showSettings, setShowSettings] = useState(false);
  const [showTOC, setShowTOC] = useState(false);
  const [showSearch, setShowSearch] = useState(false);
  const [showBookmarks, setShowBookmarks] = useState(false);
  const [showChrome, setShowChrome] = useState(true);
  // One-time hint teaching the tap-to-toggle-chrome / tap-zone interaction.
  const [showHint, setShowHint] = useState(() => {
    try {
      return !localStorage.getItem(HINT_KEY);
    } catch {
      return false;
    }
  });

  const [currentChapter, setCurrentChapter] = useState(1);
  // Not state: this changes on nearly every scroll frame but is never
  // rendered — only read when adding a bookmark. As state it re-rendered
  // the whole reader, paragraphs included, once per frame of scrolling.
  const currentOffsetRef = useRef(0);
  const setCurrentOffset = (v: number) => {
    currentOffsetRef.current = v;
  };
  const [pct, setPct] = useState(0);
  const [bookmarks, setBookmarks] = useState<Bookmark[]>([]);
  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState<SearchMatch[]>([]);
  const [searchBusy, setSearchBusy] = useState(false);
  const [searchMsg, setSearchMsg] = useState<string | null>(null);
  const [searchTotal, setSearchTotal] = useState(0);
  // The term to mark in the rendered text. Set when following a search
  // result, cleared by any other kind of jump so it does not linger over
  // reading you did afterwards.
  const [highlightTerm, setHighlightTerm] = useState('');
  // The bookmark whose note is open for editing, if any.
  const [editingNote, setEditingNote] = useState<number | null>(null);
  const [searchNext, setSearchNext] = useState<number | null>(null);
  // Percentage being dragged on the footer progress bar, or null when
  // not scrubbing. Kept separate from `pct` so the bar tracks the finger
  // immediately while the (async, chapter-loading) jump only fires on
  // release.
  const [scrub, setScrub] = useState<number | null>(null);

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
  // Assigned once onScroll exists, further down. The jump-apply effect is
  // declared above it and has to invoke it.
  const onScrollRef = useRef<() => void>(() => {});
  // Set when a jump came from a search result, so the applied scroll can
  // be corrected onto the highlighted hit itself.
  const centreOnHitRef = useRef(false);

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
    setBookmarks([]);
    setSearchResults([]);
    setSearchMsg(null);
    setCurrentOffset(0);
    initialised.current = false;
    fetchingRef.current.clear();

    (async () => {
      try {
        const [{ book, chapters }, progress, bookmarkList] = await Promise.all([
          api.getBook(bookId),
          api.getProgress(bookId).catch(() => null as Progress | null),
          api.listBookmarks(bookId).catch(() => [] as Bookmark[]),
        ]);
        if (cancelled) return;
        setBook(book);
        setChapters(chapters);
        setBookmarks(bookmarkList);

        if (chapters.length === 0) {
          setLoading(false);
          initialised.current = true;
          return;
        }

        const total = book.char_count ?? 0;
        if (book.format === 'pdf-image') {
          const pageCount = Math.max(1, chapters.length || total || 1);
          const savedPage = progress?.chapter_idx || (progress ? progress.char_offset + 1 : 1);
          const page = Math.max(1, Math.min(pageCount, savedPage));
          setCurrentChapter(page);
          setCurrentOffset(page - 1);
          setPct(Math.round((page / pageCount) * 100));
          setLoading(false);
          initialised.current = true;
          return;
        }

        const targetIdx = progress
          ? chapterIdxAtOffset(progress.char_offset, chapters)
          : chapters[0].idx;

        // Queue the saved scroll position; the useLayoutEffect below
        // applies it once the initial chapter is committed to the DOM.
        // Doing this via raf before commit would race React's render and
        // land at scrollTop=0 (the symptom of "opens at chapter top").
        if (progress && progress.char_offset > 0) {
          pendingScrollRef.current = progress.char_offset;
          setCurrentOffset(progress.char_offset);
          if (total > 0) setPct(Math.round((progress.char_offset / total) * 100));
        } else {
          setCurrentOffset(0);
          setPct(0);
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
            cacheChapter(idx, t);
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

  // --- User-supplied fonts ------------------------------------------------
  // An empty <data>/fonts is the common case, so this is one cheap request
  // that usually returns []. A failure is not worth surfacing — the built-in
  // system stacks are always available.

  useEffect(() => {
    api.listFonts().then(setCustomFonts).catch(() => setCustomFonts([]));
  }, []);

  // Register the selected file under one fixed family name, which
  // .reader--font-custom points at. Using FontFace rather than an injected
  // <style> means swapping fonts replaces the old registration instead of
  // stacking up rules.
  useEffect(() => {
    if (settings.font !== 'custom' || !settings.customFont) return;
    const face = new FontFace('zreader-custom', `url(${JSON.stringify(api.fontURL(settings.customFont))})`);
    let cancelled = false;
    face
      .load()
      .then(() => {
        if (!cancelled) document.fonts.add(face);
      })
      .catch(() => {
        /* falls back to the stack behind it in .reader--font-custom */
      });
    return () => {
      cancelled = true;
      document.fonts.delete(face);
    };
  }, [settings.font, settings.customFont]);

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
      // The header title and the progress readout are only ever computed
      // from a scroll event, and a jump that lands on the scrollTop it
      // started at fires none — every jump into a freshly opened window
      // starts at 0, so opening chapter 2150 from the contents left the
      // header naming chapter 1 and the progress reading 0%. Worse, that
      // stale position is what the next progress write would save.
      onScrollRef.current();

      // A search result's offset only positions the reader approximately:
      // the offset-to-pixel mapping is linear over the chapter's measured
      // height, so the hit can land a screen away from where the jump
      // stops. The mark itself is exact, so once it is rendered, centre
      // the one nearest where we landed.
      if (centreOnHitRef.current) {
        centreOnHitRef.current = false;
        requestAnimationFrame(() => {
          const el = scrollRef.current;
          if (!el) return;
          const hits = [...el.querySelectorAll<HTMLElement>('.reader__hit')];
          if (hits.length === 0) return;
          const want = el.scrollTop + el.clientHeight / 2;
          const nearest = hits.reduce((best, h) =>
            Math.abs(h.offsetTop - want) < Math.abs(best.offsetTop - want) ? h : best,
          );
          el.scrollTo({
            top: Math.max(0, nearest.offsetTop - el.clientHeight / 3),
            behavior: 'instant',
          });
          onScrollRef.current();
        });
      }
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
      cacheChapter(next, text);
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
      cacheChapter(prev, text);
      setLoadedRange((r) => (r && r.lo - 1 === prev ? { ...r, lo: prev } : r));
      requestAnimationFrame(compensate);
    } finally {
      fetchingRef.current.delete(prev);
    }
  }, [bookId, book, chapters, chapterText, loadedRange]);

  // --- Ensure the loaded window is tall enough to scroll -------------------
  // If the rendered content fits inside the viewport (e.g. a TOC jump
  // landed on a 卷 header whose only content is the header line), the
  // user has nothing to scroll, so onScroll never fires and the normal
  // PREFETCH_TRIGGER path never kicks in — the page is stuck.
  //
  // After every loadedRange commit, measure scrollHeight vs clientHeight
  // synchronously (useLayoutEffect runs between commit and paint, so the
  // new chapter's DOM is in the tree). If we're under 1.5× viewport,
  // kick extendDown. extendDown updates loadedRange, this effect re-fires,
  // and the chain stops once content exceeds the threshold or EOF is
  // reached (extendDown self-checks chapters.findIndex).

  useLayoutEffect(() => {
    if (!loadedRange) return;
    const el = scrollRef.current;
    if (!el) return;
    if (el.scrollHeight <= el.clientHeight * 1.5) {
      void extendDown();
    }
  }, [loadedRange, extendDown]);

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

  // Where each loaded chapter starts, and the content padding, cached.
  //
  // This used to be a getElementById + offsetTop pair per loaded chapter,
  // plus a getComputedStyle, on every scroll event. Both are forced
  // synchronous layout, and the loaded window only grows as you read: at
  // 40 chapters that was ~98 layout reads per frame, and it climbs from
  // there for as long as the session lasts.
  //
  // scrollHeight is the invalidation key. Every way the table can go stale
  // — a chapter appended or prepended, a font swap, a settings change that
  // reflows the column — changes the scrolled content's height, and
  // reading one property is far cheaper than rebuilding the table.
  const topsCache = useRef<{
    key: string;
    padTop: number;
    tops: { idx: number; top: number }[];
  } | null>(null);

  const chapterTops = useCallback((el: HTMLElement, range: { lo: number; hi: number }) => {
    const key = `${range.lo}-${range.hi}-${el.scrollHeight}-${el.clientWidth}`;
    const cached = topsCache.current;
    if (cached && cached.key === key) return cached;
    const tops: { idx: number; top: number }[] = [];
    for (let idx = range.lo; idx <= range.hi; idx++) {
      const anchor = document.getElementById(`chap-${idx}`);
      if (anchor) tops.push({ idx, top: anchor.offsetTop });
    }
    const next = {
      key,
      padTop: parseFloat(getComputedStyle(el).paddingTop) || 0,
      tops,
    };
    topsCache.current = next;
    return next;
  }, []);

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
    const { tops, padTop } = chapterTops(el, loadedRange);
    const readingPos = scrollTop + padTop;
    const total = book.char_count ?? 0;
    let activeIdx = loadedRange.lo;
    let activeTop = 0;
    let activeHeight = el.scrollHeight;
    for (let i = 0; i < tops.length; i++) {
      const bottom = i + 1 < tops.length ? tops[i + 1].top : el.scrollHeight;
      if (readingPos + 1 < bottom) {
        activeIdx = tops[i].idx;
        activeTop = tops[i].top;
        activeHeight = Math.max(1, bottom - tops[i].top);
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
        setCurrentOffset(absOffset);
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

    // Drop chapters that are far enough above the viewport to be out of
    // reach of a scroll back. The text stays in chapterText, so returning
    // to one costs a re-render and no network; what is reclaimed is the
    // DOM, which is the part that grows without bound.
    if (loadedRange.hi - loadedRange.lo + 1 > MAX_WINDOW_CHAPTERS) {
      const limit = scrollTop - viewport * KEEP_ABOVE_VIEWPORTS;
      let newLo = loadedRange.lo;
      let removed = 0;
      for (const t of tops) {
        const above = t.top - padTop;
        if (above <= limit && loadedRange.hi - t.idx + 1 >= MIN_WINDOW_CHAPTERS) {
          newLo = t.idx;
          removed = above;
        }
      }
      if (newLo > loadedRange.lo && removed > 0) {
        setLoadedRange((r) => (r && r.lo < newLo ? { ...r, lo: newLo } : r));
        // Absolute target, not a relative nudge: by the time this runs the
        // browser may have adjusted scrollTop itself for the removed
        // content, and subtracting again would move the page twice.
        // `instant` for the reason extendUp documents — the container's
        // smooth scroll-behavior would otherwise animate over this.
        requestAnimationFrame(() => {
          const after = scrollRef.current;
          if (after) after.scrollTo({ top: Math.max(0, scrollTop - removed), behavior: 'instant' });
        });
      }
    }
  }, [book, chapters, loadedRange, report, extendDown, extendUp, chapterTops]);

  onScrollRef.current = onScroll;

  // Scroll events can arrive several times per frame; the work behind them
  // is only meaningful once per frame, and doing it more often just spends
  // layout reads the browser then throws away.
  const scrollFrame = useRef<number | null>(null);
  const onScrollThrottled = useCallback(() => {
    if (scrollFrame.current !== null) return;
    scrollFrame.current = requestAnimationFrame(() => {
      scrollFrame.current = null;
      onScrollRef.current();
    });
  }, []);
  useEffect(
    () => () => {
      if (scrollFrame.current !== null) cancelAnimationFrame(scrollFrame.current);
    },
    [],
  );

  // --- Keyboard nav -------------------------------------------------------

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const el = scrollRef.current;
      if (!el) return;
      // Escape still belongs to the reader — it closes the drawer the
      // field lives in — but every scrolling key must be left to whatever
      // is being typed into. Without this, Space in the in-book search box
      // paged the book and was swallowed by preventDefault, so a query
      // with a space in it could not be typed at all.
      if (e.key !== 'Escape' && isTypingTarget(e.target)) return;
      if (e.key === 'PageDown' || e.key === ' ' || e.key === 'ArrowDown' || e.key === 'ArrowRight') {
        el.scrollBy({ top: el.clientHeight * 0.9, behavior: 'smooth' });
        e.preventDefault();
      } else if (e.key === 'PageUp' || e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
        el.scrollBy({ top: -el.clientHeight * 0.9, behavior: 'smooth' });
        e.preventDefault();
      } else if (e.key === 'Escape') {
        if (showSettings || showTOC || showSearch || showBookmarks) {
          setShowSettings(false);
          setShowTOC(false);
          setShowSearch(false);
          setShowBookmarks(false);
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
  }, [flush, navigate, showBookmarks, showSearch, showSettings, showTOC]);

  // --- Click anywhere to toggle chrome ------------------------------------

  const dismissHint = useCallback(() => {
    try {
      localStorage.setItem(HINT_KEY, '1');
    } catch {
      /* ignore */
    }
    setShowHint(false);
  }, []);

  const onContentClick = useCallback((e: MouseEvent<HTMLDivElement>) => {
    const el = scrollRef.current;
    if (!el) return;
    const target = e.target as HTMLElement;
    if (target.closest('button,a,input,select,textarea')) return;
    // First tap dismisses the one-time hint instead of toggling chrome, so the
    // gesture that clears it is the same gesture it's teaching.
    if (showHint) {
      dismissHint();
      return;
    }
    if (window.matchMedia('(max-width: 700px)').matches) {
      const rect = el.getBoundingClientRect();
      const x = e.clientX - rect.left;
      if (x < rect.width * 0.32) {
        el.scrollBy({ top: -el.clientHeight * 0.9, behavior: 'smooth' });
        return;
      }
      if (x > rect.width * 0.68) {
        el.scrollBy({ top: el.clientHeight * 0.9, behavior: 'smooth' });
        return;
      }
    }
    setShowChrome((v) => !v);
  }, [showHint, dismissHint]);

  const onChapterClick = useCallback(
    (idx: number) => {
      setShowTOC(false);
      const target = chapters.find((c) => c.idx === idx);
      if (!target) return;
      setHighlightTerm('');
      void jumpToOffset(target.char_offset);
    },
    [chapters, jumpToOffset],
  );

  // `from` is a character offset, not a page number: the server resumes
  // the scan there. Passing 0 starts a new search and replaces the list;
  // anything else appends, so paging keeps what you have already seen.
  const runSearch = useCallback(
    async (from: number) => {
      const q = searchQuery.trim();
      if (!q) {
        setSearchMsg('请输入搜索内容');
        setSearchResults([]);
        setSearchTotal(0);
        setSearchNext(null);
        return;
      }
      setSearchBusy(true);
      setSearchMsg(null);
      try {
        const page = await api.searchBook(bookId, q, { from });
        setSearchResults((prev) => (from === 0 ? page.matches : [...prev, ...page.matches]));
        setSearchTotal(page.total);
        setSearchNext(page.next_from ?? null);
        if (from === 0 && page.matches.length === 0) {
          setSearchMsg('没有匹配结果');
        }
      } catch (err) {
        setSearchMsg(err instanceof Error ? err.message : String(err));
      } finally {
        setSearchBusy(false);
      }
    },
    [bookId, searchQuery],
  );

  const onSearch = useCallback(() => runSearch(0), [runSearch]);

  const onSearchResultClick = useCallback(
    (offset: number) => {
      setShowSearch(false);
      setHighlightTerm(searchQuery.trim());
      centreOnHitRef.current = true;
      void jumpToOffset(offset);
    },
    [jumpToOffset, searchQuery],
  );

  const onAddBookmark = useCallback(async () => {
    if (!book) return;
    try {
      const b = await api.addBookmark(bookId, {
        char_offset: currentOffsetRef.current,
        chapter_idx: currentChapter,
      });
      setBookmarks((prev) => [...prev, b].sort((a, b) => a.char_offset - b.char_offset));
      setShowBookmarks(true);
    } catch (err) {
      setSearchMsg(err instanceof Error ? err.message : String(err));
    }
  }, [book, bookId, currentChapter]);

  // Notes are edited in place rather than at creation: you drop a bookmark
  // mid-sentence and know what you wanted to say about it a moment later,
  // and the column, the API and the type all supported one already — there
  // was simply nowhere to type it.
  const onSaveBookmarkNote = useCallback(
    async (bookmarkId: number, note: string) => {
      setEditingNote(null);
      const current = bookmarks.find((b) => b.id === bookmarkId);
      if (!current || (current.note ?? '') === note) return;
      try {
        const updated = await api.updateBookmarkNote(bookId, bookmarkId, note);
        setBookmarks((prev) => prev.map((b) => (b.id === bookmarkId ? updated : b)));
      } catch (err) {
        setSearchMsg(err instanceof Error ? err.message : String(err));
      }
    },
    [bookId, bookmarks],
  );

  const onDeleteBookmark = useCallback(
    async (bookmarkId: number) => {
      try {
        await api.deleteBookmark(bookId, bookmarkId);
        setBookmarks((prev) => prev.filter((b) => b.id !== bookmarkId));
      } catch (err) {
        setSearchMsg(err instanceof Error ? err.message : String(err));
      }
    },
    [bookId],
  );

  const onBookmarkClick = useCallback(
    (offset: number) => {
      setShowBookmarks(false);
      setHighlightTerm('');
      void jumpToOffset(offset);
    },
    [jumpToOffset],
  );

  // The footer bar showed progress but could not be used to move — the
  // one place in the reader where the obvious gesture did nothing. It is
  // a range input rather than a div with pointer handlers so that
  // keyboard and assistive tech get seeking for free.
  const scrubChapterTitle = useCallback(
    (percent: number) => {
      const total = book?.char_count ?? 0;
      if (total === 0 || chapters.length === 0) return '';
      const offset = Math.round((percent / 100) * total);
      return chapters.find((c) => c.idx === chapterIdxAtOffset(offset, chapters))?.title ?? '';
    },
    [book, chapters],
  );

  const commitScrub = useCallback(() => {
    if (scrub === null) return;
    const total = book?.char_count ?? 0;
    setScrub(null);
    if (total > 0) {
      setHighlightTerm('');
      void jumpToOffset(Math.round((scrub / 100) * total));
    }
  }, [book, jumpToOffset, scrub]);

  const onResetSettings = useCallback(() => {
    try {
      localStorage.removeItem(SETTINGS_KEY);
    } catch {
      /* ignore */
    }
    setSettings(DEFAULT_SETTINGS);
  }, []);

  const pdfPageCount =
    book?.format === 'pdf-image' ? Math.max(1, chapters.length || book.char_count || 1) : 0;
  const goPDFPage = useCallback(
    (page: number) => {
      if (!book || book.format !== 'pdf-image') return;
      const count = Math.max(1, chapters.length || book.char_count || 1);
      const next = Math.max(1, Math.min(count, page));
      setCurrentChapter(next);
      setCurrentOffset(next - 1);
      setPct(Math.round((next / count) * 100));
      report({
        char_offset: next - 1,
        chapter_idx: next,
        chapter_offset: 0,
      });
    },
    [book, chapters.length, report],
  );

  // --- Render -------------------------------------------------------------

  const themeClass = `reader reader--theme-${resolveTheme(settings.theme)} reader--size-${settings.size} reader--font-${settings.font} reader--line-${settings.line} reader--gap-${settings.gap} reader--width-${settings.width} reader--indent-${settings.indent}`;
  const currentChapterTitle = chapters.find((c) => c.idx === currentChapter)?.title ?? '';

  if (book?.format === 'pdf-image') {
    const pdfSrc = `${api.bookSourceURL(bookId)}#page=${currentChapter}`;
    return (
      <div className={`${themeClass} reader--pdf`}>
        {showChrome && (
          <header className="reader__top">
            <Link
              to="/"
              className="reader__icon-btn"
              onClick={() => void flush()}
              aria-label="返回书架"
              title="返回书架"
            >
              <IconBack />
            </Link>
            <div className="reader__top-title">
              <div className="reader__book-title">{book.title}</div>
              <div className="reader__chap-title">
                {currentChapterTitle || `Page ${currentChapter}`}
              </div>
            </div>
            <div className="reader__pct">{pct}%</div>
          </header>
        )}

        <div
          ref={scrollRef}
          className="reader__content reader__content--pdf"
          onClick={onContentClick}
        >
          {error && <p className="reader__error">加载失败：{error}</p>}
          {loading && !error && <p className="reader__loading">加载中…</p>}
          {!loading && !error && (
            <iframe
              key={currentChapter}
              className="reader__pdf-frame"
              src={pdfSrc}
              title={book.title}
            />
          )}
        </div>

        {showChrome && (
          <footer className="reader__bottom reader__bottom--pdf">
            <button
              className="reader__icon-btn"
              onClick={(e) => {
                e.stopPropagation();
                goPDFPage(currentChapter - 1);
              }}
              disabled={currentChapter <= 1}
              aria-label="上一页"
              title="上一页"
            >
              <IconChevronLeft />
            </button>
            <button
              className="reader__icon-btn"
              onClick={(e) => {
                e.stopPropagation();
                setShowTOC(true);
              }}
              aria-label="页面目录"
              title="目录"
            >
              <IconList />
            </button>
            <div className="reader__pdf-page">
              {currentChapter} / {pdfPageCount}
            </div>
            <button
              className="reader__icon-btn"
              onClick={(e) => {
                e.stopPropagation();
                goPDFPage(currentChapter + 1);
              }}
              disabled={currentChapter >= pdfPageCount}
              aria-label="下一页"
              title="下一页"
            >
              <IconChevronRight />
            </button>
          </footer>
        )}

        {showTOC && (
          <ReaderDrawer
            title="目录"
            onClose={() => setShowTOC(false)}
            narrow
          >
            <ul className="toc toc--pdf">
              {chapters.map((c) => (
                <li
                  key={c.idx}
                  className={`toc__node toc__node--d0${c.idx === currentChapter ? ' toc__node--active' : ''}`}
                >
                  <button
                    onClick={() => {
                      setShowTOC(false);
                      goPDFPage(c.idx);
                    }}
                  >
                    {c.title}
                  </button>
                </li>
              ))}
            </ul>
          </ReaderDrawer>
        )}
      </div>
    );
  }

  const renderedChapters: React.ReactNode[] = [];
  if (loadedRange) {
    for (let idx = loadedRange.lo; idx <= loadedRange.hi; idx++) {
      const text = chapterText.get(idx);
      if (text === undefined) continue;
      const meta = chapters.find((c) => c.idx === idx);
      renderedChapters.push(renderChapter(idx, text, meta?.title ?? '', highlightTerm));
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
            title="返回书架"
          >
            <IconBack />
          </Link>
          <div className="reader__top-title">
            <div className="reader__book-title">{book?.title ?? ''}</div>
            <div className="reader__chap-title">{currentChapterTitle}</div>
          </div>
          <div className="reader__pct">{pct}%</div>
          <button
            className="reader__icon-btn"
            onClick={() => setShowSearch(true)}
            aria-label="搜索"
            title="搜索"
          >
            <IconSearch />
          </button>
          <button
            className="reader__icon-btn"
            onClick={onAddBookmark}
            aria-label="添加书签"
            title="添加书签"
          >
            <IconBookmarkPlus />
          </button>
        </header>
      )}

      <div
        ref={scrollRef}
        className="reader__content"
        onScroll={onScrollThrottled}
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

      {showHint && !loading && !error && (
        <div className="reader__hint" onClick={dismissHint}>
          <div className="reader__hint-card" onClick={(e) => e.stopPropagation()}>
            <p>
              点击页面<b>中部</b>，呼出 / 隐藏菜单
            </p>
            <p className="reader__hint-sub">移动端轻点屏幕<b>左右两侧</b>可翻页</p>
            <button type="button" className="reader__hint-ok" onClick={dismissHint}>
              知道了
            </button>
          </div>
        </div>
      )}

      {showChrome && (
        <footer className="reader__bottom">
          <button
            className="reader__icon-btn"
            onClick={(e) => {
              e.stopPropagation();
              setShowTOC(true);
            }}
            aria-label="章节目录"
            title="目录"
          >
            <IconList />
          </button>
          <button
            className="reader__icon-btn"
            onClick={(e) => {
              e.stopPropagation();
              setShowBookmarks(true);
            }}
            aria-label="书签"
            title="书签"
          >
            <IconBookmark />
          </button>
          <div className="reader__scrub" onClick={(e) => e.stopPropagation()}>
            {scrub !== null && (
              <div className="reader__scrub-tip" style={{ '--p': `${scrub}%` } as React.CSSProperties}>
                <b>{scrub}%</b>
                <span>{scrubChapterTitle(scrub)}</span>
              </div>
            )}
            <div className="reader__progress-track">
              <div
                className="reader__progress-fill"
                style={{ width: `${scrub ?? pct}%`, transition: scrub !== null ? 'none' : undefined }}
              />
            </div>
            <input
              type="range"
              className="reader__scrub-input"
              min={0}
              max={100}
              value={scrub ?? pct}
              aria-label="阅读进度"
              onChange={(e) => setScrub(Number(e.target.value))}
              onPointerUp={commitScrub}
              onPointerCancel={commitScrub}
              onKeyUp={commitScrub}
              onBlur={commitScrub}
            />
          </div>
          <button
            className="reader__icon-btn"
            onClick={(e) => {
              e.stopPropagation();
              setShowSettings(true);
            }}
            aria-label="阅读设置"
            title="设置"
          >
            <IconSettings />
          </button>
        </footer>
      )}

      {/* --- TOC drawer ---------------------------------------------------- */}
      {showTOC && (
        <ReaderDrawer title="章节目录" onClose={() => setShowTOC(false)}>
          <TOCList
            chapters={chapters}
            currentChapter={currentChapter}
            onJump={onChapterClick}
          />
        </ReaderDrawer>
      )}

      {/* --- Search drawer ------------------------------------------------- */}
      {showSearch && (
        <ReaderDrawer
            title="搜索"
            onClose={() => setShowSearch(false)}
          >
          <form
            className="reader-search"
            onSubmit={(e) => {
              e.preventDefault();
              void onSearch();
            }}
          >
            <input
              autoFocus
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder="搜索正文"
            />
            <button type="submit" disabled={searchBusy}>
              {searchBusy ? '搜索中…' : '搜索'}
            </button>
          </form>
          {searchMsg && <div className="drawer__message">{searchMsg}</div>}
          {searchTotal > 0 && (
            <div className="search-results__count">
              共 {searchTotal} 处，已显示 {searchResults.length}
            </div>
          )}
          <ul className="search-results">
            {searchResults.map((m) => (
              <li key={`${m.char_offset}-${m.chapter_idx}`}>
                <button onClick={() => onSearchResultClick(m.char_offset)}>
                  <span className="search-results__chapter">
                    {chapters.find((c) => c.idx === m.chapter_idx)?.title ?? `第 ${m.chapter_idx} 章`}
                  </span>
                  <span className="search-results__snippet">{m.snippet}</span>
                </button>
              </li>
            ))}
          </ul>
          {searchNext !== null && (
            <button
              type="button"
              className="search-results__more"
              disabled={searchBusy}
              onClick={() => void runSearch(searchNext)}
            >
              {searchBusy ? '加载中…' : '加载更多'}
            </button>
          )}
        </ReaderDrawer>
      )}

      {/* --- Bookmark drawer ---------------------------------------------- */}
      {showBookmarks && (
        <ReaderDrawer
            title="书签"
            onClose={() => setShowBookmarks(false)}
          >
          <div className="bookmark-actions">
            <button type="button" onClick={onAddBookmark}>
              添加当前位置
            </button>
          </div>
          {bookmarks.length === 0 ? (
            <div className="drawer__message">还没有书签</div>
          ) : (
            <ul className="bookmark-list">
              {bookmarks.map((b) => {
                const chapterTitle =
                  chapters.find((c) => c.idx === b.chapter_idx)?.title ??
                  `位置 ${b.char_offset.toLocaleString()}`;
                return (
                  <li key={b.id}>
                    <div className="bookmark-list__row">
                      <button className="bookmark-list__jump" onClick={() => onBookmarkClick(b.char_offset)}>
                        <span>{chapterTitle}</span>
                        <small>{b.char_offset.toLocaleString()} 字</small>
                      </button>
                      <button className="bookmark-list__delete" onClick={() => void onDeleteBookmark(b.id)}>
                        删除
                      </button>
                    </div>
                    {editingNote === b.id ? (
                      <input
                        autoFocus
                        className="bookmark-list__note-input"
                        defaultValue={b.note ?? ''}
                        maxLength={500}
                        placeholder="写点什么…"
                        onBlur={(e) => void onSaveBookmarkNote(b.id, e.target.value.trim())}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') e.currentTarget.blur();
                          if (e.key === 'Escape') {
                            e.stopPropagation();
                            setEditingNote(null);
                          }
                        }}
                      />
                    ) : (
                      <button
                        type="button"
                        className={`bookmark-list__note${b.note ? '' : ' is-empty'}`}
                        onClick={() => setEditingNote(b.id)}
                      >
                        {b.note || '添加备注'}
                      </button>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </ReaderDrawer>
      )}

      {/* --- Settings drawer ---------------------------------------------- */}
      {showSettings && (
        <ReaderDrawer
            title="阅读设置"
            onClose={() => setShowSettings(false)}
          >
          <div className="settings">
            <div className="settings__row">
              <span className="settings__label">主题</span>
              <div className="settings__themes">
                {THEME_SWATCHES.map((t) => (
                  <button
                    key={t.key}
                    className={`theme-swatch theme-swatch--${t.key}${settings.theme === t.key ? ' is-active' : ''}`}
                    onClick={() => setSettings((s) => ({ ...s, theme: t.key }))}
                    aria-label={t.label}
                    aria-pressed={settings.theme === t.key}
                    title={t.label}
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
                    aria-pressed={settings.size === sz}
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
                {BUILTIN_FONTS.map((f) => (
                  <button
                    key={f.key}
                    className={`font-btn font-btn--${f.key}${settings.font === f.key ? ' is-active' : ''}`}
                    aria-pressed={settings.font === f.key}
                    onClick={() => setSettings((s) => ({ ...s, font: f.key }))}
                  >
                    {f.label}
                  </button>
                ))}
              </div>
              {customFonts.length > 0 && (
                <div className="settings__fonts settings__fonts--custom">
                  {customFonts.map((f) => (
                    <button
                      key={f.file}
                      className={`font-btn font-btn--custom${
                        settings.font === 'custom' && settings.customFont === f.file ? ' is-active' : ''
                      }`}
                      onClick={() => setSettings((s) => ({ ...s, font: 'custom', customFont: f.file }))}
                      title={f.name}
                    >
                      {f.name}
                    </button>
                  ))}
                </div>
              )}
              <p className="settings__hint">
                内置字体全部来自系统，不联网。把 woff2 / ttf 放进 <code>&lt;data&gt;/fonts</code> 可以加入这个列表。
              </p>
            </div>
            <div className="settings__row">
              <span className="settings__label">行距</span>
              <div className="settings__seg">
                {(['compact', 'normal', 'loose'] as LineHeight[]).map((v) => (
                  <button
                    key={v}
                    className={settings.line === v ? 'is-active' : ''}
                    aria-pressed={settings.line === v}
                    onClick={() => setSettings((s) => ({ ...s, line: v }))}
                  >
                    {LINE_LABELS[v]}
                  </button>
                ))}
              </div>
            </div>
            <div className="settings__row">
              <span className="settings__label">段距</span>
              <div className="settings__seg">
                {(['compact', 'normal', 'loose'] as ParagraphGap[]).map((v) => (
                  <button
                    key={v}
                    className={settings.gap === v ? 'is-active' : ''}
                    aria-pressed={settings.gap === v}
                    onClick={() => setSettings((s) => ({ ...s, gap: v }))}
                  >
                    {GAP_LABELS[v]}
                  </button>
                ))}
              </div>
            </div>
            <div className="settings__row">
              <span className="settings__label">版面</span>
              <div className="settings__seg">
                {(['narrow', 'normal', 'wide'] as PageWidth[]).map((v) => (
                  <button
                    key={v}
                    className={settings.width === v ? 'is-active' : ''}
                    aria-pressed={settings.width === v}
                    onClick={() => setSettings((s) => ({ ...s, width: v }))}
                  >
                    {WIDTH_LABELS[v]}
                  </button>
                ))}
              </div>
            </div>
            <div className="settings__row">
              <span className="settings__label">首行缩进</span>
              <div className="settings__seg">
                {(['indent', 'flush'] as IndentMode[]).map((v) => (
                  <button
                    key={v}
                    className={settings.indent === v ? 'is-active' : ''}
                    aria-pressed={settings.indent === v}
                    onClick={() => setSettings((s) => ({ ...s, indent: v }))}
                  >
                    {v === 'indent' ? '开启' : '关闭'}
                  </button>
                ))}
              </div>
            </div>
            <button
              type="button"
              className="settings__reset"
              onClick={onResetSettings}
            >
              恢复默认设置
            </button>
          </div>
        </ReaderDrawer>
      )}
    </div>
  );
}
