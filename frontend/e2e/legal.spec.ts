import { expect, test } from '@playwright/test'

test.describe('legal documents', () => {
  test('the landing footer opens the privacy policy and terms', async ({ page }) => {
    await page.goto('/')
    const footer = page.getByRole('contentinfo')
    await expect(footer.getByRole('link', { name: '隐私政策' })).toBeVisible()
    await expect(footer.getByRole('link', { name: '用户条款' })).toBeVisible()

    await footer.getByRole('link', { name: '隐私政策' }).click()
    await expect(page).toHaveURL(/\/privacy$/)
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('隐私政策')
    await expect(page.locator('#who')).toContainText('我们是谁')
    await expect(page.getByText('Coyume Pty Ltd').first()).toBeVisible()
    await expect(page.locator('#audio')).toContainText('实时音频会流经我们的服务器')

    await page.getByRole('banner').getByRole('link', { name: '用户条款' }).click()
    await expect(page).toHaveURL(/\/terms$/)
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('用户条款')
    await expect(page.locator('#recording')).toContainText('录音、同意与合法使用')
  })

  test('legal pages follow the interface language', async ({ page }) => {
    await page.goto('/privacy')
    await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('隐私政策')

    await page.getByRole('radio', { name: 'English' }).click()
    await expect(page.locator('html')).toHaveAttribute('lang', 'en')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Privacy Policy')
    await expect(page.getByRole('heading', { level: 2 }).first()).toContainText('Who we are')

    await page.goto('/terms')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Terms of Use')
    await expect(page.getByText('Effective date: 2026-09-04')).toBeVisible()
  })
})
