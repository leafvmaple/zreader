import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { existsSync } from 'node:fs';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, '..', '..');
const backendDir = path.join(repoRoot, 'backend');
const distIndex = path.join(backendDir, 'internal', 'webui', 'dist', 'index.html');

function assert(ok, message) {
  if (!ok) throw new Error(message);
}

// Per-book actions sit behind a "⋯" that is only revealed on hover, so the
// menu has to be opened before its items can be clicked.
async function openRowMenu(page, index) {
  const row = page.locator('.book-row').nth(index);
  await row.hover();
  await row.locator('.book-row__more').click();
  await page.waitForSelector('.shelf__menu-pop');
}

// Library maintenance (duplicates, job history) lives in the header's
// overflow menu rather than as top-level buttons.
async function openHeaderMenu(page, itemText) {
  // The header holds two "⋯"-style menus now — the account menu and this
  // one — so target by label rather than by position.
  await page.getByRole('button', { name: '更多' }).click();
  await page.waitForSelector('.shelf__menu-pop');
  await page.locator('.shelf__menu-pop button', { hasText: itemText }).click();
}

async function freePort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      server.close(() => resolve(address.port));
    });
    server.on('error', reject);
  });
}

async function waitForServer(url, proc, logs) {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    if (proc.exitCode !== null) {
      throw new Error(`server exited early with ${proc.exitCode}\n${logs.text()}`);
    }
    try {
      const res = await fetch(url);
      if (res.ok) return;
    } catch {
      // keep polling
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`server did not become ready\n${logs.text()}`);
}

function captureLogs(proc) {
  const chunks = [];
  const add = (buf) => {
    chunks.push(String(buf));
    if (chunks.length > 120) chunks.shift();
  };
  proc.stdout.on('data', add);
  proc.stderr.on('data', add);
  return {
    text() {
      return chunks.join('');
    },
  };
}

