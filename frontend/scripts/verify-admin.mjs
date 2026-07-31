import { build } from 'esbuild'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const outfile = join(tmpdir(), 'dreamtrans-admin-verification.mjs')

await build({
  entryPoints: ['src/admin/verification.ts'],
  bundle: true,
  platform: 'node',
  format: 'esm',
  define: {
    'import.meta.env.VITE_BACKEND_URL': JSON.stringify('/'),
  },
  outfile,
})

await import(`${pathToFileURL(outfile).href}?verified=${Date.now()}`)
