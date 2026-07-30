import path from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, './src') },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // The server sends `script-src 'self'`, and Vite's modulepreload polyfill is
    // an inline module script. Every browser we care about supports modulepreload
    // natively, so drop the polyfill rather than loosening the CSP.
    modulePreload: { polyfill: false },
    // `default-src 'self'` blocks data: URIs, so no asset may be base64-inlined.
    assetsInlineLimit: 0,
    sourcemap: false,
    rollupOptions: {
      output: {
        // dist/ is committed. Stable filenames keep a UI change to a real diff on
        // a handful of paths instead of adding and removing hashed files every
        // time. The static handler sets no long-lived Cache-Control, so this
        // costs nothing in practice.
        entryFileNames: 'assets/[name].js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name][extname]',
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      // changeOrigin stays false so the Host header keeps pointing at localhost
      // and the session cookie is accepted as same-origin.
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false },
    },
  },
})
