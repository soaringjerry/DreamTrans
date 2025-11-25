import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import vue from '@vitejs/plugin-vue'

// https://vite.dev/config/
export default defineConfig({
  // Support both React (existing app) and Vue (Pro UI micro-app)
  plugins: [react(), vue()],
})
