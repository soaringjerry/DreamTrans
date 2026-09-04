import { expect, test, type Page } from '@playwright/test'

/**
 * Interface language: follows the browser by default, the switch overrides it,
 * and the choice survives a reload.
 */

async function installBackend(page: Page) {
  // Match the backend only; `**/api/**` would also swallow Vite's /src/pro/api/* modules.
  await page.route((url) => url.pathname.startsWith('/api/'), (route) => route.fulfill({
    body: JSON.stringify({ error: 'not mocked' }),
    contentType: 'application/json',
    status: 404,
  }))
}

test.describe('interface language', () => {
  test('follows a Chinese browser and switches to English', async ({ page }) => {
    await installBackend(page)
    await page.goto('/')
    await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN')
    await expect(page.getByRole('heading', { level: 1 })).toContainText('把每一场对话')

    await page.getByRole('radio', { name: 'English' }).click()
    await expect(page.locator('html')).toHaveAttribute('lang', 'en')
    await expect(page.getByRole('heading', { level: 1 })).toContainText('Turn every conversation')
    await expect(page.getByRole('radio', { name: 'English' })).toHaveAttribute('aria-checked', 'true')

    // The choice is remembered across a reload.
    await page.reload()
    await expect(page.locator('html')).toHaveAttribute('lang', 'en')
    await expect(page.getByRole('heading', { level: 1 })).toContainText('Turn every conversation')
    expect(await page.evaluate(() => localStorage.getItem('dt_locale'))).toBe('en')
  })

  test.describe('english browser', () => {
    test.use({ locale: 'en-US' })

    test('opens in English and the login gate is translated too', async ({ page }) => {
      await installBackend(page)
      await page.goto('/')
      await expect(page.locator('html')).toHaveAttribute('lang', 'en')
      await expect(page.getByRole('heading', { level: 1 })).toContainText('Turn every conversation')

      await page.goto('/pro.html')
      await expect(page.getByRole('button', { name: 'Log in' })).toBeVisible()
      await page.getByRole('radio', { name: '中文' }).click()
      await expect(page.getByRole('button', { name: '登录' })).toBeVisible()
    })
  })
})
