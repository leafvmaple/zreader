import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { RefObject } from 'react';

// useWindowedList renders only the slice of a long list that is near the
// viewport, with spacers standing in for the rest.
//
// The shelf scrolls the document rather than a container, so this measures
// against window scroll and the container's position in the page instead
// of a scroll element's scrollTop.
//
// Heights are measured, not assumed. Shelf rows vary — a book with tags is
// taller than one without — and a virtualiser that assumes a uniform
// height drifts as you scroll, eventually showing the wrong items or a gap.
// Every rendered row reports its real height; rows never yet rendered use
// `estimate`. Since you can only scroll past what has been rendered,
// everything above the viewport is exact and only the scrollbar length is
// approximate, which is the one part nobody notices.
//
// Rows are measured on mount and then watched by a single ResizeObserver
// for the rest of their life, so a row that changes height while mounted —
// a webfont finishing its swap, a title rewrapping — updates its stored
// height without waiting to leave and re-enter the window.
//
// This used to happen by accident and at a price. `measure(index)` built a
// new closure per render, so React tore down and re-ran every visible row's
// ref callback on every render, and each of those forced a layout read.
// That masked the staleness (any render re-measured everything) while
// making scrolling do O(visible rows) synchronous layout work per frame.
// The ref callbacks are now stable per index, and the observer — one for
// the whole list, not one per row — reports the size changes instead.

type Options = {
  count: number;
  /** Height guess for rows that have not been rendered yet. */
  estimate: number;
  containerRef: RefObject<HTMLElement | null>;
  /** Rows to keep mounted beyond each edge, so scrolling never shows a gap. */
  overscan?: number;
  /**
   * Off for short lists: below a few screens the windowing costs more than
   * it saves, and a fully rendered list keeps the browser's own find-in-page
   * working across every book.
   */
  enabled: boolean;
};

type Result = {
  start: number;
  end: number;
  padTop: number;
  padBottom: number;
  /** Attach to each rendered row so its real height replaces the estimate. */
  measure: (index: number) => (el: HTMLElement | null) => void;
};

export function useWindowedList({
  count,
  estimate,
  containerRef,
  overscan = 6,
  enabled,
}: Options): Result {
  const heights = useRef<number[]>([]);
  // The mounted row elements, and the reverse lookup the observer needs to
  // turn one of its entries back into a list index.
  const rows = useRef(new Map<number, HTMLElement>());
  const rowIndex = useRef(new WeakMap<Element, number>());
  const observer = useRef<ResizeObserver | null>(null);
  const refs = useRef(new Map<number, (el: HTMLElement | null) => void>());
  const [, forceRecompute] = useState(0);
  const [viewport, setViewport] = useState({ scrollY: 0, height: 0, containerTop: 0 });

  // Reset when the list identity changes (a filter, a different sort).
  // Stale heights would place the wrong rows.
  if (heights.current.length !== count) {
    const next = new Array<number>(count);
    for (let i = 0; i < count; i++) next[i] = heights.current[i] ?? estimate;
    heights.current = next;
  }

  const read = useCallback(() => {
    const el = containerRef.current;
    setViewport({
      scrollY: window.scrollY,
      height: window.innerHeight,
      // offsetTop is relative to the offset parent; for the shelf's static
      // layout that is the document, which is what we want.
      containerTop: el ? el.offsetTop : 0,
    });
  }, [containerRef]);

  useEffect(() => {
    if (!enabled) return;
    read();
    window.addEventListener('scroll', read, { passive: true });
    window.addEventListener('resize', read);
    return () => {
      window.removeEventListener('scroll', read);
      window.removeEventListener('resize', read);
    };
  }, [enabled, read]);

  // Running offsets, rebuilt whenever a measurement lands. O(count) per
  // rebuild rather than per scroll — scrolling only binary-searches this.
  const offsets = useMemo(() => {
    const out = new Array<number>(count + 1);
    out[0] = 0;
    for (let i = 0; i < count; i++) out[i + 1] = out[i] + (heights.current[i] || estimate);
    return out;
    // forceRecompute is the measurement signal; heights.current is a ref so
    // it can't be a dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [count, estimate, viewport, forceRecompute]);

  const record = useCallback((index: number, h: number) => {
    // Ignore sub-pixel noise. The observer re-fires on every layout that
    // touches a row, so a threshold of zero would turn rounding into an
    // endless measure/render loop.
    if (h > 0 && Math.abs((heights.current[index] ?? 0) - h) > 1) {
      heights.current[index] = h;
      forceRecompute((n) => n + 1);
    }
  }, []);

  useEffect(() => {
    if (!enabled) return;
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const index = rowIndex.current.get(entry.target);
        // Deliberately not entry.contentRect: that is the content box,
        // while the mount-time path below reads the border box. Two
        // metrics for one row would read as a change on every observation.
        if (index !== undefined) record(index, entry.target.getBoundingClientRect().height);
      }
    });
    observer.current = ro;
    // Rows mounted before this effect ran (or while windowing was off).
    for (const [index, el] of rows.current) {
      rowIndex.current.set(el, index);
      ro.observe(el);
    }
    return () => {
      ro.disconnect();
      observer.current = null;
    };
  }, [enabled, record]);

  // One stable callback per index. React only re-runs a ref callback whose
  // identity changed, so caching these is what stops every render from
  // detaching and re-measuring every visible row.
  const measure = useCallback(
    (index: number) => {
      let fn = refs.current.get(index);
      if (fn) return fn;
      fn = (el: HTMLElement | null) => {
        const prev = rows.current.get(index);
        if (prev && prev !== el) {
          observer.current?.unobserve(prev);
          rows.current.delete(index);
        }
        if (!el) return;
        rows.current.set(index, el);
        rowIndex.current.set(el, index);
        observer.current?.observe(el);
        // Measure now as well: observation is delivered asynchronously, and
        // waiting a frame for the first height would show one frame of
        // estimate-sized padding on every scroll.
        record(index, el.getBoundingClientRect().height);
      };
      refs.current.set(index, fn);
      return fn;
    },
    [record],
  );

  if (!enabled || count === 0) {
    return { start: 0, end: count, padTop: 0, padBottom: 0, measure };
  }

  // Where the viewport sits in the list's own coordinate space.
  const top = viewport.scrollY - viewport.containerTop;
  const bottom = top + viewport.height;

  const findIndex = (offset: number) => {
    let lo = 0;
    let hi = count;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (offsets[mid + 1] <= offset) lo = mid + 1;
      else hi = mid;
    }
    return Math.min(lo, count - 1);
  };

  const start = Math.max(0, findIndex(Math.max(0, top)) - overscan);
  const end = Math.min(count, findIndex(Math.max(0, bottom)) + 1 + overscan);

  return {
    start,
    end,
    padTop: offsets[start],
    padBottom: offsets[count] - offsets[end],
    measure,
  };
}

/**
 * useColumnCount reports how many grid columns are currently laid out, by
 * reading the resolved template rather than recomputing the CSS breakpoints
 * in JS — the grid stays defined in one place.
 */
export function useColumnCount(ref: RefObject<HTMLElement | null>, enabled: boolean): number {
  const [columns, setColumns] = useState(1);

  useEffect(() => {
    const el = ref.current;
    if (!enabled || !el) return;
    const read = () => {
      const template = getComputedStyle(el).gridTemplateColumns;
      const n = template.split(' ').filter(Boolean).length;
      setColumns(n > 0 ? n : 1);
    };
    read();
    const ro = new ResizeObserver(read);
    ro.observe(el);
    return () => ro.disconnect();
  }, [ref, enabled]);

  return columns;
}
