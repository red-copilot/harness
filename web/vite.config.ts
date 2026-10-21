import { fileURLToPath, URL } from 'node:url'
import { rmSync } from 'node:fs'
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

const outDir = fileURLToPath(new URL('../webui/dist', import.meta.url))
const proxyTarget = process.env.VITE_PROXY_TARGET ?? 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [
    react(),
    {
      name: 'preserve-webui-placeholder',
      apply: 'build',
      buildStart() {
        rmSync(`${outDir}/assets`, { recursive: true, force: true })
        rmSync(`${outDir}/index.html`, { force: true })
      },
    },
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir,
    emptyOutDir: false,
  },
  server: {
    proxy: {
      '/v1': { target: proxyTarget, changeOrigin: false },
      '/healthz': { target: proxyTarget, changeOrigin: false },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: true,
    globals: true,
    testTimeout: 15000,
    hookTimeout: 15000,
    server: {
      deps: {
        inline: ['@ant-design/colors', '@ant-design/icons'],
      },
    },
  },
})
