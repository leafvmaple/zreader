import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../api/client';
import type { Book, Progress } from '../types/api';
import './ShelfPage.css';

type SortKey = 'recent' | 'title' | 'added';

export function ShelfPage() {
  const [books, setBooks] = useState<Book[]>([]);
  const [progress, setProgress] = useState<Record<number, Progress>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [scanBusy, setScanBusy] = useState(false);
  const [scanMsg, setScanMsg] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [sort, setSort] = useState<SortKey>('recent');

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const list = await api.listBooks();
      setBooks(list);
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
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </main>
  );
}
