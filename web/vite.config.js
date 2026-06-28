import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Build to web/dist with relative asset paths so the Go binary can serve the
// embedded SPA from any mount point. The dev server proxies the API + SSE to the
// running daemon on :7777.
export default defineConfig({
  plugins: [react()],
  base: './',
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:7777',
      '/events': { target: 'http://127.0.0.1:7777', ws: false },
      '/health': 'http://127.0.0.1:7777',
    },
  },
})
