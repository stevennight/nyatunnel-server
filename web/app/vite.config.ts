import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: Number(process.env.PORT) || 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8080',
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
    globals: true,
  },
  build: {
    // The Go server serves this directory (NYATUNNEL_WEB_DIR, default .tmp-webdist); the Docker image copies it to /app/.tmp-webdist.
    outDir: '../../.tmp-webdist',
    emptyOutDir: true,
  },
})
