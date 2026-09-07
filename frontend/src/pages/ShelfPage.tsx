import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../api/client';
import { BookCover } from '../components/BookCover';
import { Dialog } from '../components/Dialog';
import { ExportDialog } from '../components/ExportDialog';
import type { Book, DuplicateGroup, Folder, LibraryJob, Progress, ReadingStatus, Tag } from '../types/api';
import './ShelfPage.css';

type SortKey = 'recent' | 'title' | 'added';
type BookAction = 'reparse' | 'delete';
type StatusFilter = 'all' | 'favorite' | ReadingStatus;
type ThemeMode = 'light' | 'dark';
type ViewMode = 'list' | 'grid';
// What the delete dialog is currently confirming: one book (named in the
// prompt) or the current multi-select.
type DeleteTarget = { kind: 'one'; book: Book } | { kind: 'many'; ids: number[] } | null;

const THEME_KEY = 'zreader.theme';
const VIEW_KEY = 'zreader.view';

function loadView(): ViewMode {
  try {
    const v = localStorage.getItem(VIEW_KEY);
    if (v === 'grid' || v === 'list') return v;
  } catch {
    /* ignore */
  }
  return 'list';
}

function currentTheme(): ThemeMode {
  if (typeof document === 'undefined') return 'light';
  return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'light';
}

function applyTheme(mode: ThemeMode) {
  document.documentElement.setAttribute('data-theme', mode);
  try {
    localStorage.setItem(THEME_KEY, mode);
  } catch {
    /* ignore */
  }
}

function ThemeIcon({ mode }: { mode: ThemeMode }) {
  // Show the glyph for the mode you'd switch *to*.
  if (mode === 'light') {
    // moon
    return (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <path
          d="M21 12.8A8.5 8.5 0 1 1 11.2 3a6.6 6.6 0 0 0 9.8 9.8Z"
          stroke="currentColor"
          strokeWidth="1.7"
          strokeLinejoin="round"
        />
      </svg>
    );
  }
  // sun
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle cx="12" cy="12" r="4" stroke="currentColor" strokeWidth="1.7" />
      <path
        d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
      />
    </svg>
  );
}

function ViewIcon({ mode }: { mode: ViewMode }) {
  // Show the glyph for the view you'd switch *to*.
  if (mode === 'list') {
    // grid
    return (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <rect x="3" y="3" width="7" height="7" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
        <rect x="14" y="3" width="7" height="7" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
        <rect x="3" y="14" width="7" height="7" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
        <rect x="14" y="14" width="7" height="7" rx="1.5" stroke="currentColor" strokeWidth="1.7" />
      </svg>
    );
  }
  // list
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path
        d="M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
      />
    </svg>
  );
}

function SearchIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle cx="11" cy="11" r="7" stroke="currentColor" strokeWidth="1.7" />
      <path d="M20.5 20.5L16 16" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
    </svg>
  );
}

function PlusIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path d="M12 5v14M5 12h14" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
    </svg>
  );
}

function MoreIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle cx="5" cy="12" r="1.6" fill="currentColor" />
      <circle cx="12" cy="12" r="1.6" fill="currentColor" />
      <circle cx="19" cy="12" r="1.6" fill="currentColor" />
    </svg>
  );
}

function StarIcon({ filled }: { filled: boolean }) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true">
      <path
        d="M12 3.6l2.5 5.2 5.7.8-4.1 4 1 5.7-5.1-2.7-5.1 2.7 1-5.7-4.1-4 5.7-.8z"
        fill={filled ? 'currentColor' : 'none'}
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function SortIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path
        d="M7 4v16m0 0l-3-3.5M7 20l3-3.5M17 20V4m0 0l-3 3.5M17 4l3 3.5"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

// Menu is the popover behind the header's "⋯" button. It closes on
// outside click, on Escape, and after any item fires — the three ways a
// user expects to dismiss a menu — so the callers stay one-liners.
function Menu({
  label,
  badge,
  className,
  children,
}: {
  label: string;
  badge?: number;
  className?: string;
  children: (close: () => void) => React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement | null>(null);
  const close = useCallback(() => setOpen(false), []);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: globalThis.MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div className="shelf__menu" ref={ref}>
      <button
        type="button"
        className={`shelf__btn shelf__btn--icon shelf__btn--ghost${className ? ` ${className}` : ''}${open ? ' is-open' : ''}`}
        onClick={() => setOpen((v) => !v)}
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        title={label}
      >
        <MoreIcon />
        {badge !== undefined && badge > 0 && <span className="shelf__badge">{badge}</span>}
      </button>
      {open && (
        <div className="shelf__menu-pop" role="menu">
          {children(close)}
        </div>
      )}
    </div>
  );
}

// STATUS_FILTERS is the chip row's order — "all" first, then the
// reading-status lifecycle, then favourites, which is a different axis
// and so sits last.
const STATUS_FILTERS: { key: StatusFilter; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'reading', label: '在读' },
  { key: 'unread', label: '未读' },
  { key: 'finished', label: '已读完' },
  { key: 'paused', label: '搁置' },
  { key: 'favorite', label: '收藏' },
];

