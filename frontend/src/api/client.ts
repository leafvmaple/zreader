// Tiny fetch wrapper around the zreader REST API. All calls go through
// /api/v1; in `pnpm dev` Vite proxies that prefix to the Go backend, and in
// production the backend serves both the SPA and the API on the same origin.

import type {
  Account,
  AuthStatus,
  Book,
  Bookmark,
  Chapter,
  ContentSlice,
  DuplicateGroup,
  Folder,
  LibraryJob,
  Progress,
  ReadingStatus,
  ScanResult,
  SearchMatch,
  Tag,
  ExportPreview,
  ExportRules,
  ReadingFont,
  Role,
  UploadResult,
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
  const isFormData = typeof FormData !== 'undefined' && init.body instanceof FormData;
  if (init.body && !isFormData && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  const res = await fetch(path, { ...init, headers });
  // 204 No Content has no body.
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  const data = text ? (JSON.parse(text) as unknown) : undefined;
  if (!res.ok) {
    const err = data as { error?: string; message?: string } | undefined;
    // A 401 means the session ended underneath us — expired, revoked, or
    // the password rotated elsewhere. Announce it once, centrally, so the
    // app can return to the login screen instead of every caller rendering
    // its own "load failed".
    if (res.status === 401 && !path.startsWith('/api/v1/auth/')) {
      window.dispatchEvent(new Event('zreader:unauthenticated'));
    }
    throw new ApiError(
      res.status,
      err?.error ?? `http_${res.status}`,
      err?.message ?? res.statusText,
    );
  }
  return data as T;
}

// --- Auth -------------------------------------------------------------------

/** The one call that works before signing in; drives which screen renders. */
export async function authStatus(): Promise<AuthStatus> {
  return request<AuthStatus>('/api/v1/auth/status');
}

export async function setupFirstAccount(username: string, password: string): Promise<Account> {
  const out = await request<{ user: Account }>('/api/v1/auth/setup', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  });
  return out.user;
}

export async function login(username: string, password: string): Promise<Account> {
  const out = await request<{ user: Account }>('/api/v1/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  });
  return out.user;
}

export async function logout(): Promise<void> {
  await request<void>('/api/v1/auth/logout', { method: 'POST' });
}

export async function changeOwnPassword(current: string, next: string): Promise<void> {
  await request<{ user: Account }>('/api/v1/auth/password', {
    method: 'POST',
    body: JSON.stringify({ current_password: current, new_password: next }),
  });
}

// --- Accounts (admin) -------------------------------------------------------

export async function listAccounts(): Promise<Account[]> {
  const out = await request<{ users: Account[] }>('/api/v1/users');
  return out.users ?? [];
}

export async function createAccount(username: string, password: string, role: Role): Promise<Account> {
  const out = await request<{ user: Account }>('/api/v1/users', {
    method: 'POST',
    body: JSON.stringify({ username, password, role }),
  });
  return out.user;
}

export async function updateAccount(
  id: string,
  patch: { password?: string; role?: Role },
): Promise<Account> {
  const out = await request<{ user: Account }>(`/api/v1/users/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  });
  return out.user;
}

export async function deleteAccount(id: string): Promise<void> {
  await request<void>(`/api/v1/users/${id}`, { method: 'DELETE' });
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

export async function listJobs(limit = 50): Promise<LibraryJob[]> {
  const out = await request<{ jobs: LibraryJob[] }>(`/api/v1/library/jobs?limit=${limit}`);
  return out.jobs ?? [];
}

export async function retryJob(id: number): Promise<LibraryJob> {
  const out = await request<{ job: LibraryJob }>(`/api/v1/library/jobs/${id}/retry`, {
    method: 'POST',
  });
  return out.job;
}

export async function listTags(): Promise<Tag[]> {
  const out = await request<{ tags: Tag[] }>('/api/v1/library/tags');
  return out.tags ?? [];
}

export async function uploadBooks(files: File[], folderId?: number): Promise<UploadResult> {
  const body = new FormData();
  if (folderId) {
    body.set('folder_id', String(folderId));
  }
  for (const file of files) {
    body.append('files', file);
  }
  return request<UploadResult>('/api/v1/library/upload', {
    method: 'POST',
    body,
  });
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

export type BookPatch = Partial<Pick<
  Book,
  'title' | 'author' | 'description' | 'category' | 'favorite' | 'reading_status' | 'cover_color' | 'cover_label'
>> & { tags?: string[] };

export async function patchBook(id: number, patch: BookPatch): Promise<Book> {
  const out = await request<{ book: Book }>(`/api/v1/books/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  });
  return out.book;
}

export async function setBookTags(id: number, tags: string[]): Promise<Tag[]> {
  const out = await request<{ tags: Tag[] }>(`/api/v1/books/${id}/tags`, {
    method: 'PUT',
    body: JSON.stringify({ tags }),
  });
  return out.tags ?? [];
}

