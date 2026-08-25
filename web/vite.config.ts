import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Fully self-contained SPA (no external CDN). Vendor code is split so browsers
// cache it across deploys; route code is split via React.lazy in the app.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2020',
    chunkSizeWarningLimit: 1200,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules')) {
            if (id.includes('echarts') || id.includes('zrender')) return 'echarts'
            if (id.includes('@mui') || id.includes('@emotion')) return 'mui'
            if (id.includes('react-router')) return 'router'
            if (id.includes('react') || id.includes('scheduler')) return 'react'
            return 'vendor'
          }
        },
      },
    },
  },
  server: {
    proxy: {
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: true, ws: true },
    },
  },
})
