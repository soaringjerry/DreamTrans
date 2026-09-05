import { test, expect } from '@playwright/test'
import { buildSync } from 'esbuild'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const frontendDir = fileURLToPath(new URL('../', import.meta.url))
const bundle = buildSync({
  stdin: {
    resolveDir: frontendDir,
    contents: `
      import React from 'react';
      import {createRoot} from 'react-dom/client';
      import {flushSync} from 'react-dom';
      import Markdown from './src/components/MarkdownView';
      import {projectOptions, reportContents} from '../extension/src/popup/safeDom';
      const payload = '</option><style>body{display:none}</style><img src=x onerror=alert(1)>';
      const select = document.createElement('select'); document.body.append(select);
      projectOptions(select, 'choose', [{id: payload, name: payload}]);
      const report = document.createElement('section'); document.body.append(report);
      reportContents(report, {checks:[{label:payload, detail:payload, ok:false}], modtypes:{[payload]:payload}});
      const host = document.createElement('main'); document.body.append(host);
      flushSync(() => createRoot(host).render(React.createElement(Markdown, {text:
        '<style>body{display:none}</style>\\n<img src=x onerror=alert(1)>\\n' +
        '[badjs](javascript:alert%281%29)\\n[baddata](data:text/html,hello)\\n' +
        '[badfile](file:///etc/passwd)\\n[badblob](blob:https://example.com/id)\\n' +
        '[badtab](java\\tscript:alert%281%29)\\n' +
        '[web](https://example.com)\\n[mail](mailto:user@example.com)\\n[local](/pro)'
      })));
    `,
  },
  bundle: true, platform: 'browser', format: 'iife', write: false,
}).outputFiles[0].text

test('untrusted Markdown and extension responses remain inert text', async ({ page }) => {
  await page.setContent('<body></body>')
  await page.addScriptTag({ content: bundle })
  await expect(page.locator('main a')).toHaveCount(3)
  await expect(page.locator('style, img')).toHaveCount(0)
  await expect(page.locator('select option')).toHaveCount(2)
  await expect(page.locator('select option').nth(1)).toHaveAttribute('value', /<style>/)
  await expect(page.locator('section')).toContainText('<img')
  await expect(page.locator('body')).toBeVisible()
})

const policySource = readFileSync(new URL('../../backend/cmd/web/security_headers.go', import.meta.url), 'utf8')
const policy = JSON.parse(policySource.match(/const contentSecurityPolicy = ("[^\n]+")/)![1]) as string

for (const enforce of [false, true]) {
  test(`CSP ${enforce ? 'enforces' : 'reports'} inline script violations and permits workers`, async ({ page }) => {
    await page.route('http://security.test/**', async (route) => {
      if (route.request().url().endsWith('/app.js')) {
        await route.fulfill({ contentType: 'text/javascript', body: `
          window.externalRan = true;
          const worker = new Worker(URL.createObjectURL(new Blob(['postMessage("ok")'], {type:'text/javascript'})));
          worker.onmessage = () => { window.workerRan = true; worker.terminate(); };
        ` })
        return
      }
      if (route.request().method() === 'POST') { await route.fulfill({ status: 204 }); return }
      await route.fulfill({ contentType: 'text/html', headers: {
        [enforce ? 'Content-Security-Policy' : 'Content-Security-Policy-Report-Only']: policy,
      }, body: '<body><script>window.inlineRan=true</script><script src="/app.js"></script></body>' })
    })
    await page.goto('http://security.test/')
    await expect.poll(() => page.evaluate('window.workerRan')).toBe(true)
    expect(await page.evaluate('window.externalRan')).toBe(true)
    expect(await page.evaluate('!!window.inlineRan')).toBe(!enforce)
  })
}
