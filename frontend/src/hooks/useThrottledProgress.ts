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
  // The row version last read back from the server. This is what the
  // optimistic lock is checked against — it used to be unused, and the
  // client sent its own wall clock instead, which made the winner of a
  // two-device race whichever clock ran faster.
  const lastUpdatedAt = useRef<number>(0);
  // Assigned below, once schedule exists; flush is declared above it and
  // has to reschedule itself after a failed write.
  const scheduleRef = useRef<() => void>(() => {});

  const flush = useCallback(async () => {
    timer.current = null;
    const pos = pending.current;
    if (!pos) return;

    let result;
    try {
      result = await putProgress(bookId, { ...pos, base_updated_at: lastUpdatedAt.current });
    } catch (err) {
      // The position stays pending. It used to be cleared before the
      // request went out, so a scroll followed by an offline moment lost
      // it outright — and nothing retried unless you happened to scroll
      // again, which a reader who has stopped for the night will not.
      scheduleRef.current();
      throw err;
    }

    // Only drop what was actually written, and only if nothing newer
    // landed while the request was in flight.
    if (pending.current === pos) pending.current = null;

    if (result.ok) {
      lastUpdatedAt.current = result.progress.updated_at;
    } else {
      lastUpdatedAt.current = result.conflict.updated_at;
      onConflict?.(result.conflict);
    }
  }, [bookId, onConflict]);

  const schedule = useCallback(() => {
    if (timer.current != null) return;
    timer.current = setTimeout(() => {
      // Fire-and-forget: flush keeps the position and reschedules itself
      // if the write failed.
      flush().catch((err) => {
        console.warn('[progress] flush failed:', err);
      });
    }, intervalMs);
  }, [flush, intervalMs]);
  scheduleRef.current = schedule;

  const report = useCallback(
    (pos: Position) => {
      pending.current = pos;
      schedule();
    },
    [schedule],
  );

  // flushBeacon is the terminal write: a keepalive fetch that the browser
  // is obliged to deliver even as the page goes away. We can't await it,
  // so it does not touch lastUpdatedAt — a conflict here would have
  // nowhere to be reported anyway. It still carries the base version, so
  // a tab that has fallen behind another device is refused rather than
  // clobbering it on the way out.
  const flushBeacon = useCallback(() => {
    const pos = pending.current;
    if (!pos) return;
    pending.current = null;
    if (timer.current != null) {
      clearTimeout(timer.current);
      timer.current = null;
    }
    fetch(`/api/v1/progress/${bookId}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ...pos, base_updated_at: lastUpdatedAt.current }),
      keepalive: true,
    }).catch(() => {
      /* nothing to do — best effort */
    });
  }, [bookId]);

  // Save whenever the page stops being watched, not only on unmount.
  //
  // React's unmount cleanup covers navigating inside the app, but it does
  // NOT run when a tab is closed, when a phone is locked, or when the user
  // switches apps — which is how most reading sessions actually end. With
  // only the 5s throttle behind it, that lost up to five seconds of
  // position every time, on the one feature the app exists to sync.
  //
  // visibilitychange → hidden is the event that reliably fires in all of
  // those cases; pagehide covers the bfcache path Safari takes on
  // navigation away.
  useEffect(() => {
    const onHide = () => {
      if (document.visibilityState === 'hidden') flushBeacon();
    };
    document.addEventListener('visibilitychange', onHide);
    window.addEventListener('pagehide', flushBeacon);
    return () => {
      document.removeEventListener('visibilitychange', onHide);
      window.removeEventListener('pagehide', flushBeacon);
    };
  }, [flushBeacon]);

  // Flush on unmount so navigating away inside the app saves promptly.
  useEffect(() => {
    return () => {
      flushBeacon();
    };
  }, [flushBeacon]);

  return { report, flush };
}
