import { defineConfig } from 'vite'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'

const __dirname = dirname(fileURLToPath(import.meta.url))

type MiddlewareServer = {
  middlewares: {
    use: (middleware: (
      request: { url?: string },
      response: unknown,
      next: () => void,
    ) => void) => void
  }
}

function installProFallbacks(server: MiddlewareServer): void {
  server.middlewares.use((request, _response, next) => {
    if (request.url) {
      const query = request.url.indexOf('?')
      const suffix = query >= 0 ? request.url.slice(query) : ''
      if (/^\/pro\/admin(?:\/|\?|$)/.test(request.url)) {
        request.url = `/pro-admin.html${suffix}`
      } else if (/^\/pro\/study(?:\/|\?|$)/.test(request.url)) {
        request.url = `/study.html${suffix}`
      } else if (/^\/pro(?:\/|\?|$)/.test(request.url)) {
        request.url = `/pro.html${suffix}`
      }
    }
    next()
  })
}

const proHistoryFallback = {
  name: 'dreamtrans-pro-history-fallback',
  configureServer(server: MiddlewareServer) {
    installProFallbacks(server)
  },
  configurePreviewServer(server: MiddlewareServer) {
    installProFallbacks(server)
  },
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [proHistoryFallback, react()],
  build: {
    rollupOptions: {
      input: {
        main: resolve(__dirname, 'index.html'),
        pro: resolve(__dirname, 'pro.html'),
        'pro-admin': resolve(__dirname, 'pro-admin.html'),
        study: resolve(__dirname, 'study.html'),
      },
    },
  },
})
