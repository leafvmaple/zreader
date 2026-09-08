import { useCallback, useState } from 'react';
import * as api from '../api/client';
import type { Chapter, SearchMatch } from '../types/api';

// ReaderSearch is the search drawer, state included.
//
// The state lived on ReaderPage, where it also served as that page's
// general error channel: a failed bookmark delete reported itself through
// `searchMsg` and surfaced inside the search drawer. Owning the state here
// makes the message mean one thing.

type Props = {
  bookId: number;
  chapters: Chapter[];
  /** Called with the match's offset and the term that found it. */
  onSelect: (offset: number, term: string) => void;
};

export function ReaderSearch({ bookId, chapters, onSelect }: Props) {
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<SearchMatch[]>([]);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [total, setTotal] = useState(0);
  const [next, setNext] = useState<number | null>(null);

  // `from` is a character offset, not a page number: the server resumes
  // the scan there. Passing 0 starts a new search and replaces the list;
  // anything else appends, so paging keeps what you have already seen.
  const run = useCallback(
    async (from: number) => {
      const q = query.trim();
      if (!q) {
        setMessage('请输入搜索内容');
        setResults([]);
        setTotal(0);
        setNext(null);
        return;
      }
      setBusy(true);
      setMessage(null);
      try {
        const page = await api.searchBook(bookId, q, { from });
        setResults((prev) => (from === 0 ? page.matches : [...prev, ...page.matches]));
        setTotal(page.total);
        setNext(page.next_from ?? null);
        if (from === 0 && page.matches.length === 0) {
          setMessage('没有匹配结果');
        }
      } catch (err) {
        setMessage(err instanceof Error ? err.message : String(err));
      } finally {
        setBusy(false);
      }
    },
    [bookId, query],
  );

  return (
    <>
      <form
        className="reader-search"
        onSubmit={(e) => {
          e.preventDefault();
          void run(0);
        }}
      >
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="搜索正文"
        />
        <button type="submit" disabled={busy}>
          {busy ? '搜索中…' : '搜索'}
        </button>
      </form>
      {message && <div className="drawer__message">{message}</div>}
      {total > 0 && (
        <div className="search-results__count">
          共 {total} 处，已显示 {results.length}
        </div>
      )}
      <ul className="search-results">
        {results.map((m) => (
          <li key={`${m.char_offset}-${m.chapter_idx}`}>
            <button onClick={() => onSelect(m.char_offset, query.trim())}>
              <span className="search-results__chapter">
                {chapters.find((c) => c.idx === m.chapter_idx)?.title ?? `第 ${m.chapter_idx} 章`}
              </span>
              <span className="search-results__snippet">{m.snippet}</span>
            </button>
          </li>
        ))}
      </ul>
      {next !== null && (
        <button
          type="button"
          className="search-results__more"
          disabled={busy}
          onClick={() => void run(next)}
        >
          {busy ? '加载中…' : '加载更多'}
        </button>
      )}
    </>
  );
}
