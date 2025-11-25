import { defineConfig } from 'vite'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import vue from '@vitejs/plugin-vue'

const __dirname = dirname(fileURLToPath(import.meta.url))

// https://vite.dev/config/
export default defineConfig({
  // Support both React (existing app) and Vue (Pro UI micro-app)
  plugins: [react(), vue()],
  build: {
    rollupOptions: {
      input: {
        main: resolve(__dirname, 'index.html'),
        pro: resolve(__dirname, 'pro.html'),
        'pro-admin': resolve(__dirname, 'pro-admin.html'),
      },
    },
  },
})
