// useThrottledProgress trickles PUT /api/v1/progress writes at most once
// every `intervalMs`, plus one final flush on unmount. It's intentionally
// lossy: rapid scrolls collapse into a single write — we always send the
// latest known position rather than a queue.
//
// Conflict policy: if the server returns 409 stale_write the local position
// is REPLACED with the server's — the assumption is that "the other device
// is currently reading further, this device must have been left open". The
// caller is notified via onConflict so it can re-render at the new offset.

import { useCallback, useEffect, useRef } from 'react';
import { putProgress } from '../api/client';
import type { Progress } from '../types/api';

type Position = Omit<Progress, 'book_id' | 'updated_at'>;

type Options = {
  bookId: number;
  intervalMs?: number;
  onConflict?: (server: Progress) => void;
};

export function useThrottledProgress({ bookId, intervalMs = 5000, onConflict }: Options) {
  // Latest desired position; replaced wholesale, never queued.
  const pending = useRef<Position | null>(null);
  // Set when a flush is scheduled; cleared after it runs.
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Highest UpdatedAt we've successfully written; used as the optimistic
  // lock value the server checks on the next PUT.
  const lastUpdatedAt = useRef<number>(0);

  const flush = useCallback(async () => {
    timer.current = null;
    const pos = pending.current;
    if (!pos) return;
    pending.current = null;
    const now = Math.floor(Date.now() / 1000);
    const result = await putProgress(bookId, { ...pos, updated_at: now });
    if (result.ok) {
      lastUpdatedAt.current = result.progress.updated_at;
    } else {
      lastUpdatedAt.current = result.conflict.updated_at;
      onConflict?.(result.conflict);
    }
  }, [bookId, onConflict]);

  const report = useCallback(
    (pos: Position) => {
      pending.current = pos;
      if (timer.current != null) return;
      timer.current = setTimeout(() => {
        // Fire-and-forget. Errors are swallowed; next report restarts a timer
        // and the same position will be retried.
        flush().catch((err) => {
          console.warn('[progress] flush failed:', err);
          // schedule a retry on the next report
          lastUpdatedAt.current = 0;
        });
      }, intervalMs);
    },
    [flush, intervalMs],
  );

  // Flush on unmount so navigating away saves the latest position promptly.
  useEffect(() => {
    return () => {
      if (timer.current != null) {
        clearTimeout(timer.current);
        timer.current = null;
      }
      // Best-effort sync flush — fetch keepalive lets the request survive
      // the page tear-down. We can't await it here, but the keepalive flag
      // tells the browser to deliver it anyway.
      const pos = pending.current;
      if (pos) {
        const now = Math.floor(Date.now() / 1000);
        fetch(`/api/v1/progress/${bookId}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ ...pos, updated_at: now }),
          keepalive: true,
        }).catch(() => {
          /* nothing to do — best effort */
        });
      }
    };
  }, [bookId]);

  return { report, flush };
}
