// Tiny fetch wrapper around the zreader REST API. All calls go through
// /api/v1; in `pnpm dev` Vite proxies that prefix to the Go backend, and in
// production the backend serves both the SPA and the API on the same origin.

import type {
  Book,
  Chapter,
  ContentSlice,
  Folder,
  Progress,
  ScanResult,
} from '../types/api';

export class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  const res = await fetch(path, { ...init, headers });
  // 204 No Content has no body.
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  const data = text ? (JSON.parse(text) as unknown) : undefined;
  if (!res.ok) {
    const err = data as { error?: string; message?: string } | undefined;
    throw new ApiError(
      res.status,
      err?.error ?? `http_${res.status}`,
      err?.message ?? res.statusText,
    );
  }
  return data as T;
}

// --- Library folders --------------------------------------------------------

export async function listFolders(): Promise<Folder[]> {
  const out = await request<{ folders: Folder[] }>('/api/v1/library/folders');
  return out.folders ?? [];
}

export async function addFolder(path: string): Promise<Folder> {
  return request<Folder>('/api/v1/library/folders', {
    method: 'POST',
    body: JSON.stringify({ path }),
  });
}

export async function deleteFolder(id: number): Promise<void> {
  await request<void>(`/api/v1/library/folders/${id}`, { method: 'DELETE' });
}

export async function scan(folderId?: number): Promise<ScanResult[]> {
  const out = await request<{ scans: ScanResult[] }>('/api/v1/library/scan', {
    method: 'POST',
    body: JSON.stringify(folderId ? { folder_id: folderId } : {}),
  });
  return out.scans ?? [];
}

// --- Books ------------------------------------------------------------------

export async function listBooks(folderId?: number): Promise<Book[]> {
  const qs = folderId ? `?folder_id=${folderId}` : '';
  const out = await request<{ books: Book[] }>(`/api/v1/books${qs}`);
  return out.books ?? [];
}

export async function getBook(id: number): Promise<{ book: Book; chapters: Chapter[] }> {
  return request<{ book: Book; chapters: Chapter[] }>(`/api/v1/books/${id}`);
}

export async function getContent(
  id: number,
  from: number,
  len: number,
): Promise<ContentSlice> {
  return request<ContentSlice>(`/api/v1/books/${id}/content?from=${from}&len=${len}`);
}

// --- Progress ---------------------------------------------------------------

export async function getProgress(bookId: number): Promise<Progress> {
  return request<Progress>(`/api/v1/progress/${bookId}`);
}

export type PutProgressResult =
  | { ok: true; progress: Progress }
  | { ok: false; conflict: Progress };

/**
 * Save reading progress. If the server has a newer write (HTTP 409) we don't
 * throw — we return a typed conflict so the caller can decide whether to
 * adopt the server's position or force-overwrite.
 */
export async function putProgress(
  bookId: number,
  body: Omit<Progress, 'book_id'>,
): Promise<PutProgressResult> {
  try {
    const progress = await request<Progress>(`/api/v1/progress/${bookId}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    });
    return { ok: true, progress };
  } catch (err) {
    if (err instanceof ApiError && err.status === 409) {
      const conflict = await getProgress(bookId);
      return { ok: false, conflict };
    }
    throw err;
  }
}
