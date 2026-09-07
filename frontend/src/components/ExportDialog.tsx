import { useCallback, useEffect, useState } from 'react';
import * as api from '../api/client';
import type { Book, ExportPreview, ExportRules } from '../types/api';
import { Dialog } from './Dialog';
import './ExportDialog.css';

// ExportDialog configures a cleaned JSONL export.
//
// The preview is the point: cleaning rules delete text, and a rule that
// looks reasonable in the abstract can eat a paragraph you wanted. Every
// toggle re-runs the export server-side and reports what it removed plus
// the first chapters verbatim, so the decision is made against the real
// output rather than a description of it.

const RULE_LABELS: { key: keyof ExportRules; label: string; hint: string }[] = [
  { key: 'promo', label: '盗版站推广行', hint: '正文里夹带的网址、「记住本站」、「本书由…整理」' },
  { key: 'edges', label: '重复页眉页脚', hint: '按跨章重复率识别，长段落不会被误删' },
  { key: 'normalise', label: '空白与标点', hint: '首行缩进、连续空格、。。。→……、半角标点转全角' },
  { key: 'notes', label: '作者的话 / 章末感言', hint: '「作者有话说」「求推荐票」及其之后的段落' },
];

const DEFAULT_RULES: ExportRules = { promo: true, edges: true, normalise: true, notes: true };

export function ExportDialog({ book, onClose }: { book: Book; onClose: () => void }) {
  const [rules, setRules] = useState<ExportRules>(DEFAULT_RULES);
  const [anonymize, setAnonymize] = useState(false);
  const [preview, setPreview] = useState<ExportPreview | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Re-preview after each rule change so destructive passes are visible
  // before the user commits to a download.
  useEffect(() => {
    let cancelled = false;
    setBusy(true);
    const timer = setTimeout(() => {
      api
        .previewExport(book.id, rules, anonymize)
        .then((p) => {
          if (cancelled) return;
          setPreview(p);
          setError(null);
        })
        .catch((err: unknown) => {
          if (cancelled) return;
          setError(err instanceof Error ? err.message : String(err));
          setPreview(null);
        })
        .finally(() => {
          if (!cancelled) setBusy(false);
        });
    }, 220);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [anonymize, book.id, rules]);

  const toggle = useCallback((key: keyof ExportRules) => {
    setRules((r) => ({ ...r, [key]: !r[key] }));
  }, []);

  const stats = preview?.stats;
  const removed = stats ? stats.chars_in - stats.chars_out : 0;
  const removedPct = stats && stats.chars_in > 0 ? Math.round((removed / stats.chars_in) * 1000) / 10 : 0;
  const canDownload = Boolean(!busy && preview && stats?.replacement_characters === 0);

  return (
    <Dialog
      title="导出给 AI"
      onClose={onClose}
      footer={
        <>
          <button type="button" className="shelf__btn" onClick={onClose}>
            取消
          </button>
          <a
            className={`shelf__btn shelf__btn--primary${canDownload ? '' : ' is-disabled'}`}
            href={canDownload ? api.exportURL(book.id, rules, anonymize) : undefined}
            download={preview?.filename}
            aria-disabled={!canDownload}
          >
            下载 JSONL
          </a>
        </>
      }
    >
      <p className="export__intro">
        把《{book.title}》导出为训练语料 JSONL，每章一条记录，附带稳定 ID、清洗配置和校验哈希。
      </p>

      <div className="export__section">
        <span className="export__label">清洗规则</span>
        <div className="export__rules">
          {RULE_LABELS.map((r) => (
            <label key={r.key} className={`export__rule${rules[r.key] ? ' is-on' : ''}`}>
              <input type="checkbox" checked={rules[r.key]} onChange={() => toggle(r.key)} />
              <span>
                {r.label}
                <small>{r.hint}</small>
              </span>
            </label>
          ))}
        </div>
      </div>

      <div className="export__section">
        <span className="export__label">隐私</span>
        <div className="export__rules">
          <label className={`export__rule${anonymize ? ' is-on' : ''}`}>
            <input type="checkbox" checked={anonymize} onChange={() => setAnonymize((value) => !value)} />
            <span>
              匿名化语料身份
              <small>文件名始终使用稳定哈希；开启后同时隐藏书名、作者和原始章节名</small>
            </span>
          </label>
        </div>
      </div>

      <div className={`export__result${busy ? ' is-busy' : ''}`}>
        {error && <div className="form-error">预览失败：{error}</div>}
        {preview?.rules_warning && <div className="form-error">{preview.rules_warning}</div>}
        {stats && (
          <>
            {stats.replacement_characters > 0 && (
              <div className="form-error">
                检测到 {stats.replacement_characters.toLocaleString()} 个乱码替代字符（�）。请修复或重新导入源文件后再下载。
              </div>
            )}
            {stats.chapter_structure_warning && (
              <p className="export__notice">
                当前只识别到一个通用“正文”章节。请先确认目录解析是否正确；导出仍会保留完整正文。
              </p>
            )}
            <div className="export__stats">
              <div>
                <b>{stats.records.toLocaleString()}</b>
                <span>章节记录</span>
              </div>
              <div>
                <b>{stats.chars_out.toLocaleString()}</b>
                <span>导出字数</span>
              </div>
              <div>
                <b>{removedPct}%</b>
                <span>已清除</span>
              </div>
              <div>
                <b>{stats.dropped_paragraphs.toLocaleString()}</b>
                <span>删除段落</span>
              </div>
            </div>
            {preview.sample.length > 0 && (
              <>
                <span className="export__label">前 {preview.sample.length} 章预览</span>
                <pre className="export__sample">
                  {preview.sample
                    .map((c) => JSON.stringify({ ...c, text: c.text }, null, 0))
                    .join('\n')}
                </pre>
              </>
            )}
          </>
        )}
      </div>

      <p className="export__hint">
        规则不够用时，在 <code>&lt;data&gt;/clean-rules.json</code> 里加自己的正则即可扩展，不用改代码。
      </p>
    </Dialog>
  );
}
