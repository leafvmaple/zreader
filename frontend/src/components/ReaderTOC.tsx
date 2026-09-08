import { useLayoutEffect, useMemo, useRef } from 'react';
import type { Chapter } from '../types/api';
import { useWindowedList } from '../hooks/useWindowedList';

// Chapter.level as 0-indexed depth. Each entry is attached as a child
// of the most recent ancestor with a strictly smaller level; entries
// with no such ancestor become roots. The result mirrors the EPUB nav
// tree the backend wrote, with no special-casing for "volume" vs
// "chapter" — depth alone drives visual rendering.
type TOCNode = { chapter: Chapter; children: TOCNode[] };

// A flattened tree row. Depth drives the indent class; `firstChild` stands
// in for the `:first-child` selector the nested markup used to satisfy,
// which flattening would otherwise apply only to the very first row.
type TOCRow = { chapter: Chapter; depth: number; container: boolean; firstChild: boolean };

function buildTOCTree(chapters: Chapter[]): TOCNode[] {
  const roots: TOCNode[] = [];
  const stack: TOCNode[] = [];
  for (const c of chapters) {
    const node: TOCNode = { chapter: c, children: [] };
    while (stack.length > 0 && stack[stack.length - 1].chapter.level >= c.level) {
      stack.pop();
    }
    if (stack.length === 0) {
      roots.push(node);
    } else {
      stack[stack.length - 1].children.push(node);
    }
    stack.push(node);
  }
  return roots;
}

// flattenTOC walks the tree into the row order the drawer renders.
//
// The nesting was only ever carrying indent, and the indent comes from the
// per-depth padding classes — .toc__sublist has no padding of its own — so
// a flat list renders identically. Flat is what lets the drawer window: a
// 4670-chapter book mounted ~9,000 nodes on open, every one of them laid
// out before the first row could be shown.
function flattenTOC(nodes: TOCNode[], depth: number, out: TOCRow[]): TOCRow[] {
  nodes.forEach((node, i) => {
    out.push({
      chapter: node.chapter,
      depth,
      container: node.children.length > 0,
      firstChild: i === 0,
    });
    if (node.children.length > 0) flattenTOC(node.children, depth + 1, out);
  });
  return out;
}

// MAX_TOC_DEPTH caps the per-depth CSS class for indent / typography.
// Deeper levels still render — they just share styling with the last
// styled depth. Three depths cover every shape our parser produces
// today (部 / 卷 / 章); push this up if we ever support 4+ tiers.
const MAX_TOC_DEPTH = 3;

// TOC_ROW_ESTIMATE is the height of a single-line leaf row, used for rows
// that have not been rendered yet. Container rows and wrapped titles are
// taller; those correct themselves once measured.
const TOC_ROW_ESTIMATE = 40;

// TOC_WINDOW_THRESHOLD: below this, mounting the whole list costs less
// than the windowing does, and find-in-page keeps working across it.
const TOC_WINDOW_THRESHOLD = 80;

export function TOCList({
  chapters,
  currentChapter,
  onJump,
}: {
  chapters: Chapter[];
  currentChapter: number;
  onJump: (idx: number) => void;
}): React.ReactNode {
  const rows = useMemo(() => flattenTOC(buildTOCTree(chapters), 0, []), [chapters]);
  // Two elements on purpose: the scroller owns the height, the list owns
  // the spacers. See the note in useWindowedList's container branch.
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const listRef = useRef<HTMLUListElement | null>(null);
  const windowed = rows.length > TOC_WINDOW_THRESHOLD;

  const { start, end, padTop, padBottom, measure, offsetOf } = useWindowedList({
    count: rows.length,
    estimate: TOC_ROW_ESTIMATE,
    containerRef: listRef,
    scrollRef,
    enabled: windowed,
  });

  const activeIndex = useMemo(
    () => rows.findIndex((r) => r.chapter.idx === currentChapter),
    [rows, currentChapter],
  );

  // Open the TOC where the reader actually is. Without this a 200-chapter
  // book always opened at chapter 1 and you had to hunt for the highlight.
  // Runs once per mount — the drawer unmounts on close, so reopening
  // re-centres on the chapter you moved to.
  //
  // scrollIntoView cannot be used while windowed: the active row is not
  // mounted yet when the list has thousands of entries. Scrolling to its
  // computed offset renders it, and a second pass corrects for the
  // difference between the estimate and the rows' real heights.
  const centred = useRef(false);
  // offsetOf is rebuilt every render, so it cannot be an effect dependency:
  // the effect would re-run on every scroll and yank the list back to the
  // current chapter the moment you moved away from it.
  const offsetOfRef = useRef(offsetOf);
  offsetOfRef.current = offsetOf;

  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el || activeIndex < 0 || centred.current) return;
    centred.current = true;
    if (!windowed) {
      el.querySelector('.toc__node--active')?.scrollIntoView({ block: 'center' });
      return;
    }
    // The target moves as rows around it mount and swap their estimated
    // height for a real one, so re-aim over the next few frames instead of
    // landing once on the estimate.
    let passes = 0;
    const aim = () => {
      const want = Math.max(0, offsetOfRef.current(activeIndex) - el.clientHeight / 2);
      if (Math.abs(el.scrollTop - want) > 4) el.scrollTop = want;
      if (++passes < 4) requestAnimationFrame(aim);
    };
    aim();
  }, [activeIndex, windowed]);

  const visible = windowed ? rows.slice(start, end) : rows;

  return (
    <div className="toc__scroll" ref={scrollRef}>
      <ul
        className="toc"
        ref={listRef}
        style={windowed ? { paddingTop: padTop, paddingBottom: padBottom } : undefined}
      >
        {visible.map((row, i) => {
        const index = windowed ? start + i : i;
        const className = [
          'toc__node',
          `toc__node--d${Math.min(row.depth, MAX_TOC_DEPTH)}`,
          row.container ? 'toc__node--container' : 'toc__node--leaf',
          row.firstChild ? 'toc__node--first' : '',
          row.chapter.idx === currentChapter ? 'toc__node--active' : '',
        ]
          .filter(Boolean)
          .join(' ');
          return (
            <li key={row.chapter.idx} className={className} ref={measure(index)}>
              <button onClick={() => onJump(row.chapter.idx)}>{row.chapter.title}</button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
