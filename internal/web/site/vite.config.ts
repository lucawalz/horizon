import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const dashboardPort = 8973

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  server: {
    host: '127.0.0.1',
    proxy: {
      // the host is forwarded as the browser sent it, so it still matches the origin the mutation guard compares it against
      '/api': {
        target: `http://127.0.0.1:${dashboardPort}`,
      },
    },
  },
})
