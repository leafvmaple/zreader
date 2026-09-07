import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import fs from 'node:fs';
import path from 'node:path';

// Build output goes directly into the Go backend's webui package so the
// resulting binary can embed it via go:embed. The dev backend (started with
// `go run ./cmd/zreader` from backend/) listens on :8080 — the Vite dev
// proxy below mirrors that.
const distOutDir = path.resolve(__dirname, '../backend/internal/webui/dist');

// `go:embed all:dist` fails to compile when dist/ has no files at all, so a
// tracked .gitkeep keeps a fresh clone buildable before anyone runs a
// frontend build. emptyOutDir deletes it on every build, which meant the
// placeholder kept disappearing from commits and breaking CI's backend
// tests — they run before the frontend build. Put it back afterwards.
const keepDistPlaceholder = {
  name: 'zreader:keep-dist-placeholder',
  closeBundle() {
    fs.mkdirSync(distOutDir, { recursive: true });
    fs.writeFileSync(path.join(distOutDir, '.gitkeep'), '');
  },
};

export default defineConfig({
  plugins: [react(), keepDistPlaceholder],
  build: {
    outDir: distOutDir,
    emptyOutDir: true,
    rollupOptions: {
      output: {
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash][extname]',
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
    },
  },
});
