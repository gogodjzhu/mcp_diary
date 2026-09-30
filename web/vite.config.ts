import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// The build output is embedded into the Go binary by internal/webui, so the
// dist directory lives inside the Go module tree.
export default defineConfig({
  plugins: [vue()],
  base: '/',
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    // Stable file names keep the committed embed output diff-friendly.
    rollupOptions: {
      output: {
        entryFileNames: 'assets/app.js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name].[ext]',
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/mcp': 'http://localhost:8080',
      '/oauth': 'http://localhost:8080',
      '/.well-known': 'http://localhost:8080',
    },
  },
})
