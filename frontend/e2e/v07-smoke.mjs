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

function longBookText(needle) {
  const paragraphs = [];
  for (let i = 0; i < 80; i += 1) {
    paragraphs.push(`Paragraph ${i}. Alpha beta gamma delta epsilon zeta eta theta.`);
  }
  paragraphs.splice(20, 0, `This paragraph contains ${needle} for search.`);
  return `Chapter 1\n\n${paragraphs.join('\n\n')}\n\nChapter 2\n\nTail paragraph.`;
}

if (!existsSync(distIndex)) {
  throw new Error('frontend dist is missing; run pnpm build before this e2e test');
}

const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'zreader-v07-e2e-'));
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
  assert(await page.getByRole('button', { name: '添加书籍' }).isVisible(), 'add-book button missing');

  const uploadPath = path.join(tempRoot, 'BookA - AuthorX.txt');
  await writeFile(uploadPath, longBookText('OldNeedle'), 'utf8');
  await page.getByRole('button', { name: '添加书籍' }).click();
  assert(await page.locator('.upload-dialog').isVisible(), 'upload dialog did not open');
  await page.locator('.upload-dialog input[type=file]').setInputFiles(uploadPath);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/library/upload') && r.status() === 201),
    page.locator('.upload-dialog__actions .shelf__btn--primary').click(),
  ]);
  await page.waitForSelector('.book-row');
  assert((await page.locator('.book-row').count()) === 1, 'uploaded book row missing');
  assert((await page.locator('.book-row').first().locator('.book-row__action').count()) === 4, 'book row actions missing');

  const secondUploadPath = path.join(tempRoot, 'BookB - AuthorY.txt');
  await writeFile(secondUploadPath, longBookText('OldNeedle'), 'utf8');
  await page.getByRole('button', { name: '添加书籍' }).click();
  await page.locator('.upload-dialog input[type=file]').setInputFiles(secondUploadPath);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/library/upload') && r.status() === 201),
    page.locator('.upload-dialog__actions .shelf__btn--primary').click(),
  ]);
  await page.waitForFunction(() => document.querySelectorAll('.book-row').length === 2);

  await page.getByRole('button', { name: /重复/ }).click();
  await page.waitForSelector('.duplicate-list li');
  assert((await page.locator('.duplicate-list li').count()) === 1, 'duplicate panel did not show duplicate group');
  await page.locator('.library-panel__close').click();

  await page.locator('.book-row').first().locator('.book-row__action').nth(0).click();
  await page.locator('.edit-dialog input').nth(0).fill('EditedTitle');
  await page.locator('.edit-dialog input').nth(1).fill('EditedAuthor');
  await page.locator('.edit-dialog input').nth(2).fill('CategoryA');
  await page.locator('.edit-dialog input').nth(3).fill('TagA TagB');
  await page.locator('.edit-dialog select').selectOption('reading');
  await page.locator('.edit-dialog__check input').check();
  await page.locator('.edit-dialog textarea').fill('Short description');
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/books/') && r.request().method() === 'PATCH' && r.status() === 200),
    page.locator('.edit-dialog .shelf__btn--primary').click(),
  ]);
  await page.waitForFunction(() => document.body.textContent.includes('EditedTitle'));
  assert(await page.locator('.book-row__tags span', { hasText: 'TagA' }).count() > 0, 'edited tags not visible');

  await page.locator('.book-row__select input').nth(0).check();
  await page.locator('.book-row__select input').nth(1).check();
  await page.locator('.batch-bar input').fill('BatchTag');
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/books/batch') && r.status() === 200),
    page.locator('.batch-bar button', { hasText: '打标签' }).click(),
  ]);
  await page.waitForFunction(() => document.body.textContent.includes('BatchTag'));

  await page.getByRole('button', { name: '任务' }).click();
  await page.waitForSelector('.job-list li');
  assert((await page.locator('.job-list li').count()) > 0, 'job history did not open');
  await page.locator('.library-panel__close').click();

  await writeFile(path.join(libraryDir, 'BookA - AuthorX.txt'), longBookText('FreshNeedle'), 'utf8');
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/books/') && r.url().includes('/reparse') && r.status() === 200),
    page.locator('.book-row').first().locator('.book-row__action').nth(2).click(),
  ]);

  await page.locator('.book-row').first().locator('a.book-row__link').click();
  await page.waitForURL(/\/read\/\d+/, { timeout: 10_000 });
  await page.waitForSelector('.reader__article');
  assert((await page.locator('.reader__error').count()) === 0, 'reader loaded with an error');

  await page.locator('.reader__top button.reader__icon-btn').nth(0).click();
  assert(await page.locator('.reader-search').isVisible(), 'search drawer did not open');
  await page.locator('.reader-search input').fill('FreshNeedle');
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/books/') && r.url().includes('/search') && r.status() === 200),
    page.locator('.reader-search button').click(),
  ]);
  await page.waitForSelector('.search-results li');
  assert((await page.locator('.search-results li').count()) > 0, 'search did not return the reparsed text');
  await page.keyboard.press('Escape');

  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/books/') && r.url().includes('/bookmarks') && r.status() === 201),
    page.locator('.reader__top button.reader__icon-btn').nth(1).click(),
  ]);
  await page.waitForSelector('.bookmark-list li');
  assert((await page.locator('.bookmark-list li').count()) === 1, 'bookmark was not created');
  await page.keyboard.press('Escape');

  await page.locator('.reader__top button.reader__icon-btn').nth(2).click();
  await page.locator('.settings__seg').nth(0).locator('button').nth(0).click();
  assert(await page.locator('.reader.reader--line-compact').count() === 1, 'line-height setting did not apply');
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
  page.once('dialog', (dialog) => dialog.accept());
  await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/books/') && r.request().method() === 'DELETE' && r.status() === 204),
    page.locator('.book-row').first().locator('.book-row__action').nth(3).click(),
  ]);
  await page.waitForFunction(() => document.querySelectorAll('.book-row').length === 1);

  console.log('v0.7 e2e smoke passed');
} finally {
  if (browser) await browser.close();
  await stopServer(server);
  await rm(tempRoot, { recursive: true, force: true });
}