const STATUS_LABELS: Record<ReadingStatus, string> = {
  unread: '未读',
  reading: '在读',
  finished: '已读完',
  paused: '搁置',
};

function baseName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).pop() ?? path;
}

function splitTags(raw: string): string[] {
  return raw
    .split(/[,，\s]+/)
    .map((t) => t.trim())
    .filter(Boolean);
}

function tagsText(tags?: string[]): string {
  return (tags ?? []).join('，');
}

export function ShelfPage() {
  const [books, setBooks] = useState<Book[]>([]);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [jobs, setJobs] = useState<LibraryJob[]>([]);
  const [duplicates, setDuplicates] = useState<DuplicateGroup[]>([]);
  const [progress, setProgress] = useState<Record<number, Progress>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [scanBusy, setScanBusy] = useState(false);
  const [scanMsg, setScanMsg] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [sort, setSort] = useState<SortKey>('recent');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [tagFilter, setTagFilter] = useState('');
  const [uploadOpen, setUploadOpen] = useState(false);
  const [uploadBusy, setUploadBusy] = useState(false);
  const [uploadFiles, setUploadFiles] = useState<File[]>([]);
  const [uploadFolderId, setUploadFolderId] = useState<number | undefined>(undefined);
  const [uploadMsg, setUploadMsg] = useState<string | null>(null);
  const [bookBusy, setBookBusy] = useState<Record<number, BookAction>>({});
  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget>(null);
  const [deleteSource, setDeleteSource] = useState(false);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteMsg, setDeleteMsg] = useState<string | null>(null);
  const [exportBook, setExportBook] = useState<Book | null>(null);

  // Every entry point resets the source checkbox: an opt-in that
  // remembered its last value would be an opt-in in name only.
  const askDelete = useCallback((target: DeleteTarget) => {
    setDeleteSource(false);
    setDeleteMsg(null);
    setDeleteTarget(target);
  }, []);
  const [selected, setSelected] = useState<number[]>([]);
  const [batchTag, setBatchTag] = useState('');
  const [showJobs, setShowJobs] = useState(false);
  const [showDuplicates, setShowDuplicates] = useState(false);
  const [editBook, setEditBook] = useState<Book | null>(null);
  const [editForm, setEditForm] = useState({
    title: '',
    author: '',
    description: '',
    category: '',
    reading_status: 'unread' as ReadingStatus,
    favorite: false,
    tags: '',
  });
  const [editBusy, setEditBusy] = useState(false);
  const [editMsg, setEditMsg] = useState<string | null>(null);
  const [theme, setTheme] = useState<ThemeMode>(currentTheme);
  const [view, setView] = useState<ViewMode>(loadView);
  // Drives the sticky header's divider — see .shelf__header.is-stuck.
  const [stuck, setStuck] = useState(false);

  useEffect(() => {
    const onScroll = () => setStuck(window.scrollY > 4);
    onScroll();
    window.addEventListener('scroll', onScroll, { passive: true });
    return () => window.removeEventListener('scroll', onScroll);
  }, []);

  const toggleTheme = useCallback(() => {
    setTheme((prev) => {
      const next: ThemeMode = prev === 'dark' ? 'light' : 'dark';
      applyTheme(next);
      return next;
    });
  }, []);

  const toggleView = useCallback(() => {
    setView((prev) => {
      const next: ViewMode = prev === 'grid' ? 'list' : 'grid';
      try {
        localStorage.setItem(VIEW_KEY, next);
      } catch {
        /* ignore */
      }
      return next;
    });
  }, []);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [list, folderList, tagList, jobList, duplicateList, progressMap] = await Promise.all([
        api.listBooks(),
        api.listFolders(),
        api.listTags(),
        api.listJobs(20),
        api.duplicateBooks(),
        api.listProgress(),
      ]);
      setBooks(list);
      setFolders(folderList);
      setTags(tagList);
      setJobs(jobList);
      setDuplicates(duplicateList);
      setProgress(progressMap);
      setSelected((current) => current.filter((id) => list.some((b) => b.id === id)));
      setUploadFolderId((current) =>
        current !== undefined && folderList.some((f) => f.id === current)
          ? current
          : folderList[0]?.id,
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (
      folders.length > 0 &&
      (uploadFolderId === undefined || !folders.some((f) => f.id === uploadFolderId))
    ) {
      setUploadFolderId(folders[0].id);
    }
  }, [folders, uploadFolderId]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const onScan = useCallback(async () => {
    setScanBusy(true);
    setScanMsg(null);
    try {
      const results = await api.scan();
      const total = results.reduce(
        (a, r) => ({
          added: a.added + r.added,
          updated: a.updated + r.updated,
          removed: a.removed + r.removed,
        }),
        { added: 0, updated: 0, removed: 0 },
      );
      setScanMsg(`扫描完成：新增 ${total.added}，更新 ${total.updated}，移除 ${total.removed}`);
      await refresh();
    } catch (err) {
      setScanMsg(`扫描失败：${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setScanBusy(false);
    }
  }, [refresh]);

  const onUpload = useCallback(async () => {
    if (uploadFiles.length === 0) {
      setUploadMsg('请选择文件');
      return;
    }
    setUploadBusy(true);
    setUploadMsg(null);
    try {
      const result = await api.uploadBooks(uploadFiles, uploadFolderId);
      const failedList = result.scan.failed ?? [];
      const failed = failedList.length;
      if (failed > 0) {
        const failedNames = failedList.map(baseName).join('、');
        setScanMsg(`已上传 ${result.uploaded.length} 个文件，${failed} 个扫描失败：${failedNames}`);
        setUploadMsg(`以下文件未导入：${failedNames}`);
      } else {
        setScanMsg(`添加完成：新增 ${result.scan.added}，更新 ${result.scan.updated}`);
        setUploadOpen(false);
        setUploadFiles([]);
      }
      await refresh();
    } catch (err) {
      setUploadMsg(err instanceof Error ? err.message : String(err));
    } finally {
      setUploadBusy(false);
    }
  }, [refresh, uploadFiles, uploadFolderId]);

  const onReparseBook = useCallback(
    async (book: Book) => {
      setBookBusy((prev) => ({ ...prev, [book.id]: 'reparse' }));
      setScanMsg(null);
      try {
        const result = await api.reparseBook(book.id);
        setScanMsg(`重解析完成：新增 ${result.added}，更新 ${result.updated}`);
        await refresh();
      } catch (err) {
        setScanMsg(`重解析失败：${err instanceof Error ? err.message : String(err)}`);
      } finally {
        setBookBusy((prev) => {
          const next = { ...prev };
          delete next[book.id];
          return next;
        });
      }
    },
    [refresh],
  );

  // Deleting used to run api.deleteBook(id, true) behind a window.confirm
  // — i.e. the easiest button on every row erased the user's actual file,
  // while the harder-to-reach batch delete only dropped the record. The
  // defaults are now the other way round: record-only unless you tick the
  // box, and the box is in a real dialog that names what it will remove.
  const onConfirmDelete = useCallback(async () => {
    if (!deleteTarget) return;
    const ids = deleteTarget.kind === 'one' ? [deleteTarget.book.id] : deleteTarget.ids;
    setDeleteBusy(true);
    setScanMsg(null);
    try {
      if (deleteTarget.kind === 'one') {
        await api.deleteBook(ids[0], deleteSource);
      } else {
        await api.batchBooks({ action: 'delete', book_ids: ids, delete_source: deleteSource });
        setSelected([]);
      }
      setScanMsg(
        deleteSource
          ? `已删除 ${ids.length} 本书的记录和源文件`
          : `已删除 ${ids.length} 本书的记录和缓存（源文件保留）`,
      );
      setDeleteTarget(null);
      await refresh();
    } catch (err) {
      if (err instanceof api.ApiError && err.code === 'source_not_found') {
        setDeleteMsg('源文件没有找到。取消勾选「同时删除源文件」可以只删除阅读器里的记录。');
      } else {
        setDeleteMsg(err instanceof Error ? err.message : String(err));
      }
    } finally {
      setDeleteBusy(false);
    }
  }, [deleteSource, deleteTarget, refresh]);

  const openEdit = useCallback((book: Book) => {
    setEditBook(book);
    setEditMsg(null);
    setEditForm({
      title: book.title,
      author: book.author ?? '',
      description: book.description ?? '',
      category: book.category ?? '',
      reading_status: book.reading_status,
      favorite: book.favorite,
      tags: tagsText(book.tags),
    });
  }, []);

  const onSaveEdit = useCallback(async () => {
    if (!editBook) return;
    setEditBusy(true);
    setEditMsg(null);
    try {
      await api.patchBook(editBook.id, {
        title: editForm.title,
        author: editForm.author,
        description: editForm.description,
        category: editForm.category,
        reading_status: editForm.reading_status,
        favorite: editForm.favorite,
        tags: splitTags(editForm.tags),
      });
      setEditBook(null);
      await refresh();
    } catch (err) {
      setEditMsg(err instanceof Error ? err.message : String(err));
    } finally {
      setEditBusy(false);
    }
  }, [editBook, editForm, refresh]);

  // Favouriting used to run the full refresh() — six list endpoints plus,
  // before the batch progress endpoint, one request per book — to flip one
  // boolean. Patch the row in place and let the server confirm.
  const onToggleFavorite = useCallback(async (book: Book) => {
    const next = !book.favorite;
    setBooks((prev) => prev.map((b) => (b.id === book.id ? { ...b, favorite: next } : b)));
    try {
      const updated = await api.patchBook(book.id, { favorite: next });
      setBooks((prev) => prev.map((b) => (b.id === book.id ? { ...b, ...updated } : b)));
    } catch (err) {
      // Roll back to what the server still believes.
      setBooks((prev) => prev.map((b) => (b.id === book.id ? { ...b, favorite: book.favorite } : b)));
      setScanMsg(`收藏失败：${err instanceof Error ? err.message : String(err)}`);
    }
  }, []);

  const toggleSelected = useCallback((id: number) => {
    setSelected((current) =>
      current.includes(id) ? current.filter((x) => x !== id) : [...current, id],
    );
  }, []);

  const clearSelected = useCallback(() => setSelected([]), []);

  const runBatch = useCallback(
    async (label: string, fn: () => Promise<LibraryJob>) => {
      if (selected.length === 0) return;
      setScanBusy(true);
      setScanMsg(null);
      try {
        const job = await fn();
        setScanMsg(`${label}完成：更新 ${job.updated}，移除 ${job.removed}`);
        setSelected([]);
        await refresh();
      } catch (err) {
        setScanMsg(`${label}失败：${err instanceof Error ? err.message : String(err)}`);
      } finally {
        setScanBusy(false);
      }
    },
    [refresh, selected],
  );

  const onBatchTag = useCallback(async () => {
    const names = splitTags(batchTag);
    if (names.length === 0) {
      setScanMsg('请输入批量标签');
      return;
    }
    await runBatch('批量打标签', () =>
      api.batchBooks({ action: 'tag', book_ids: selected, tags: names }),
    );
    setBatchTag('');
  }, [batchTag, runBatch, selected]);

  const onBatchStatus = useCallback(
    async (status: ReadingStatus) => {
      await runBatch('批量状态', () =>
        api.batchBooks({ action: 'status', book_ids: selected, reading_status: status }),
      );
    },
    [runBatch, selected],
  );

  const onBatchFavorite = useCallback(async () => {
    await runBatch('批量收藏', () =>
      api.batchBooks({ action: 'favorite', book_ids: selected, favorite: true }),
    );
  }, [runBatch, selected]);

  const onBatchReparse = useCallback(async () => {
    await runBatch('批量重解析', () =>
      api.batchBooks({ action: 'reparse', book_ids: selected }),
    );
  }, [runBatch, selected]);

  const onRetryJob = useCallback(
    async (job: LibraryJob) => {
      setScanBusy(true);
      setScanMsg(null);
      try {
        const retried = await api.retryJob(job.id);
        setScanMsg(`任务已重试：${retried.status}`);
        await refresh();
      } catch (err) {
        setScanMsg(`重试失败：${err instanceof Error ? err.message : String(err)}`);
      } finally {
        setScanBusy(false);
      }
    },
    [refresh],
  );

  // --- Derived views -------------------------------------------------------

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    let xs = books;
    if (q) {
      xs = xs.filter(
        (b) =>
          b.title.toLowerCase().includes(q) ||
          (b.author ?? '').toLowerCase().includes(q) ||
          (b.category ?? '').toLowerCase().includes(q) ||
          (b.tags ?? []).some((tag) => tag.toLowerCase().includes(q)),
      );
    }
    if (statusFilter === 'favorite') {
      xs = xs.filter((b) => b.favorite);
    } else if (statusFilter !== 'all') {
      xs = xs.filter((b) => b.reading_status === statusFilter);
    }
    if (tagFilter) {
      xs = xs.filter((b) => (b.tags ?? []).includes(tagFilter));
    }
    const sorted = [...xs];
    switch (sort) {
      case 'title':
        sorted.sort((a, b) => a.title.localeCompare(b.title, 'zh-Hans-CN'));
        break;
      case 'added':
        sorted.sort((a, b) => b.added_at - a.added_at);
        break;
      case 'recent':
      default:
        sorted.sort((a, b) => {
          const ap = progress[a.id]?.updated_at ?? 0;
          const bp = progress[b.id]?.updated_at ?? 0;
          if (ap !== bp) return bp - ap; // recently read first
          return b.scanned_at - a.scanned_at;
        });
        break;
    }
    return sorted;
  }, [books, progress, search, sort, statusFilter, tagFilter]);

  const continueReading = useMemo(() => {
    return books
      .filter((b) => (progress[b.id]?.updated_at ?? 0) > 0)
      .sort((a, b) => (progress[b.id]?.updated_at ?? 0) - (progress[a.id]?.updated_at ?? 0))
      .slice(0, 5);
  }, [books, progress]);

  // --- Render --------------------------------------------------------------

  // Per-book management actions, shared by the list rows and the grid
  // cards. These used to be four same-weight bordered buttons rendered on
  // every row — including 删除 — which out-shouted the book itself and put
  // the destructive action one stray click away. They live in an overflow
  // menu now; the star stays direct because favouriting is the one action
  // people do often.
  const bookActionMenu = (b: Book, action: BookAction | undefined) => (
    <Menu label={`《${b.title}》的操作`} className="book-row__more">
      {(close) => (
        <>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              openEdit(b);
              close();
            }}
          >
            编辑信息
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={action !== undefined}
            onClick={() => {
              void onReparseBook(b);
              close();
            }}
          >
            {action === 'reparse' ? '解析中…' : '重新解析'}
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={b.format !== 'epub'}
            title={b.format !== 'epub' ? '这本书没有可导出的文本层' : undefined}
            onClick={() => {
              setExportBook(b);
              close();
            }}
          >
            导出给 AI…
          </button>
          <button
            type="button"
            role="menuitem"
            className="shelf__menu-item--danger"
            disabled={action !== undefined}
            onClick={() => {
              askDelete({ kind: 'one', book: b });
              close();
            }}
          >
            删除…
          </button>
        </>
      )}
    </Menu>
  );

  const favButton = (b: Book, action: BookAction | undefined, className: string) => (
    <button
      type="button"
      className={`${className}${b.favorite ? ' is-active' : ''}`}
      onClick={() => void onToggleFavorite(b)}
      disabled={action !== undefined}
      aria-label={b.favorite ? '取消收藏' : '收藏'}
      aria-pressed={b.favorite}
      title={b.favorite ? '取消收藏' : '收藏'}
    >
      <StarIcon filled={b.favorite} />
    </button>
  );

  return (
    <main className="shelf">
      <header className={`shelf__header${stuck ? ' is-stuck' : ''}`}>
        <div className="shelf__bar">
          <div className="shelf__title">
            <h1>zreader</h1>
            <span className="shelf__count">{books.length} 本</span>
          </div>

          <div className="shelf__search-wrap">
            <SearchIcon />
            <input
              type="search"
              placeholder="搜索书名 / 作者 / 标签"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="shelf__search"
              aria-label="搜索"
            />
          </div>

          <div className="shelf__actions">
            <button
              type="button"
              className="shelf__btn shelf__btn--icon shelf__btn--ghost"
              onClick={() => {
                setUploadOpen(true);
                setUploadMsg(null);
              }}
              disabled={uploadBusy}
              aria-label="添加书籍"
              title="添加书籍"
            >
              <PlusIcon />
            </button>
            <button
              type="button"
              className="shelf__btn shelf__btn--icon shelf__btn--ghost"
              onClick={toggleView}
              aria-label={view === 'grid' ? '切换到列表视图' : '切换到网格视图'}
              title={view === 'grid' ? '切换到列表视图' : '切换到网格视图'}
            >
              <ViewIcon mode={view} />
            </button>
            <button
              type="button"
              className="shelf__btn shelf__btn--icon shelf__btn--ghost"
              onClick={toggleTheme}
              aria-label={theme === 'dark' ? '切换到浅色' : '切换到深色'}
              title={theme === 'dark' ? '切换到浅色' : '切换到深色'}
            >
              <ThemeIcon mode={theme} />
            </button>
            <Menu label="更多" badge={duplicates.length}>
              {(close) => (
                <>
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => {
                      setShowDuplicates((v) => !v);
                      close();
                    }}
                  >
                    重复书籍
                    {duplicates.length > 0 && <span className="shelf__menu-count">{duplicates.length}</span>}
                  </button>
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => {
                      setShowJobs((v) => !v);
                      close();
                    }}
                  >
                    任务历史
                  </button>
                </>
              )}
            </Menu>
            <button onClick={onScan} disabled={scanBusy} className="shelf__btn shelf__btn--primary">
              {scanBusy ? '扫描中…' : '扫描书库'}
            </button>
          </div>
        </div>

        {/* Filters as a single scrollable chip row: one tap to switch,
            and the whole row costs one line instead of the three stacked
            <select>s it replaces — which is what used to push every book
            below the fold on a phone. */}
        <div className="shelf__filters">
          <div className="shelf__chips" role="group" aria-label="筛选">
            {STATUS_FILTERS.map((f) => (
              <button
                key={f.key}
                type="button"
                className={`chip${statusFilter === f.key ? ' is-active' : ''}`}
                onClick={() => setStatusFilter(f.key)}
                aria-pressed={statusFilter === f.key}
              >
                {f.label}
                {f.key === 'all' && <span className="chip__count">{books.length}</span>}
              </button>
            ))}
            {tags.length > 0 && <span className="shelf__chip-divider" aria-hidden="true" />}
            {tags.map((tag) => (
              <button
                key={tag.id}
                type="button"
                className={`chip chip--tag${tagFilter === tag.name ? ' is-active' : ''}`}
                onClick={() => setTagFilter((cur) => (cur === tag.name ? '' : tag.name))}
                aria-pressed={tagFilter === tag.name}
              >
                {tag.name}
              </button>
            ))}
          </div>

          <label className="shelf__sort">
            <SortIcon />
            <select value={sort} onChange={(e) => setSort(e.target.value as SortKey)} aria-label="排序方式">
              <option value="recent">最近阅读</option>
              <option value="added">最近添加</option>
              <option value="title">按书名</option>
            </select>
          </label>
        </div>
      </header>

      {scanMsg && <div className="shelf__notice">{scanMsg}</div>}
      {error && <div className="shelf__notice shelf__notice--error">加载失败：{error}</div>}

      {showDuplicates && (
        <section className="library-panel">
          <header className="library-panel__header">
            <h2>重复书籍</h2>
            <button type="button" className="library-panel__close" onClick={() => setShowDuplicates(false)}>
              关闭
            </button>
          </header>
          {duplicates.length === 0 ? (
            <p className="library-panel__empty">没有发现重复书籍。</p>
          ) : (
            <ul className="duplicate-list">
              {duplicates.map((group) => (
                <li key={group.hash}>
                  <div className="duplicate-list__hash">{group.hash.slice(0, 12)}</div>
                  <div className="duplicate-list__books">
                    {group.books.map((b) => (
                      <span key={b.id}>{b.title}</span>
                    ))}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {showJobs && (
        <section className="library-panel">
          <header className="library-panel__header">
            <h2>任务历史</h2>
            <button type="button" className="library-panel__close" onClick={() => setShowJobs(false)}>
              关闭
            </button>
          </header>
          {jobs.length === 0 ? (
            <p className="library-panel__empty">还没有任务记录。</p>
          ) : (
            <ul className="job-list">
              {jobs.map((job) => (
                <li key={job.id}>
                  <div>
                    <strong>{job.label || job.type}</strong>
                    <span>{job.status}</span>
                    <small>新增 {job.added} · 更新 {job.updated} · 移除 {job.removed}</small>
                    {job.failed && job.failed.length > 0 && <small>失败 {job.failed.length}</small>}
                    {job.error && <small>{job.error}</small>}
                  </div>
                  <button type="button" onClick={() => void onRetryJob(job)} disabled={scanBusy}>
                    重试
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {selected.length > 0 && (
        <section className="batch-bar">
          <span>已选 {selected.length} 本</span>
          <input
            value={batchTag}
            onChange={(e) => setBatchTag(e.target.value)}
            placeholder="批量标签"
          />
          <button type="button" onClick={() => void onBatchTag()} disabled={scanBusy}>打标签</button>
          <button type="button" onClick={() => void onBatchStatus('finished')} disabled={scanBusy}>标为已读</button>
          <button type="button" onClick={() => void onBatchFavorite()} disabled={scanBusy}>收藏</button>
          <button type="button" onClick={() => void onBatchReparse()} disabled={scanBusy}>重解析</button>
          <button type="button" className="batch-bar__danger" onClick={() => askDelete({ kind: 'many', ids: selected })} disabled={scanBusy}>删除…</button>
          <button type="button" onClick={clearSelected}>取消</button>
        </section>
      )}

      {continueReading.length > 0 && (
        <section className="shelf__section">
          <h2 className="shelf__section-title">继续阅读</h2>
          <div className="shelf__continue">
            {continueReading.map((b) => {
              const p = progress[b.id];
              const pct = b.char_count ? Math.round(((p?.char_offset ?? 0) / b.char_count) * 100) : 0;
              return (
                <Link to={`/read/${b.id}`} key={b.id} className="continue-card">
                  <BookCover book={b} className="continue-card__cover" />
                  <div className="continue-card__body">
                    <div className="continue-card__title">{b.title}</div>
                    <div className="continue-card__meta">
                      {b.author ?? '佚名'} · 已读 {pct}%
                    </div>
                    <div className="continue-card__bar">
                      <div className="continue-card__bar-fill" style={{ width: `${pct}%` }} />
                    </div>
                  </div>
                </Link>
              );
            })}
          </div>
        </section>
      )}

      <section className="shelf__section">
        <h2 className="shelf__section-title">全部书籍</h2>
        {loading ? (
          <div className="shelf__empty">
            <span className="shelf__spinner" aria-hidden="true" />
            <p>加载中…</p>
          </div>
        ) : filtered.length === 0 ? (
          <div className="shelf__empty">
            <svg
              className="shelf__empty-icon"
              width="46"
              height="46"
              viewBox="0 0 24 24"
              fill="none"
              aria-hidden="true"
            >
              <path
                d="M12 6.5C10.6 5.2 8.7 4.5 6.5 4.5H4V18h2.5c2.2 0 4.1.7 5.5 2 1.4-1.3 3.3-2 5.5-2H20V4.5h-2.5C15.3 4.5 13.4 5.2 12 6.5Z"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinejoin="round"
              />
              <path d="M12 6.5V20" stroke="currentColor" strokeWidth="1.5" />
            </svg>
            {books.length === 0 ? (
              <>
                <p>书库还是空的</p>
                <p className="shelf__empty-sub">
                  把 .txt / .epub / .pdf / .mobi / .azw3 放进已授权目录，点右上「扫描书库」即可导入。
                </p>
              </>
            ) : (
              <>
                <p>没有匹配的结果</p>
                <p className="shelf__empty-sub">换个关键词，或清除筛选条件再试试。</p>
              </>
            )}
          </div>
        ) : view === 'grid' ? (
          <ul className="book-grid">
            {filtered.map((b) => {
              const p = progress[b.id];
              const pct = b.char_count ? Math.round(((p?.char_offset ?? 0) / b.char_count) * 100) : 0;
              const action = bookBusy[b.id];
              const isSelected = selected.includes(b.id);
              return (
                <li key={b.id} className={`book-card${isSelected ? ' is-selected' : ''}`}>
                  <label className="book-card__select" aria-label={`选择 ${b.title}`}>
                    <input
                      type="checkbox"
                      checked={isSelected}
                      onChange={() => toggleSelected(b.id)}
                    />
                  </label>
                  <Link to={`/read/${b.id}`} className="book-card__link">
                    <BookCover book={b} className="book-card__cover" />
                  </Link>
                  <div className="book-card__body">
                    <Link to={`/read/${b.id}`} className="book-card__title" title={b.title}>
                      {b.title}
                    </Link>
                    <div className="book-card__meta">
                      <span className={`book-row__status book-row__status--${b.reading_status}`}>
                        {STATUS_LABELS[b.reading_status]}
                      </span>
                      <span className="book-row__author">{b.author ?? '佚名'}</span>
                    </div>
                    <div className="book-card__progress">
                      <div className="book-row__bar">
                        <div className="book-row__bar-fill" style={{ width: `${pct}%` }} />
                      </div>
                      <span className="book-card__pct">{pct}%</span>
                    </div>
                  </div>
                  <div className="book-card__tools">
                    {favButton(b, action, 'book-row__fav-btn')}
                    {bookActionMenu(b, action)}
                  </div>
                </li>
              );
            })}
          </ul>
        ) : (
          <ul className="book-list">
            {filtered.map((b) => {
              const p = progress[b.id];
              const pct = b.char_count ? Math.round(((p?.char_offset ?? 0) / b.char_count) * 100) : 0;
              const action = bookBusy[b.id];
              const isSelected = selected.includes(b.id);
              return (
                <li key={b.id} className={`book-row${isSelected ? ' is-selected' : ''}`}>
                  <label className="book-row__select" aria-label={`选择 ${b.title}`}>
                    <input
                      type="checkbox"
                      checked={isSelected}
                      onChange={() => toggleSelected(b.id)}
                    />
                  </label>
                  <Link to={`/read/${b.id}`} className="book-row__link">
                    <BookCover book={b} className="book-row__cover" />
                    <div className="book-row__main">
                      <div className="book-row__title">{b.title}</div>
                      <div className="book-row__meta">
                        <span
                          className={`book-row__status book-row__status--${b.reading_status}`}
                        >
                          {STATUS_LABELS[b.reading_status]}
                        </span>
                        <span className="book-row__author">{b.author ?? '佚名'}</span>
                        {b.category && <span className="book-row__cat">{b.category}</span>}
                        <span className="book-row__stat">
                          {(b.char_count ?? 0).toLocaleString()} 字 · {b.chapter_count ?? 0} 章
                        </span>
                      </div>
                      {(b.tags ?? []).length > 0 && (
                        <div className="book-row__tags">
                          {(b.tags ?? []).map((tag) => (
                            <span key={tag}>{tag}</span>
                          ))}
                        </div>
                      )}
                    </div>
                    <div className="book-row__progress">
                      <div className="book-row__bar">
                        <div className="book-row__bar-fill" style={{ width: `${pct}%` }} />
                      </div>
                      <div className="book-row__pct">{pct}%</div>
                    </div>
                    <div className="book-row__cta">{pct > 0 ? '继续阅读' : '开始阅读'}</div>
                  </Link>
                  <div className="book-row__actions">
                    {favButton(b, action, 'book-row__fav-btn')}
                    {bookActionMenu(b, action)}
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      {uploadOpen && (
        <Dialog
          title="添加书籍"
          onClose={() => setUploadOpen(false)}
          busy={uploadBusy}
          footer={
            <>
              <button type="button" className="shelf__btn" onClick={() => setUploadOpen(false)} disabled={uploadBusy}>
                取消
              </button>
              <button
                type="button"
                className="shelf__btn shelf__btn--primary"
                onClick={onUpload}
                disabled={uploadBusy || uploadFiles.length === 0 || folders.length === 0}
              >
                {uploadBusy ? '添加中…' : '添加'}
              </button>
            </>
          }
        >
          {folders.length > 1 && (
            <label className="field">
              <span>书库</span>
              <select
                value={uploadFolderId ?? ''}
                onChange={(e) => setUploadFolderId(Number(e.target.value))}
                disabled={uploadBusy}
              >
                {folders.map((f) => (
                  <option key={f.id} value={f.id}>
                    {f.path}
                  </option>
                ))}
              </select>
            </label>
          )}

          <label className="upload-drop">
            <input
              type="file"
              multiple
              accept=".txt,.epub,.pdf,.mobi,.azw,.azw3,text/plain,application/epub+zip,application/pdf"
              disabled={uploadBusy}
              onChange={(e) => setUploadFiles(Array.from(e.target.files ?? []))}
            />
            <span>{uploadFiles.length > 0 ? `已选 ${uploadFiles.length} 个文件` : '点击选择文件'}</span>
            <small>支持 .txt / .epub / .pdf / .mobi / .azw3</small>
          </label>

          {uploadFiles.length > 0 && (
            <ul className="upload-files">
              {uploadFiles.map((file) => (
                <li key={`${file.name}-${file.size}`}>{file.name}</li>
              ))}
            </ul>
          )}

          {folders.length === 0 && <div className="form-error">没有可用书库目录</div>}
          {uploadMsg && <div className="form-error">{uploadMsg}</div>}
        </Dialog>
      )}

      {editBook && (
        <Dialog
          title="编辑书籍"
          onClose={() => setEditBook(null)}
          busy={editBusy}
          footer={
            <>
              <button type="button" className="shelf__btn" onClick={() => setEditBook(null)} disabled={editBusy}>
                取消
              </button>
              <button
                type="button"
                className="shelf__btn shelf__btn--primary"
                onClick={() => void onSaveEdit()}
                disabled={editBusy || editForm.title.trim() === ''}
              >
                {editBusy ? '保存中…' : '保存'}
              </button>
            </>
          }
        >
          <label className="field">
            <span>书名</span>
            <input
              value={editForm.title}
              onChange={(e) => setEditForm((f) => ({ ...f, title: e.target.value }))}
              disabled={editBusy}
            />
          </label>
          <div className="field-row">
            <label className="field">
              <span>作者</span>
              <input
                value={editForm.author}
                onChange={(e) => setEditForm((f) => ({ ...f, author: e.target.value }))}
                disabled={editBusy}
              />
            </label>
            <label className="field">
              <span>分类</span>
              <input
                value={editForm.category}
                onChange={(e) => setEditForm((f) => ({ ...f, category: e.target.value }))}
                disabled={editBusy}
              />
            </label>
          </div>
          <label className="field">
            <span>标签</span>
            <input
              value={editForm.tags}
              onChange={(e) => setEditForm((f) => ({ ...f, tags: e.target.value }))}
              placeholder="用空格或逗号分隔"
              disabled={editBusy}
            />
          </label>
          <div className="field-row">
            <label className="field">
              <span>阅读状态</span>
              <select
                value={editForm.reading_status}
                onChange={(e) => setEditForm((f) => ({ ...f, reading_status: e.target.value as ReadingStatus }))}
                disabled={editBusy}
              >
                <option value="unread">未读</option>
                <option value="reading">在读</option>
                <option value="finished">已读完</option>
                <option value="paused">搁置</option>
              </select>
            </label>
            <label className="field field--check">
              <span>收藏</span>
              <input
                type="checkbox"
                checked={editForm.favorite}
                onChange={(e) => setEditForm((f) => ({ ...f, favorite: e.target.checked }))}
                disabled={editBusy}
              />
            </label>
          </div>
          <label className="field">
            <span>简介</span>
            <textarea
              value={editForm.description}
              onChange={(e) => setEditForm((f) => ({ ...f, description: e.target.value }))}
              disabled={editBusy}
            />
          </label>

          {editMsg && <div className="form-error">{editMsg}</div>}
        </Dialog>
      )}

      {exportBook && <ExportDialog book={exportBook} onClose={() => setExportBook(null)} />}

      {deleteTarget && (
        <Dialog
          title="删除书籍"
          compact
          onClose={() => setDeleteTarget(null)}
          busy={deleteBusy}
          footer={
            <>
              <button type="button" className="shelf__btn" onClick={() => setDeleteTarget(null)} disabled={deleteBusy}>
                取消
              </button>
              <button
                type="button"
                className="shelf__btn shelf__btn--danger"
                onClick={() => void onConfirmDelete()}
                disabled={deleteBusy}
              >
                {deleteBusy ? '删除中…' : '删除'}
              </button>
            </>
          }
        >
          <p className="confirm-text">
            {deleteTarget.kind === 'one' ? (
              <>
                确定删除《<b>{deleteTarget.book.title}</b>》吗？
              </>
            ) : (
              <>
                确定删除选中的 <b>{deleteTarget.ids.length}</b> 本书吗？
              </>
            )}
          </p>
          <label className="confirm-check">
            <input
              type="checkbox"
              checked={deleteSource}
              onChange={(e) => setDeleteSource(e.target.checked)}
              disabled={deleteBusy}
            />
            <span>
              同时删除源文件
              <small>
                {deleteSource
                  ? '磁盘上的原始文件会被一并删除，无法恢复。'
                  : '只清除阅读器里的记录和缓存，磁盘上的原始文件保留。'}
              </small>
            </span>
          </label>

          {deleteMsg && <div className="form-error">{deleteMsg}</div>}
        </Dialog>
      )}

    </main>
  );
}