export async function duplicateBooks(): Promise<DuplicateGroup[]> {
  const out = await request<{ groups: DuplicateGroup[] }>('/api/v1/books/duplicates');
  return out.groups ?? [];
}

export type BatchBooksBody =
  | { action: 'tag' | 'untag'; book_ids: number[]; tags: string[] }
  | { action: 'status'; book_ids: number[]; reading_status: ReadingStatus }
  | { action: 'favorite'; book_ids: number[]; favorite: boolean }
  | { action: 'reparse' | 'delete'; book_ids: number[]; delete_source?: boolean };

export async function batchBooks(body: BatchBooksBody): Promise<LibraryJob> {
  const out = await request<{ job: LibraryJob }>('/api/v1/books/batch', {
    method: 'POST',
    body: JSON.stringify(body),
  });
  return out.job;
}

export async function getContent(
  id: number,
  from: number,
  len: number,
): Promise<ContentSlice> {
  return request<ContentSlice>(`/api/v1/books/${id}/content?from=${from}&len=${len}`);
}

export function bookSourceURL(id: number): string {
  return `/api/v1/books/${id}/source`;
}

/**
 * Cover image URL. Only meaningful when `book.has_cover` is true —
 * the endpoint 404s for books with no embedded art, and the shelf is
 * expected to draw a generated cover for those instead of asking.
 */
export function coverURL(id: number): string {
  return `/api/v1/books/${id}/cover`;
}

export async function searchBook(
  id: number,
  query: string,
  limit = 30,
): Promise<SearchMatch[]> {
  const params = new URLSearchParams({ q: query, limit: String(limit) });
  const out = await request<{ matches: SearchMatch[] }>(`/api/v1/books/${id}/search?${params}`);
  return out.matches ?? [];
}

export async function reparseBook(id: number): Promise<ScanResult> {
  const out = await request<{ scan: ScanResult }>(`/api/v1/books/${id}/reparse`, {
    method: 'POST',
  });
  return out.scan;
}

export async function deleteBook(id: number, deleteSource = true): Promise<void> {
  const source = deleteSource ? 'true' : 'false';
  await request<void>(`/api/v1/books/${id}?source=${source}`, { method: 'DELETE' });
}

export async function listBookmarks(bookId: number): Promise<Bookmark[]> {
  const out = await request<{ bookmarks: Bookmark[] }>(`/api/v1/books/${bookId}/bookmarks`);
  return out.bookmarks ?? [];
}

export async function addBookmark(
  bookId: number,
  body: Pick<Bookmark, 'char_offset'> & Partial<Pick<Bookmark, 'chapter_idx' | 'note'>>,
): Promise<Bookmark> {
  return request<Bookmark>(`/api/v1/books/${bookId}/bookmarks`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export async function deleteBookmark(bookId: number, bookmarkId: number): Promise<void> {
  await request<void>(`/api/v1/books/${bookId}/bookmarks/${bookmarkId}`, { method: 'DELETE' });
}

// --- Cleaned export ---------------------------------------------------------

function exportParams(rules: ExportRules, chunkChars: number): string {
  return new URLSearchParams({
    chunk: String(chunkChars),
    promo: rules.promo ? '1' : '0',
    edges: rules.edges ? '1' : '0',
    normalise: rules.normalise ? '1' : '0',
    notes: rules.notes ? '1' : '0',
  }).toString();
}

/** Statistics plus the first few chunks, so the dialog can show what a
 *  given rule combination would actually strip before committing. */
export async function previewExport(
  id: number,
  rules: ExportRules,
  chunkChars: number,
): Promise<ExportPreview> {
  return request<ExportPreview>(`/api/v1/books/${id}/export?preview=1&${exportParams(rules, chunkChars)}`);
}

/** The download URL. Same origin with Content-Disposition, so a plain
 *  anchor is enough — no blob juggling. */
export function exportURL(id: number, rules: ExportRules, chunkChars: number): string {
  return `/api/v1/books/${id}/export?${exportParams(rules, chunkChars)}`;
}

// --- Reading fonts ----------------------------------------------------------

export async function listFonts(): Promise<ReadingFont[]> {
  const out = await request<{ fonts: ReadingFont[] }>('/api/v1/fonts');
  return out.fonts ?? [];
}

export function fontURL(file: string): string {
  return `/api/v1/fonts/${encodeURIComponent(file)}`;
}

// --- Progress ---------------------------------------------------------------

export async function getProgress(bookId: number): Promise<Progress> {
  return request<Progress>(`/api/v1/progress/${bookId}`);
}

/**
 * Every saved position for the current user, keyed by book id. One request
 * for the whole shelf — the alternative is a getProgress per book, which
 * is a request per book in the library on every refresh.
 *
 * Books with no saved position are absent from the map; callers treat a
 * missing entry as "not started".
 */
export async function listProgress(): Promise<Record<number, Progress>> {
  const out = await request<{ progress: Progress[] }>('/api/v1/progress');
  const map: Record<number, Progress> = {};
  for (const p of out.progress ?? []) map[p.book_id] = p;
  return map;
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
