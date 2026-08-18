import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Build to web/dist with relative asset paths so the Go binary can serve the
// embedded SPA from any mount point. The dev server proxies the API + SSE to a
// running daemon — :7777 by default, or wherever AXON_DASHBOARD_PORT points, so
// `npm run dev` can be aimed at a scratch profile instead of the real one.
const target = `http://127.0.0.1:${process.env.AXON_DASHBOARD_PORT || 7777}`
export default defineConfig({
  plugins: [react()],
  base: './',
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    proxy: {
      '/api': target,
      '/events': { target, ws: false },
      '/health': target,
    },
  },
})
