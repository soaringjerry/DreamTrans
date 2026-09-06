import { expect, test } from '@playwright/test'

test('super admin reviews signup rewards and can adjust thresholds', async ({ page }) => {
  const encode = (value: object) => Buffer.from(JSON.stringify(value)).toString('base64url')
  const token = `${encode({ alg: 'none' })}.${encode({ exp: Math.floor(Date.now() / 1000) + 3600 })}.e2e`
  await page.addInitScript((access) => localStorage.setItem('dt_access_token', access), token)
  const user = { id: 'admin-1', role: 'super_admin', name: 'Admin', email: 'admin@example.test' }
  let settings = { enabled: true, device_limit: 1, network_daily_limit: 5, automatic_daily_limit: 100 }
  let profile = { id: 'risk-1', user_id: 'user-1', name: '学生', email: 'student@example.test', verified: true, decision: 'review', reasons: ['device_accounts'], device_count: 1, network_count: 2, daily_count: 3, rules: settings, created_at: '2026-09-06T01:00:00Z', reviewed_at: null, review_note: '', promotion: '开学季', channel: '校园社团' }
  let reviewBody: unknown
  await page.route(/^https?:\/\/[^/]+\/api\//, async (route) => {
    const url = new URL(route.request().url())
    const method = route.request().method()
    let data: unknown = {}
    if (url.pathname === '/api/user/profile') data = { user }
    if (url.pathname === '/api/admin/stats') data = { basic: { user_count: 1, tenant_count: 1, session_count: 0, transcript_count: 0 } }
    if (url.pathname === '/api/admin/signup-risk/settings') {
      if (method === 'PUT') settings = route.request().postDataJSON()
      data = settings
    }
    if (url.pathname === '/api/admin/signup-risk') {
      const profiles = profile.decision === url.searchParams.get('decision') ? [profile] : []
      data = { profiles, total: profiles.length }
    }
    if (url.pathname === '/api/admin/signup-risk/risk-1' && method === 'POST') {
      reviewBody = route.request().postDataJSON()
      profile = { ...profile, ...route.request().postDataJSON() }
      data = { ok: true }
    }
    if (url.pathname === '/api/admin/signup-risk/risk-1' && method === 'GET') data = { audit: [] }
    await route.fulfill({ json: data })
  })
  await page.goto('/pro/admin')
  await page.getByRole('button', { name: '注册风控', exact: true }).click()
  await expect(page.getByRole('cell', { name: /同一浏览器重复注册/ })).toBeVisible()
  await page.getByRole('button', { name: '查看 / 审核' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('button', { name: '放行赠送权益' })).toBeDisabled()
  await dialog.getByLabel('审核备注', { exact: true }).fill('已核实为社团共用电脑，不同学生')
  await dialog.getByRole('button', { name: '放行赠送权益' }).click()
  await expect(dialog).toHaveCount(0)
  expect(reviewBody).toEqual({ decision: 'approved', note: '已核实为社团共用电脑，不同学生' })
  await expect(page.getByText('暂无匹配记录')).toBeVisible()
  await page.getByText('风控规则与阈值', { exact: true }).click()
  await page.getByLabel('同一网络 24 小时注册数阈值').fill('10')
  await page.getByRole('button', { name: '保存风控设置' }).click()
  await expect(page.getByText('风控设置已保存；已待审账号仍需单独审核')).toBeVisible()
  expect(settings.network_daily_limit).toBe(10)
})
