import { expect, test } from '@playwright/test'

test('administrator creates, shares, inspects and pauses a channel invitation', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const encode = (value: object) => Buffer.from(JSON.stringify(value)).toString('base64url')
  const token = `${encode({ alg: 'none' })}.${encode({ exp: Math.floor(Date.now() / 1000) + 3600, sub: 'admin-1' })}.e2e`
  await page.addInitScript((access) => localStorage.setItem('dt_access_token', access), token)
  const user = { id: 'admin-1', tenant_id: 'tenant-1', email: 'admin@example.test', name: 'Admin', role: 'super_admin', is_active: true, email_verified: true }
  let offer: Record<string, unknown> | null = null
  await page.route(/^https?:\/\/[^/]+\/api\//, async (route) => {
    const url = new URL(route.request().url())
    const method = route.request().method()
    let data: unknown = {}
    if (url.pathname === '/api/admin/stats') data = { basic: { user_count: 1, tenant_count: 1, session_count: 0, transcript_count: 0 } }
    if (url.pathname === '/api/user/profile') data = { user }
    if (url.pathname === '/api/admin/billing/plans') data = { plans: [{ code: 'pro', name: 'Pro', active: true }] }
    if (url.pathname === '/api/admin/promotions') {
      if (method === 'POST') {
        offer = { ...route.request().postDataJSON(), id: 'invite-1', enabled: true, registrations: 0, verified: 0, rewarded: 0 }
        data = offer
      } else data = { invites: offer ? [offer] : [], total: offer ? 1 : 0 }
    }
    if (url.pathname === '/api/admin/promotions/invite-1') {
      if (method === 'PATCH') { offer = { ...offer, ...route.request().postDataJSON() }; data = { ok: true } }
      else data = { registrations: [{ id: 'receipt-1', user_id: 'user-1', email: 'student@example.test', name: '学生昵称', verified: true, registered_at: '2026-09-06T01:00:00Z', rewarded_at: '2026-09-06T01:05:00Z', plan_until: '2026-10-06T01:05:00Z' }], total: 1 }
    }
    await route.fulfill({ json: data })
  })
  await page.goto('/pro/admin')
  await page.getByRole('button', { name: '推广邀请', exact: true }).click()
  await page.getByRole('button', { name: '创建推广邀请', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('活动名称', { exact: true }).fill('开学季')
  await dialog.getByLabel('渠道', { exact: true }).fill('小红书')
  await dialog.getByLabel('用户来源标签').fill('校园, 博主A')
  await dialog.getByLabel('邀请码（留空自动生成）').fill('XHS2026A')
  await dialog.getByLabel('注册截止时间').fill('2027-09-30T23:59')
  await dialog.getByLabel('额外活动余额（USD）').fill('2.50')
  await dialog.getByLabel('赠送套餐', { exact: true }).selectOption('pro')
  await dialog.getByRole('button', { name: '创建邀请', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await expect(page.getByLabel('新建邀请链接')).toHaveValue(/\/pro\?invite=XHS2026A$/)
  expect(offer).toMatchObject({ channel: '小红书', tags: ['校园', '博主A'], grant_usd: 2.5, plan_code: 'pro' })
  await page.getByRole('button', { name: '复制邀请链接', exact: true }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toMatch(/\/pro\?invite=XHS2026A$/)
  await page.getByRole('button', { name: '注册记录', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('student@example.test')
  await expect(page.getByRole('dialog')).toContainText('已领取')
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByRole('button', { name: '暂停', exact: true }).click()
  await expect(page.getByRole('cell', { name: /已暂停/ })).toBeVisible()
})
