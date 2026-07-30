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

function installProAdminFallback(server: MiddlewareServer): void {
  server.middlewares.use((request, _response, next) => {
    if (request.url && /^\/pro\/admin(?:\/|\\?|$)/.test(request.url)) {
      const query = request.url.indexOf('?')
      request.url = `/pro-admin.html${query >= 0 ? request.url.slice(query) : ''}`
    }
    next()
  })
}

const proAdminHistoryFallback = {
  name: 'dreamtrans-pro-admin-history-fallback',
  configureServer(server: MiddlewareServer) {
    installProAdminFallback(server)
  },
  configurePreviewServer(server: MiddlewareServer) {
    installProAdminFallback(server)
  },
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [proAdminHistoryFallback, react()],
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
