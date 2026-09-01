// Builds the extension for Chromium (Chrome / Edge) and Firefox into
// dist/chrome and dist/firefox. Same code, two manifests: Chrome wants a
// service-worker background, Firefox wants background scripts.
import { build, context } from 'esbuild'
import { cpSync, mkdirSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs'
import { execSync } from 'node:child_process'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = dirname(fileURLToPath(import.meta.url))
const watch = process.argv.includes('--watch')
const zip = process.argv.includes('--zip')
const targets = ['chrome', 'firefox']

const entries = [
  { in: 'src/content/moodle.ts', out: 'content/moodle', format: 'iife' },
  { in: 'src/background.ts', out: 'background', format: 'esm' },
  { in: 'src/popup/popup.ts', out: 'popup/popup', format: 'iife' },
]

function manifestFor(target) {
  const base = JSON.parse(readFileSync(join(root, 'manifest.json'), 'utf8'))
  if (target === 'firefox') {
    base.background = { scripts: ['background.js'], type: 'module' }
    base.browser_specific_settings = {
      gecko: { id: 'moodle-sync@dreamtrans.app', strict_min_version: '128.0' },
    }
  }
  return JSON.stringify(base, null, 2)
}

function copyStatic(outDir) {
  mkdirSync(join(outDir, 'popup'), { recursive: true })
  mkdirSync(join(outDir, 'icons'), { recursive: true })
  cpSync(join(root, 'src/popup/popup.html'), join(outDir, 'popup/popup.html'))
  cpSync(join(root, 'src/popup/popup.css'), join(outDir, 'popup/popup.css'))
  cpSync(join(root, 'icons'), join(outDir, 'icons'), { recursive: true })
}

async function buildTarget(target) {
  const outDir = join(root, 'dist', target)
  rmSync(outDir, { recursive: true, force: true })
  mkdirSync(outDir, { recursive: true })
  copyStatic(outDir)
  writeFileSync(join(outDir, 'manifest.json'), manifestFor(target))
  const options = (entry) => ({
    entryPoints: [join(root, entry.in)],
    outfile: join(outDir, `${entry.out}.js`),
    bundle: true,
    format: entry.format,
    target: ['chrome120', 'firefox128'],
    platform: 'browser',
    sourcemap: false,
    minify: !watch,
    legalComments: 'none',
    define: { 'process.env.NODE_ENV': '"production"' },
    logLevel: 'info',
  })
  if (watch) {
    for (const entry of entries) {
      const ctx = await context(options(entry))
      await ctx.watch()
    }
    return
  }
  for (const entry of entries) {
    await build(options(entry))
  }
  if (zip) {
    const zipPath = join(root, 'dist', `dreamtrans-moodle-sync-${target}.zip`)
    if (existsSync(zipPath)) rmSync(zipPath)
    execSync(`cd "${outDir}" && zip -qr "${zipPath}" .`)
    console.log(`packaged ${zipPath}`)
  }
}

for (const target of targets) {
  await buildTarget(target)
}
if (watch) console.log('watching…')
