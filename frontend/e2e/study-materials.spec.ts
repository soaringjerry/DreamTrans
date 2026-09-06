import { test, expect } from '@playwright/test'

test('material extraction marks the route stale without starting paid generation', async ({ page }) => {
  const project = { id: '11111111-1111-4111-8111-111111111111', name: 'Materials course', context_mode: 'smart', max_context_tokens: 64000, description: '' }
  const user = { id: 'study-user', tenant_id: 'tenant', email: 'study@example.test', name: 'Study', role: 'user', is_active: true }
  const token = `e2e.${Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 })).toString('base64url')}.e2e`
  await page.addInitScript(({ user, token }) => {
    localStorage.setItem('dt_user', JSON.stringify(user))
    localStorage.setItem('dt_access_token', token)
    localStorage.setItem('dt_refresh_token', 'test-refresh')
  }, { user, token })
  let uploaded = false
  let pending = false
  let stale = false
  let generations = 0
  const source = { id: 'source-1', name: 'textbook.txt', source_type: 'file', chunk_count: 1, size_bytes: 50 }
  await page.route(/^https?:\/\/[^/]+\/api\//, async (route) => {
    const path = new URL(route.request().url()).pathname
    const post = route.request().method() === 'POST'
    let body: unknown = {}
    if (path === '/api/system/access') body = { authentication_enabled: true, rag_enabled: true }
    else if (path === '/api/user/profile') body = { user }
    else if (path === '/api/user/balance') body = { available_usd: 20 }
    else if (path === '/api/ai/projects') body = { projects: [project] }
    else if (path.endsWith('/sources')) {
      if (post) { uploaded = true; pending = true; stale = true; body = { source: { ...source, status: 'queued' } } }
      else body = { sources: uploaded ? [{ ...source, status: pending ? 'processing' : 'ready' }] : [] }
    } else if (path.endsWith('/sessions')) body = { sessions: [] }
    else if (path.endsWith('/skill-map')) {
      if (post) { generations++; stale = false }
      body = {
        artifact: { id: `map-${generations}` }, stale, materials_pending: pending,
        map: { version: 1, generated_at: new Date().toISOString(), session_count: 0, source_count: uploaded ? 1 : 0,
          skills: generations === 0
            ? [{ id: 's1', label: 'Old structure', prerequisites: [] }]
            : [
              { id: 's1', label: 'Understand definitions', prerequisites: [] },
              { id: 's2', label: 'Apply definitions', prerequisites: ['s1'] },
              { id: 's3', label: 'Compare definitions', prerequisites: ['s1'] },
            ] },
      }
    } else if (path.endsWith('/study/state')) body = { states: [], continue: null }
    else if (path.endsWith('/study/weeks')) body = { weeks: [], behind_weeks: [], current_week: 1, unassigned: { sources: [], sessions: [], skills: [] } }
    else if (path.endsWith('/study/costs')) body = { items: [], summary: { total_usd: 0, by_feature: {} } }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) })
  })
  await page.goto('/pro/study')
  await page.getByRole('button', { name: /Materials course/ }).click()
  await expect(page.locator('.dt-route__node')).toHaveCount(1)
  await expect(page.locator('.dt-route__edges g')).toHaveCount(0)
  await page.getByRole('button', { name: '课程管理', exact: true }).click()
  await page.getByLabel('上传课程资料', { exact: true }).setInputFiles({ name: 'textbook.txt', mimeType: 'text/plain', buffer: Buffer.from('A course-specific definition.') })
  await expect(page.getByRole('status')).toContainText('仍在提取')
  await expect(page.getByRole('button', { name: '重新生成', exact: true })).toBeDisabled()
  expect(generations).toBe(0)
  pending = false
  await expect(page.getByRole('status')).toContainText('需要更新', { timeout: 15000 })
  const regenerate = page.getByRole('button', { name: '重新生成', exact: true })
  await expect(regenerate).toBeEnabled()
  page.once('dialog', async (dialog) => { expect(dialog.message()).toContain('扣除'); await dialog.dismiss() })
  await regenerate.click()
  expect(generations).toBe(0)
  page.once('dialog', async (dialog) => { await dialog.accept() })
  await regenerate.click()
  await expect.poll(() => generations).toBe(1)
  await expect(page.getByRole('status')).toHaveCount(0)
  await page.locator('.dt-study__tabs button', { hasText: '今日行动' }).click()
  await expect(page.locator('.dt-route__node')).toHaveCount(3)
  await expect(page.locator('.dt-route__node')).not.toContainText(['Old structure'])
  await expect(page.locator('.dt-route__edges g')).toHaveCount(2)
  await expect(page.locator('.dt-route__edges g[data-from="s1"][data-to="s2"]')).toHaveCount(1)
  await expect(page.locator('.dt-route__edges g[data-from="s1"][data-to="s3"]')).toHaveCount(1)
})
