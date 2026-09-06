import { expect, test, type Page, type Route } from '@playwright/test'

const now = '2026-09-04T02:00:00.000Z'
const user = {
  id: 'user-statement',
  tenant_id: 'tenant-1',
  email: 'accountant@example.test',
  name: 'Accountant',
  role: 'user',
  is_active: true,
  email_verified: true,
  created_at: now,
  updated_at: now,
}

const freePlan = {
  code: 'free', name: 'Free', is_public: true, active: true, sort: 0,
  price_usd_month: 0, price_usd_year: 0, usage_discount_percent: 0,
  storage_gb: 1, retention_days: 30, max_concurrent_sessions: 1, seats: 1,
  features: {},
}

// The figures the workspace itself reported for one lecture, so the panel is
// checked against numbers a user would recognise from their session.
const septemberTotals = {
  transcription_usd: 1.66,
  transcription_seconds: 6240,
  translation_usd: 0.14,
  ai_usd: 0.006,
  charged_usd: 1.806,
  refunded_usd: 0,
  from_grant_usd: 0.006,
  from_wallet_usd: 1.8,
  topup_usd: 20,
  membership_usd: 0,
}

function accessToken(): string {
  const encode = (value: object) => Buffer.from(JSON.stringify(value)).toString('base64url')
  return `${encode({ alg: 'none', typ: 'JWT' })}.${encode({
    exp: Math.floor(Date.now() / 1_000) + 3_600,
    sub: user.id,
  })}.e2e`
}

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ body: JSON.stringify(body), contentType: 'application/json', status })
}

/** Every statement request the panel made, so the month filter can be asserted. */
interface Recorder { months: string[]; csvRequests: string[] }

async function installBackend(page: Page, rewardStatus?: 'budget_hold'): Promise<Recorder> {
  const recorder: Recorder = { months: [], csvRequests: [] }
  const balance = {
    user_id: user.id,
    account_id: 'account-statement',
    wallet_usd: 3.2,
    grant_usd: 0,
    available_usd: 3.2,
    lifetime_charged_usd: 1.806,
    plan_code: 'free',
    member_active: false,
    auto_topup_enabled: false,
  }
  await page.route(/^https?:\/\/[^/]+\/api\//, async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const method = request.method()
    const path = url.pathname
    if (method === 'GET' && path === '/api/system/access') {
      await json(route, {
        anonymous_api_enabled: false, authentication_enabled: true,
        registration_enabled: false, rag_enabled: true,
      })
      return
    }
    if (method === 'GET' && path === '/api/system/settings') {
      await json(route, { allow_user_api_key: false })
      return
    }
    if (method === 'POST' && path === '/api/auth/login') {
      await json(route, { access_token: accessToken(), expires_in: 3_600, refresh_token: 'e2e-refresh', user })
      return
    }
    if (method === 'GET' && path === '/api/user/profile') { await json(route, { user }); return }
    if (method === 'GET' && path === '/api/user/balance') { await json(route, balance); return }
    if (method === 'GET' && path === '/api/user/billing/account') {
      await json(route, {
        account: {
          ...balance, email: user.email, name: user.name, status: 'active', signup_reward_status: rewardStatus,
          plan: freePlan, effective_plan: freePlan, discount_percent: 0, grants: [],
          has_payment_method: false, storage_bytes: 0,
          realtime_hour_usd: 0.96, estimated_realtime_hours: 3.33,
        },
        payments_enabled: true,
      })
      return
    }
    if (method === 'GET' && path === '/api/user/billing/statement') {
      const month = url.searchParams.get('month') ?? url.searchParams.get('from') ?? ''
      if (url.searchParams.get('format') === 'csv') {
        recorder.csvRequests.push(month)
        await route.fulfill({
          body: 'record,timestamp,type,amount_usd\nusage,2026-09-04T01:00:00Z,transcription,-1.660000\n',
          contentType: 'text/csv; charset=utf-8',
          headers: { 'Content-Disposition': "attachment; filename*=UTF-8''yufolo-statement-2026-09.csv" },
          status: 200,
        })
        return
      }
      recorder.months.push(month)
      const september = month === '2026-09'
      await json(route, {
        from: now, to: now,
        usage: september ? [{ id: 'u1', action: 'transcription', cost_usd: 1.66, created_at: now }] : [],
        ledger: [],
        payments: september ? [{ id: 'p1', kind: 'topup', amount_usd: 20, status: 'succeeded', created_at: now }] : [],
        totals: september
          ? septemberTotals
          : { ...septemberTotals, transcription_usd: 0, translation_usd: 0, ai_usd: 0, charged_usd: 0, topup_usd: 0 },
        truncated: false,
      })
      return
    }
    if (method === 'GET' && path === '/api/user/billing/session-costs') { await json(route, { session_costs: [] }); return }
    if (method === 'GET' && path === '/api/sessions') {
      // One existing session keeps the onboarding wizard out of the way; it
      // covers the whole workspace and would swallow the panel click.
      await json(route, {
        page: 1, page_size: 60,
        sessions: [{
          id: '11111111-1111-4111-8111-111111111111', user_id: user.id, tenant_id: user.tenant_id,
          title: 'Lecture 1', source_language: 'en', target_language: 'cmn',
          duration_seconds: 6240, status: 'completed', started_at: now, created_at: now, updated_at: now,
        }],
      })
      return
    }
    if (method === 'GET' && path === '/api/models/available') { await json(route, { models: [] }); return }
    if (method === 'GET' && path === '/api/user/model-preferences') {
      await json(route, { translation_model: '', chat_model: '', summary_model: '' })
      return
    }
    await json(route, {})
  })
  return recorder
}