async function runCommand(command, args, options) {
  const proc = spawn(command, args, options);
  const logs = captureLogs(proc);
  const code = await new Promise((resolve) => proc.once('exit', resolve));
  if (code !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed with ${code}\n${logs.text()}`);
  }
}

async function stopServer(proc) {
  if (proc.exitCode !== null) return;
  proc.kill('SIGTERM');
  await new Promise((resolve) => {
    const timer = setTimeout(() => {
      if (proc.exitCode === null) proc.kill('SIGKILL');
      resolve();
    }, 5_000);
    proc.once('exit', () => {
      clearTimeout(timer);
      resolve();
    });
  });
}

// A line the AI export's promo rule is expected to strip. Kept synthetic
// and pointed at a reserved-for-testing domain so the fixture never
// resembles a real site.
const PROMO_LINE = '更多精彩小说尽在 www.example-invalid.test，请记住本站域名！';

function longBookText(needle) {
  const paragraphs = [];
  for (let i = 0; i < 80; i += 1) {
    paragraphs.push(`Paragraph ${i}. Alpha beta gamma delta epsilon zeta eta theta.`);
  }
  paragraphs.splice(20, 0, `This paragraph contains ${needle} for search.`);
  paragraphs.splice(40, 0, PROMO_LINE);
  return `Chapter 1\n\n${paragraphs.join('\n\n')}\n\nChapter 2\n\nTail paragraph.`;
}

function escapePDFString(s) {
  return s.replaceAll('\\', '\\\\').replaceAll('(', '\\(').replaceAll(')', '\\)');
}

function simplePDFBytes(title, author) {
  const content = 'BT /F1 12 Tf\nET\n';
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>',
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
    `<< /Length ${content.length} >>\nstream\n${content}\nendstream`,
    `<< /Title (${escapePDFString(title)}) /Author (${escapePDFString(author)}) >>`,
  ];
  let body = '%PDF-1.4\n';
  const offsets = [];
  for (let i = 0; i < objects.length; i += 1) {
    offsets.push(Buffer.byteLength(body));
    body += `${i + 1} 0 obj\n${objects[i]}\nendobj\n`;
  }
  const xref = Buffer.byteLength(body);
  body += `xref\n0 ${objects.length + 1}\n`;
  body += '0000000000 65535 f \n';
  for (const off of offsets) {
    body += `${String(off).padStart(10, '0')} 00000 n \n`;
  }
  body += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R /Info 6 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(body, 'utf8');
}

if (!existsSync(distIndex)) {
  throw new Error('frontend dist is missing; run pnpm build before this e2e test');
}

const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'zreader-v08-e2e-'));
const dataDir = path.join(tempRoot, 'data');
const libraryDir = path.join(tempRoot, 'books');
await mkdir(dataDir, { recursive: true });
await mkdir(libraryDir, { recursive: true });

const serverBin = path.join(tempRoot, process.platform === 'win32' ? 'zreader-e2e.exe' : 'zreader-e2e');
await runCommand('go', ['build', '-o', serverBin, './cmd/zreader'], {
  cwd: backendDir,
  stdio: ['ignore', 'pipe', 'pipe'],
});

const port = await freePort();
const baseURL = `http://127.0.0.1:${port}`;
const server = spawn(serverBin, [], {
  cwd: backendDir,
  env: {
    ...process.env,
    ZREADER_PORT: String(port),
    ZREADER_DATA_DIR: dataDir,
    ZREADER_LIBRARY_PATH: libraryDir,
  },
  stdio: ['ignore', 'pipe', 'pipe'],
});
const logs = captureLogs(server);

let browser;
try {
  await waitForServer(baseURL, server, logs);
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });

  await page.goto(baseURL, { waitUntil: 'networkidle' });

  // A fresh install is gated on creating the first account, and the library
  // must not be reachable before that. Doing setup here also covers the
  // upgrade path: everything after this runs as a signed-in admin.
  assert(await page.locator('.auth__card').isVisible(), 'first run did not show the setup screen');
  assert(
    /第一次启动/.test(await page.locator('.auth__lede').innerText()),
    'setup screen showed login copy instead of first-run copy',
  );
  const apiStatus = await page.evaluate(async () => (await fetch('/api/v1/books')).status);
  assert(apiStatus === 401, `GET /books before setup = ${apiStatus}, want 401`);

  await page.locator('.auth__card input').nth(0).fill('tester');
  await page.locator('.auth__card input').nth(1).fill('password123');
  await page.locator('.auth__card input').nth(2).fill('password123');
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/auth/setup') && r.status() === 201),
    page.locator('.auth__submit').click(),
  ]);
  await page.waitForSelector('.shelf');
  assert(await page.getByRole('button', { name: '添加书籍' }).isVisible(), 'add-book button missing');

  const uploadPath = path.join(tempRoot, 'BookA - AuthorX.txt');
  await writeFile(uploadPath, longBookText('OldNeedle'), 'utf8');
  await page.getByRole('button', { name: '添加书籍' }).click();
  assert(await page.locator('.dlg__panel').isVisible(), 'upload dialog did not open');
  await page.locator('.dlg input[type=file]').setInputFiles(uploadPath);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/library/upload') && r.status() === 201),
    page.locator('.dlg__actions .shelf__btn--primary').click(),
  ]);
  await page.waitForSelector('.book-row');
  assert((await page.locator('.book-row').count()) === 1, 'uploaded book row missing');

  // Per-book actions live behind a "⋯" menu; only the favourite star is
  // direct. A resting row must not expose four buttons, least of all delete.
  assert(
    (await page.locator('.book-row').first().locator('.book-row__more').count()) === 1,
    'per-book overflow menu missing',
  );
  await openRowMenu(page, 0);
  const menuItems = await page.locator('.shelf__menu-pop button').allInnerTexts();
  assert(menuItems.length === 4, `row menu should hold 4 actions, saw ${menuItems.length}`);
  assert(/删除/.test(menuItems[3]), 'delete should be the last row action');
  await page.keyboard.press('Escape');

  // P1: toolbar is split into a filter group and an action group.
  assert((await page.locator('.shelf__filters').count()) === 1, 'toolbar filter group missing');
  assert((await page.locator('.shelf__actions').count()) === 1, 'toolbar action group missing');

  // P0: the theme toggle flips data-theme on <html>, persists the choice to
  // localStorage, and round-trips back to the starting theme.
  const themeBefore = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
  await page.getByRole('button', { name: /切换到(浅色|深色)/ }).click();
  const themeAfter = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
  assert(themeAfter && themeAfter !== themeBefore, 'theme toggle did not change data-theme');
  assert(
    (await page.evaluate(() => localStorage.getItem('zreader.theme'))) === themeAfter,
    'theme choice was not persisted',
  );
  await page.getByRole('button', { name: /切换到(浅色|深色)/ }).click();
  assert(
    (await page.evaluate(() => document.documentElement.getAttribute('data-theme'))) === themeBefore,
    'theme toggle did not round-trip to the starting theme',
  );

  // P1: list <-> grid view toggle, persisted to localStorage.
  assert((await page.locator('.book-list').count()) === 1, 'shelf should default to list view');
  await page.getByRole('button', { name: /切换到(列表|网格)视图/ }).click();
  await page.waitForSelector('.book-grid');
  assert((await page.locator('.book-card').count()) === 1, 'grid view did not render a card per book');
  assert((await page.locator('.book-list').count()) === 0, 'grid view should replace the list');
  assert(
    (await page.evaluate(() => localStorage.getItem('zreader.view'))) === 'grid',
    'view choice was not persisted',
  );
  await page.getByRole('button', { name: /切换到(列表|网格)视图/ }).click();
  await page.waitForSelector('.book-list');
  assert((await page.locator('.book-grid').count()) === 0, 'view toggle did not return to list');

  const secondUploadPath = path.join(tempRoot, 'BookB - AuthorY.txt');
  await writeFile(secondUploadPath, longBookText('OldNeedle'), 'utf8');
  await page.getByRole('button', { name: '添加书籍' }).click();
  await page.locator('.dlg input[type=file]').setInputFiles(secondUploadPath);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/library/upload') && r.status() === 201),
    page.locator('.dlg__actions .shelf__btn--primary').click(),
  ]);
  await page.waitForFunction(() => document.querySelectorAll('.book-row').length === 2);

  await openHeaderMenu(page, '重复书籍');
  await page.waitForSelector('.duplicate-list li');
  assert((await page.locator('.duplicate-list li').count()) === 1, 'duplicate panel did not show duplicate group');
  await page.locator('.library-panel__close').click();

  await openRowMenu(page, 0);
  await page.locator('.shelf__menu-pop button').nth(0).click();
  await page.waitForSelector('.dlg__panel');
  await page.locator('.dlg .field input').nth(0).fill('EditedTitle');
  await page.locator('.dlg .field input').nth(1).fill('EditedAuthor');
  await page.locator('.dlg .field input').nth(2).fill('CategoryA');
  await page.locator('.dlg .field input').nth(3).fill('TagA TagB');
  await page.locator('.dlg .field select').selectOption('reading');
  await page.locator('.dlg .field--check input').check();
  await page.locator('.dlg .field textarea').fill('Short description');
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/books/') && r.request().method() === 'PATCH' && r.status() === 200),
    page.locator('.dlg__actions .shelf__btn--primary').click(),
  ]);
  await page.waitForFunction(() => document.body.textContent.includes('EditedTitle'));
  assert(await page.locator('.book-row__tags span', { hasText: 'TagA' }).count() > 0, 'edited tags not visible');

  // P1: reading status renders as a highlighted chip and the developer-facing
  // encoding is no longer part of the meta line.
  assert((await page.locator('.book-row__status--reading').count()) >= 1, 'reading status chip missing');
  const firstMetaText = await page.locator('.book-row__meta').first().innerText();
  assert(!/utf-?8/i.test(firstMetaText), 'encoding should no longer appear in the book meta line');

  await page.locator('.book-row__select input').nth(0).check();
  await page.locator('.book-row__select input').nth(1).check();
  await page.locator('.batch-bar input').fill('BatchTag');
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/books/batch') && r.status() === 200),
    page.locator('.batch-bar button', { hasText: '打标签' }).click(),
  ]);
  await page.waitForFunction(() => document.body.textContent.includes('BatchTag'));

  await openHeaderMenu(page, '任务历史');
  await page.waitForSelector('.job-list li');
  assert((await page.locator('.job-list li').count()) > 0, 'job history did not open');
  await page.locator('.library-panel__close').click();

  // P2: a no-match search shows the friendly empty state, then clears.
  await page.locator('.shelf__search').fill('zzz_no_such_book_zzz');
  await page.waitForSelector('.shelf__empty');
  assert(
    /没有匹配/.test(await page.locator('.shelf__empty').innerText()),
    'no-match empty state did not render',
  );
  await page.locator('.shelf__search').fill('');
  await page.waitForSelector('.book-row');

  // Both uploads share identical content (they're the duplicate pair), so row
  // order between them isn't guaranteed. Rewrite both sources with the fresh
  // needle: whichever row sorts first, reparsing it then yields searchable
  // FreshNeedle text — keeps the reparse->search assertion deterministic.
  const freshText = longBookText('FreshNeedle');
  await writeFile(path.join(libraryDir, 'BookA - AuthorX.txt'), freshText, 'utf8');
  await writeFile(path.join(libraryDir, 'BookB - AuthorY.txt'), freshText, 'utf8');
  await openRowMenu(page, 0);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/books/') && r.url().includes('/reparse') && r.status() === 200),
    page.locator('.shelf__menu-pop button').nth(1).click(),
  ]);

  await page.locator('.book-row').first().locator('a.book-row__link').click();
  await page.waitForURL(/\/read\/\d+/, { timeout: 10_000 });
  await page.waitForSelector('.reader__article');
  assert((await page.locator('.reader__error').count()) === 0, 'reader loaded with an error');

  // P1: chrome uses one SVG icon set; search lives only in the top bar and
  // settings only in the bottom bar — no controls duplicated across bars.
  const topIcons = page.locator('.reader__top button.reader__icon-btn');
  const bottomIcons = page.locator('.reader__bottom button.reader__icon-btn');
  assert((await topIcons.count()) === 2, 'reader top bar should have exactly search + add-bookmark');
  assert((await bottomIcons.count()) === 3, 'reader bottom bar should have exactly toc + bookmarks + settings');
  assert(
    (await topIcons.locator('svg').count()) === 2 && (await bottomIcons.locator('svg').count()) === 3,
    'reader chrome buttons should render svg icons, not text or emoji',
  );

  // P2: the first-entry hint shows once, dismisses, and is remembered. Dismiss
  // it before driving the chrome, since it overlays the bars while up.
  assert(await page.locator('.reader__hint').isVisible(), 'first-entry hint did not show');
  await page.locator('.reader__hint-ok').click();
  assert((await page.locator('.reader__hint').count()) === 0, 'first-entry hint did not dismiss');
  assert(
    (await page.evaluate(() => localStorage.getItem('zreader.reader.hinted'))) === '1',
    'first-entry hint was not remembered',
  );

  await page.locator('.reader__top button.reader__icon-btn').nth(0).click();
  assert(await page.locator('.reader-search').isVisible(), 'search drawer did not open');
  await page.locator('.reader-search input').fill('FreshNeedle');
  // The reparsed cache can lag the reparse response by a beat, so the first
  // query occasionally returns zero hits; retry a few times before failing so
  // the gate is deterministic. A genuine miss still fails after the attempts.
  let searchHits = 0;
  for (let attempt = 0; attempt < 5 && searchHits === 0; attempt += 1) {
    await Promise.all([
      page.waitForResponse((r) => r.url().includes('/api/v1/books/') && r.url().includes('/search') && r.status() === 200),
      page.locator('.reader-search button').click(),
    ]);
    try {
      await page.waitForSelector('.search-results li', { timeout: 2_000 });
    } catch {
      await page.waitForTimeout(500);
    }
    searchHits = await page.locator('.search-results li').count();
  }
  assert(searchHits > 0, 'search did not return the reparsed text');
  await page.keyboard.press('Escape');

  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/books/') && r.url().includes('/bookmarks') && r.status() === 201),
    page.locator('.reader__top button.reader__icon-btn').nth(1).click(),
  ]);
  await page.waitForSelector('.bookmark-list li');
  assert((await page.locator('.bookmark-list li').count()) === 1, 'bookmark was not created');
  await page.keyboard.press('Escape');

  // Settings now lives in the bottom bar (TOC=0, bookmarks=1, settings=2).
  await page.locator('.reader__bottom button.reader__icon-btn').nth(2).click();
  await page.locator('.settings__seg').nth(0).locator('button').nth(0).click();
  assert(await page.locator('.reader.reader--line-compact').count() === 1, 'line-height setting did not apply');
  // P2: reset now sits at the bottom of the drawer and reverts the change.
  await page.locator('.settings__reset').click();
  assert(
    (await page.locator('.reader.reader--line-compact').count()) === 0,
    'reset did not revert the line-height setting',
  );
  await page.keyboard.press('Escape');

  await page.setViewportSize({ width: 390, height: 720 });
  const beforeTap = await page.locator('.reader__content').evaluate((el) => el.scrollTop);
  const box = await page.locator('.reader__content').boundingBox();
  assert(box, 'reader content box missing');
  await page.mouse.click(box.x + box.width * 0.86, box.y + box.height * 0.5);
  await page.waitForFunction(
    (oldTop) => document.querySelector('.reader__content').scrollTop > oldTop,
    beforeTap,
  );

  await page.goto(baseURL, { waitUntil: 'networkidle' });
  await page.waitForSelector('.book-row');
  // The AI export: rules on by default, live preview, and a download link
  // carrying the chosen rules. Toggling a rule must actually re-run the
  // export server-side rather than only relabelling the button.
  await openRowMenu(page, 0);
  await page.locator('.shelf__menu-pop button', { hasText: '导出' }).click();
  await page.waitForSelector('.export__stats');
  assert(
    (await page.locator('.export__rule input:checked').count()) === 4,
    'export should default to every cleaning rule on',
  );
  // With every rule on, the planted promo line must be gone from the output.
  const cleanedHref = await page.locator('.dlg__actions a').getAttribute('href');
  const cleaned = await page.evaluate(async (href) => (await fetch(href)).text(), cleanedHref);
  const cleanedLines = cleaned.trim().split('\n');
  assert(
    cleanedLines.length > 0 && cleanedLines.every((l) => JSON.parse(l).text !== undefined),
    'export did not return JSONL chunks',
  );
  assert(!cleaned.includes('example-invalid.test'), 'the promo line survived a cleaned export');

  // Turning the rule off must actually re-run the export server-side, not
  // just relabel the download link — so the same line comes back.
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/export?preview=1') && r.url().includes('promo=0')),
    page.locator('.export__rule input').first().uncheck(),
  ]);
  const rawHref = await page.locator('.dlg__actions a').getAttribute('href');
  assert(/promo=0/.test(rawHref), `export link did not follow the toggle: ${rawHref}`);
  const raw = await page.evaluate(async (href) => (await fetch(href)).text(), rawHref);
  assert(raw.includes('example-invalid.test'), 'disabling the promo rule did not restore the line');
  await page.keyboard.press('Escape');

  // Delete is a dialog now, not a window.confirm, and it defaults to
  // record-only: the request must carry source=false unless the box is
  // ticked. That default is the whole point of the dialog, so gate on it.
  await openRowMenu(page, 0);
  await page.locator('.shelf__menu-pop button').nth(3).click();
  await page.waitForSelector('.dlg__panel');
  assert(
    !(await page.locator('.confirm-check input').isChecked()),
    'delete dialog must not default to removing the source file',
  );
  const [deleteRequest] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/api/v1/books/') && r.method() === 'DELETE'),
    page.waitForResponse((r) => r.url().includes('/api/v1/books/') && r.request().method() === 'DELETE' && r.status() === 204),
    page.locator('.dlg__actions .shelf__btn--danger').click(),
  ]);
  assert(
    /source=false/.test(deleteRequest.url()),
    `delete should default to record-only, requested ${deleteRequest.url()}`,
  );
  await page.waitForFunction(() => document.querySelectorAll('.book-row').length === 1);

  await page.setViewportSize({ width: 1280, height: 900 });
  const imagePDFPath = path.join(tempRoot, 'ImageOnly - AuthorX.pdf');
  await writeFile(imagePDFPath, simplePDFBytes('ImageOnly', 'AuthorX'));
  await page.getByRole('button', { name: '添加书籍' }).click();
  await page.locator('.dlg input[type=file]').setInputFiles(imagePDFPath);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/library/upload') && r.status() === 201),
    page.locator('.dlg__actions .shelf__btn--primary').click(),
  ]);
  await page.waitForFunction(() => document.querySelectorAll('.book-row').length === 2);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/books/') && r.url().includes('/source') && r.status() === 200),
    page.locator('.book-row').first().locator('a.book-row__link').click(),
  ]);
  await page.waitForSelector('.reader__pdf-frame');
  assert((await page.locator('.reader.reader--pdf').count()) === 1, 'pdf-image reader did not open');
  assert(await page.getByText(/1\s*\/\s*1/).isVisible(), 'pdf page counter missing');

  // Signing out must actually close the gate, not just clear the UI. The
  // account menu lives in the shelf header, and the PDF assertions above
  // left us in the reader.
  await page.goto(baseURL, { waitUntil: 'networkidle' });
  await page.waitForSelector('.shelf');
  await page.getByRole('button', { name: /^账号：/ }).click();
  await page.locator('.shelf__menu-pop button', { hasText: '退出登录' }).click();
  await page.waitForSelector('.auth__card');
  const afterLogout = await page.evaluate(async () => (await fetch('/api/v1/books')).status);
  assert(afterLogout === 401, `GET /books after logout = ${afterLogout}, want 401`);

  console.log('v0.8 e2e smoke passed');
} finally {
  if (browser) await browser.close();
  await stopServer(server);
  await rm(tempRoot, { recursive: true, force: true });
}
