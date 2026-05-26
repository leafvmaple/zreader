import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'node:path';

// Build output goes directly into the Go backend's webui package so the
// resulting binary can embed it via go:embed. The dev backend (started with
// `go run ./cmd/zreader` from backend/) listens on :8080 — the Vite dev
// proxy below mirrors that.
const distOutDir = path.resolve(__dirname, '../backend/internal/webui/dist');

export default defineConfig({
  plugins: [react()],
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