async function openAccountPanel(page: Page): Promise<void> {
  await page.goto('/pro.html')
  await page.locator('.dt-auth__card input[type="email"]').fill(user.email)
  await page.locator('.dt-auth__card input[type="password"]').fill('password123')
  await page.locator('.dt-auth__card .dt-button--primary').click()
  await expect(page.locator('.dt-sidebar')).toBeVisible()
  await page.locator('.dt-account-chip').click()
  await expect(page.locator('.dt-billing-statement')).toBeVisible()
}

test('the account panel totals a month and exports it as CSV', async ({ page }) => {
  const recorder = await installBackend(page)
  await openAccountPanel(page)

  const statement = page.locator('.dt-billing-statement')
  // The month the panel opens on is the current one, not "everything".
  expect(recorder.months.at(-1)).toBe('2026-09')
  await expect(statement.locator('.dt-billing-statement__total strong')).toHaveText('US$1.81')
  // Spend and money-in are separate groups, so a top-up never reads as a charge.
  const spend = statement.locator('.dt-billing-statement__split').first()
  await expect(spend).toContainText('实时转录')
  await expect(spend).toContainText('US$1.66')
  await expect(spend).toContainText('AI 功能')
  await expect(spend).not.toContainText('充值')
  const received = statement.locator('.dt-billing-statement__split').last()
  await expect(received).toContainText('充值')
  await expect(received).toContainText('US$20.00')

  const download = page.waitForEvent('download')
  await statement.getByRole('button', { name: '导出 CSV' }).click()
  const saved = await download
  expect(saved.suggestedFilename()).toBe('yufolo-statement-2026-09.csv')
  expect(recorder.csvRequests).toEqual(['2026-09'])
})

test('picking another period reloads the totals and follows the export', async ({ page }) => {
  const recorder = await installBackend(page)
  await openAccountPanel(page)

  const statement = page.locator('.dt-billing-statement')
  await statement.locator('select').selectOption('2026-08')
  await expect(statement.locator('.dt-billing-statement__total strong')).toHaveText('US$0.00')
  await expect(statement).toContainText('本期没有账单记录。')
  expect(recorder.months.at(-1)).toBe('2026-08')

  // "All records" drops the month filter rather than sending an empty one.
  await statement.locator('select').selectOption('')
  await expect.poll(() => recorder.months.at(-1)).toBe('2000-01-01')

  const download = page.waitForEvent('download')
  await statement.getByRole('button', { name: '导出 CSV' }).click()
  await download
  expect(recorder.csvRequests).toEqual(['2000-01-01'])
})


test('a reward budget hold explains retries without blocking the account', async ({ page }) => {
  await installBackend(page, 'budget_hold')
  await openAccountPanel(page)
  await expect(page.getByRole('status').filter({ hasText: '活动发放预算暂缓' })).toBeVisible()
  await expect(page.locator('.dt-billing-statement')).toBeVisible()
})

for (const locale of ['zh-CN', 'en'] as const) {
  test(`membership marketing omits catalog feature flags in ${locale}`, async ({ page }) => {
    await page.addInitScript((value) => localStorage.setItem('dt_locale', value), locale)
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await installBackend(page)
    const sharedFeatures = {
      premium_models: true, byok: true, batch: true, custom_prompt: true,
      auto_topup: true, export_ledger: true, api_access: true, unverified_future_perk: true,
    }
    const proPlan = {
      ...freePlan, code: 'pro', name: 'Pro', sort: 1,
      price_usd_month: 10, price_usd_year: 100,
      usage_discount_percent: 20, realtime_hour_usd: 0.8,
      retention_days: 999, storage_gb: 999, max_concurrent_sessions: 3,
      features: sharedFeatures,
    }
    const catalog = [
      { ...freePlan, features: sharedFeatures, realtime_hour_usd: 1 }, proPlan,
    ]
    await page.route('**/api/public/pricing', (route) => json(route, {
      plans: catalog, topup_tiers: [], trial_credit_usd: 0,
      trial_credit_days: 7, payments_enabled: true, checkout_currency: 'usd',
    }))
    await page.route('**/api/user/billing/plans', (route) => json(route, {
      plans: catalog, topup_tiers: [],
      hourly: [{ plan_code: 'pro', realtime_hour_usd: 0.8 }],
    }))
    const unsupported = /自动充值|批量|API|提示词|导出|高级|premium|advanced|batch|custom prompt|auto.?top.?up|export|unverified_future_perk/i
    await page.goto('/')
    const landingCard = page.locator('.lp-price-card').filter({ has: page.getByRole('heading', { name: 'Pro', exact: true }) })
    await expect(landingCard).toContainText('US$10')
    await expect(landingCard).toContainText(locale === 'zh-CN' ? '8 折' : '20%')
    await expect(landingCard).not.toContainText(unsupported)
    await expect(landingCard).not.toContainText('999')
    await expect(landingCard).toContainText(locale === 'zh-CN' ? '3 路并发转录' : '3 concurrent transcriptions')
    await expect(page.locator('#pricing')).not.toContainText(/高级能力|premium features/i)

    await openAccountPanel(page)
    const upgradeCard = page.locator('.dt-billing-plan').filter({ has: page.locator('.dt-billing-plan__head strong', { hasText: 'Pro' }) })
    await expect(upgradeCard).toContainText('US$10')
    await expect(upgradeCard).toContainText(locale === 'zh-CN' ? '8 折' : '20%')
    await expect(upgradeCard).toContainText('US$0.80')
    await expect(upgradeCard).not.toContainText(unsupported)
  })
}
