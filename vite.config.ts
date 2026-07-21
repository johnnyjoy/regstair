import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [react()],
  root: 'frontend',
  build: {
    outDir: '../internal/admin/frontend-dist',
    emptyOutDir: true,
    manifest: true,
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
    css: true,
  },
})
