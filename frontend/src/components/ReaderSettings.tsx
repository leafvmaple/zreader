import type { ReadingFont } from '../types/api';
import {
  BUILTIN_FONTS,
  GAP_LABELS,
  LINE_LABELS,
  THEME_SWATCHES,
  WIDTH_LABELS,
} from '../reader/settings';
import type {
  FontSize,
  IndentMode,
  LineHeight,
  PageWidth,
  ParagraphGap,
  Settings,
} from '../reader/settings';

// ReaderSettings is the body of the settings drawer: six rows of
// segmented controls over one Settings record. It was 130 lines of JSX in
// the middle of ReaderPage's render, between the search results and the
// bookmark list, with nothing to do with either.

type Props = {
  settings: Settings;
  setSettings: (update: (s: Settings) => Settings) => void;
  customFonts: ReadingFont[];
  onReset: () => void;
};

export function ReaderSettings({ settings, setSettings, customFonts, onReset }: Props) {
  return (
        <div className="settings">
          <div className="settings__row">
            <span className="settings__label">主题</span>
            <div className="settings__themes">
              {THEME_SWATCHES.map((t) => (
                <button
                  key={t.key}
                  className={`theme-swatch theme-swatch--${t.key}${settings.theme === t.key ? ' is-active' : ''}`}
                  onClick={() => setSettings((s) => ({ ...s, theme: t.key }))}
                  aria-label={t.label}
                  aria-pressed={settings.theme === t.key}
                  title={t.label}
                />
              ))}
            </div>
          </div>
          <div className="settings__row">
            <span className="settings__label">字号</span>
            <div className="settings__sizes">
              {(['sm', 'md', 'lg', 'xl'] as FontSize[]).map((sz) => (
                <button
                  key={sz}
                  className={`size-btn size-btn--${sz}${settings.size === sz ? ' is-active' : ''}`}
                  aria-pressed={settings.size === sz}
                  onClick={() => setSettings((s) => ({ ...s, size: sz }))}
                >
                  A
                </button>
              ))}
            </div>
          </div>
          <div className="settings__row">
            <span className="settings__label">字体</span>
            <div className="settings__fonts">
              {BUILTIN_FONTS.map((f) => (
                <button
                  key={f.key}
                  className={`font-btn font-btn--${f.key}${settings.font === f.key ? ' is-active' : ''}`}
                  aria-pressed={settings.font === f.key}
                  onClick={() => setSettings((s) => ({ ...s, font: f.key }))}
                >
                  {f.label}
                </button>
              ))}
            </div>
            {customFonts.length > 0 && (
              <div className="settings__fonts settings__fonts--custom">
                {customFonts.map((f) => (
                  <button
                    key={f.file}
                    className={`font-btn font-btn--custom${
                      settings.font === 'custom' && settings.customFont === f.file ? ' is-active' : ''
                    }`}
                    onClick={() => setSettings((s) => ({ ...s, font: 'custom', customFont: f.file }))}
                    title={f.name}
                  >
                    {f.name}
                  </button>
                ))}
              </div>
            )}
            <p className="settings__hint">
              内置字体全部来自系统，不联网。把 woff2 / ttf 放进 <code>&lt;data&gt;/fonts</code> 可以加入这个列表。
            </p>
          </div>
          <div className="settings__row">
            <span className="settings__label">行距</span>
            <div className="settings__seg">
              {(['compact', 'normal', 'loose'] as LineHeight[]).map((v) => (
                <button
                  key={v}
                  className={settings.line === v ? 'is-active' : ''}
                  aria-pressed={settings.line === v}
                  onClick={() => setSettings((s) => ({ ...s, line: v }))}
                >
                  {LINE_LABELS[v]}
                </button>
              ))}
            </div>
          </div>
          <div className="settings__row">
            <span className="settings__label">段距</span>
            <div className="settings__seg">
              {(['compact', 'normal', 'loose'] as ParagraphGap[]).map((v) => (
                <button
                  key={v}
                  className={settings.gap === v ? 'is-active' : ''}
                  aria-pressed={settings.gap === v}
                  onClick={() => setSettings((s) => ({ ...s, gap: v }))}
                >
                  {GAP_LABELS[v]}
                </button>
              ))}
            </div>
          </div>
          <div className="settings__row">
            <span className="settings__label">版面</span>
            <div className="settings__seg">
              {(['narrow', 'normal', 'wide'] as PageWidth[]).map((v) => (
                <button
                  key={v}
                  className={settings.width === v ? 'is-active' : ''}
                  aria-pressed={settings.width === v}
                  onClick={() => setSettings((s) => ({ ...s, width: v }))}
                >
                  {WIDTH_LABELS[v]}
                </button>
              ))}
            </div>
          </div>
          <div className="settings__row">
            <span className="settings__label">首行缩进</span>
            <div className="settings__seg">
              {(['indent', 'flush'] as IndentMode[]).map((v) => (
                <button
                  key={v}
                  className={settings.indent === v ? 'is-active' : ''}
                  aria-pressed={settings.indent === v}
                  onClick={() => setSettings((s) => ({ ...s, indent: v }))}
                >
                  {v === 'indent' ? '开启' : '关闭'}
                </button>
              ))}
            </div>
          </div>
          <button
            type="button"
            className="settings__reset"
            onClick={onReset}
          >
            恢复默认设置
          </button>
        </div>
  );
}
