import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    host: '127.0.0.1',
    port: 9245,
    strictPort: true,
    proxy: {
      '/ws': { target: 'ws://127.0.0.1:18981', ws: true, changeOrigin: true },
    },
  },
})
