import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../api/client';
import type { Book, Folder, Progress } from '../types/api';
import './ShelfPage.css';

type SortKey = 'recent' | 'title' | 'added';
type BookAction = 'reparse' | 'delete';

function baseName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).pop() ?? path;
}

export function ShelfPage() {
  const [books, setBooks] = useState<Book[]>([]);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [progress, setProgress] = useState<Record<number, Progress>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [scanBusy, setScanBusy] = useState(false);
  const [scanMsg, setScanMsg] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [sort, setSort] = useState<SortKey>('recent');
  const [uploadOpen, setUploadOpen] = useState(false);
  const [uploadBusy, setUploadBusy] = useState(false);
  const [uploadFiles, setUploadFiles] = useState<File[]>([]);
  const [uploadFolderId, setUploadFolderId] = useState<number | undefined>(undefined);
  const [uploadMsg, setUploadMsg] = useState<string | null>(null);
  const [bookBusy, setBookBusy] = useState<Record<number, BookAction>>({});

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [list, folderList] = await Promise.all([api.listBooks(), api.listFolders()]);
      setBooks(list);
      setFolders(folderList);
      setUploadFolderId((current) =>
        current !== undefined && folderList.some((f) => f.id === current)
          ? current
          : folderList[0]?.id,
      );
      // Fan out progress fetches — fine for a few dozen books; if a library
      // grows past that we'll batch this into a single endpoint.
      const progPairs = await Promise.all(
        list.map(async (b) => [b.id, await api.getProgress(b.id)] as const),
      );
      const map: Record<number, Progress> = {};
      for (const [id, p] of progPairs) map[id] = p;
      setProgress(map);
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

  const onDeleteBook = useCallback(
    async (book: Book) => {
      const confirmed = window.confirm(`确定删除《${book.title}》吗？这会同时删除源文件和书库记录。`);
      if (!confirmed) return;
      setBookBusy((prev) => ({ ...prev, [book.id]: 'delete' }));
      setScanMsg(null);
      try {
        await api.deleteBook(book.id, true);
        setScanMsg('已删除书籍和源文件');
        await refresh();
      } catch (err) {
        if (err instanceof api.ApiError && err.code === 'source_not_found') {
          const deleteRecordOnly = window.confirm('源文件没有找到。是否只删除阅读器里的记录和缓存？');
          if (deleteRecordOnly) {
            try {
              await api.deleteBook(book.id, false);
              setScanMsg('已删除书籍记录和缓存');
              await refresh();
              return;
            } catch (fallbackErr) {
              setScanMsg(`删除失败：${fallbackErr instanceof Error ? fallbackErr.message : String(fallbackErr)}`);
              return;
            }
          }
        }
        setScanMsg(`删除失败：${err instanceof Error ? err.message : String(err)}`);
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

  // --- Derived views -------------------------------------------------------

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    let xs = books;
    if (q) {
      xs = xs.filter(
        (b) =>
          b.title.toLowerCase().includes(q) ||
          (b.author ?? '').toLowerCase().includes(q),
      );
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
  }, [books, progress, search, sort]);

  const continueReading = useMemo(() => {
    return books
      .filter((b) => (progress[b.id]?.updated_at ?? 0) > 0)
      .sort((a, b) => (progress[b.id]?.updated_at ?? 0) - (progress[a.id]?.updated_at ?? 0))
      .slice(0, 5);
  }, [books, progress]);

  // --- Render --------------------------------------------------------------

  return (
    <main className="shelf">
      <header className="shelf__header">
        <div className="shelf__title">
          <h1>zreader</h1>
          <span className="shelf__count">{books.length} 本</span>
        </div>
        <div className="shelf__toolbar">
          <input
            type="search"
            placeholder="搜索书名 / 作者"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="shelf__search"
          />
          <select
            value={sort}
            onChange={(e) => setSort(e.target.value as SortKey)}
            className="shelf__sort"
            aria-label="排序方式"
          >
            <option value="recent">最近阅读</option>
            <option value="added">最近添加</option>
            <option value="title">按书名</option>
          </select>
          <button
            onClick={() => {
              setUploadOpen(true);
              setUploadMsg(null);
            }}
            disabled={uploadBusy}
            className="shelf__btn"
          >
            添加书籍
          </button>
          <button onClick={onScan} disabled={scanBusy} className="shelf__btn shelf__btn--primary">
            {scanBusy ? '扫描中…' : '扫描书库'}
          </button>
        </div>
      </header>

      {scanMsg && <div className="shelf__notice">{scanMsg}</div>}
      {error && <div className="shelf__notice shelf__notice--error">加载失败：{error}</div>}

      {continueReading.length > 0 && (
        <section className="shelf__section">
          <h2 className="shelf__section-title">继续阅读</h2>
          <div className="shelf__continue">
            {continueReading.map((b) => {
              const p = progress[b.id];
              const pct = b.char_count ? Math.round(((p?.char_offset ?? 0) / b.char_count) * 100) : 0;
              return (
                <Link to={`/read/${b.id}`} key={b.id} className="continue-card">
                  <div className="continue-card__title">{b.title}</div>
                  <div className="continue-card__meta">
                    {b.author ?? '佚名'} · 已读 {pct}%
                  </div>
                  <div className="continue-card__bar">
                    <div className="continue-card__bar-fill" style={{ width: `${pct}%` }} />
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
          <p className="shelf__empty">加载中…</p>
        ) : filtered.length === 0 ? (
          <p className="shelf__empty">
            {books.length === 0
              ? '书库为空。点击右上「扫描书库」抓取已授权目录里的 .txt / .epub / .pdf 文件。'
              : '没有匹配的结果。'}
          </p>
        ) : (
          <ul className="book-list">
            {filtered.map((b) => {
              const p = progress[b.id];
              const pct = b.char_count ? Math.round(((p?.char_offset ?? 0) / b.char_count) * 100) : 0;
              const action = bookBusy[b.id];
              return (
                <li key={b.id} className="book-row">
                  <Link to={`/read/${b.id}`} className="book-row__link">
                    <div className="book-row__main">
                      <div className="book-row__title">{b.title}</div>
                      <div className="book-row__meta">
                        <span>{b.author ?? '佚名'}</span>
                        <span>·</span>
                        <span>{(b.char_count ?? 0).toLocaleString()} 字</span>
                        <span>·</span>
                        <span>{b.chapter_count ?? 0} 章</span>
                        <span>·</span>
                        <span>{b.encoding ?? '?'}</span>
                      </div>
                    </div>
                    <div className="book-row__progress">
                      <div className="book-row__pct">{pct}%</div>
                      <div className="book-row__bar">
                        <div className="book-row__bar-fill" style={{ width: `${pct}%` }} />
                      </div>
                    </div>
                    <div className="book-row__cta">{pct > 0 ? '继续阅读' : '开始阅读'}</div>
                  </Link>
                  <div className="book-row__actions">
                    <button
                      type="button"
                      className="book-row__action"
                      onClick={() => void onReparseBook(b)}
                      disabled={action !== undefined}
                      title="重新解析这本书"
                    >
                      {action === 'reparse' ? '解析中…' : '重解析'}
                    </button>
                    <button
                      type="button"
                      className="book-row__action book-row__action--danger"
                      onClick={() => void onDeleteBook(b)}
                      disabled={action !== undefined}
                      title="删除这本书"
                    >
                      {action === 'delete' ? '删除中…' : '删除'}
                    </button>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      {uploadOpen && (
        <div className="upload-dialog" role="dialog" aria-modal="true" aria-label="添加书籍">
          <div className="upload-dialog__panel">
            <header className="upload-dialog__header">
              <h2>添加书籍</h2>
              <button
                type="button"
                className="upload-dialog__close"
                onClick={() => setUploadOpen(false)}
                disabled={uploadBusy}
                aria-label="关闭"
              >
                ×
              </button>
            </header>

            {folders.length > 1 && (
              <label className="upload-dialog__field">
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

            <label className="upload-dialog__drop">
              <input
                type="file"
                multiple
                accept=".txt,.epub,.pdf,text/plain,application/epub+zip,application/pdf"
                disabled={uploadBusy}
                onChange={(e) => setUploadFiles(Array.from(e.target.files ?? []))}
              />
              <span>{uploadFiles.length > 0 ? `${uploadFiles.length} 个文件` : '选择文件'}</span>
            </label>

            {uploadFiles.length > 0 && (
              <ul className="upload-dialog__files">
                {uploadFiles.map((file) => (
                  <li key={`${file.name}-${file.size}`}>{file.name}</li>
                ))}
              </ul>
            )}

            {folders.length === 0 && <div className="upload-dialog__error">没有可用书库目录</div>}
            {uploadMsg && <div className="upload-dialog__error">{uploadMsg}</div>}

            <footer className="upload-dialog__actions">
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
            </footer>
          </div>
        </div>
      )}
    </main>
  );
}
