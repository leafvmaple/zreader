// Reading settings: the shape, the defaults, the labels the settings
// drawer renders, and the localStorage round trip with its migrations.
//
// Pulled out of ReaderPage because none of it is about reading — it is a
// preferences record that happens to be read by one component. Keeping it
// here means the migrations sit next to the values they migrate, instead
// of a hundred lines up from the only line that calls them.

export type Theme = 'auto' | 'paper' | 'light' | 'green' | 'dark' | 'black';
export type FontSize = 'sm' | 'md' | 'lg' | 'xl';
export type FontFamily = 'sans' | 'serif' | 'kai' | 'custom';
export type LineHeight = 'compact' | 'normal' | 'loose';
export type ParagraphGap = 'compact' | 'normal' | 'loose';
export type PageWidth = 'narrow' | 'normal' | 'wide';
export type IndentMode = 'indent' | 'flush';

export type Settings = {
  theme: Theme;
  size: FontSize;
  font: FontFamily;
  line: LineHeight;
  gap: ParagraphGap;
  width: PageWidth;
  indent: IndentMode;
  /** Filename under <data>/fonts, when font === 'custom'. */
  customFont?: string;
};

export const DEFAULT_SETTINGS: Settings = {
  theme: 'auto',
  size: 'md',
  font: 'serif',
  line: 'normal',
  gap: 'normal',
  width: 'normal',
  indent: 'indent',
};

export const THEME_SWATCHES: { key: Theme; label: string }[] = [
  { key: 'auto', label: '跟随书架' },
  { key: 'paper', label: '纸张' },
  { key: 'light', label: '白' },
  { key: 'green', label: '护眼绿' },
  { key: 'dark', label: '夜间' },
  { key: 'black', label: '纯黑' },
];

// resolveTheme turns the stored preference into the class the page
// actually wears. 'auto' reads the shelf's data-theme attribute, so
// opening a book from a light shelf no longer drops you into a dark
// reader (and vice versa).
export function resolveTheme(theme: Theme): Exclude<Theme, 'auto'> {
  if (theme !== 'auto') return theme;
  if (typeof document === 'undefined') return 'paper';
  return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'paper';
}

export const BUILTIN_FONTS: { key: FontFamily; label: string }[] = [
  { key: 'serif', label: '宋体' },
  { key: 'sans', label: '黑体' },
  { key: 'kai', label: '楷体' },
];
export const LINE_LABELS: Record<LineHeight, string> = {
  compact: '紧凑',
  normal: '标准',
  loose: '宽松',
};
export const GAP_LABELS: Record<ParagraphGap, string> = {
  compact: '紧凑',
  normal: '标准',
  loose: '宽松',
};
// One control for the text column. The old build had both 页宽 and 边距,
// which are the same thing seen from opposite sides — on a wide screen
// the column width is what moves, on a phone it is the margin. This
// drives both (see --r-article-width / --r-content-x).
export const WIDTH_LABELS: Record<PageWidth, string> = {
  narrow: '窄',
  normal: '标准',
  wide: '宽',
};
export const SETTINGS_KEY = 'zreader.settings';
export const HINT_KEY = 'zreader.reader.hinted';

// Themes that existed before the palette was reworked, mapped to their
// closest survivor. Without this an upgrading user lands on a class that
// no longer exists and silently gets the stylesheet's fallback colours.
export const LEGACY_THEMES: Record<string, Theme> = {
  beige: 'paper',
  white: 'light',
  grey: 'dark',
};

// Likewise for fonts: `system` and `songti` were the same serif stack and
// `wenkai` was a CDN webfont that no longer loads.
export const LEGACY_FONTS: Record<string, FontFamily> = {
  system: 'serif',
  songti: 'serif',
  wenkai: 'kai',
};

export function loadSettings(): Settings {
  try {
    const raw = localStorage.getItem(SETTINGS_KEY);
    if (raw) {
      const stored = JSON.parse(raw) as Partial<Settings> & { margin?: string };
      // `margin` folded into `width`; drop it rather than let it linger.
      delete stored.margin;
      if (stored.theme && LEGACY_THEMES[stored.theme]) {
        stored.theme = LEGACY_THEMES[stored.theme];
      }
      if (stored.font && LEGACY_FONTS[stored.font]) {
        stored.font = LEGACY_FONTS[stored.font];
      }
      return { ...DEFAULT_SETTINGS, ...stored };
    }
  } catch {
    /* ignore */
  }
  return DEFAULT_SETTINGS;
}

export function saveSettings(s: Settings) {
  try {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(s));
  } catch {
    /* ignore */
  }
}
