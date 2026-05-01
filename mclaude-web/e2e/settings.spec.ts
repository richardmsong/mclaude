import { test, expect, type Page } from '@playwright/test'

// ── Helper: login and wait for dashboard ────────────────────────────────────

async function login(page: Page) {
  await page.goto('/')
  await expect(page.getByTestId('auth-screen')).toBeVisible({ timeout: 10000 })
  await page.getByPlaceholder(/Email/).fill('dev@mclaude.local')
  await page.getByPlaceholder(/Access token/).fill('dev')
  await page.getByRole('button', { name: 'Connect' }).click()
  // Wait for auth screen to disappear (dashboard or other screen loads)
  await expect(page.getByTestId('auth-screen')).not.toBeVisible({ timeout: 15000 })
}

// ── Settings page tests ─────────────────────────────────────────────────────

test.describe('Settings page', () => {
  test.skip(!process.env['E2E_SERVER'], 'Requires E2E_SERVER=1 and running deployment')

  test.use({ baseURL: 'https://dev.mclaude.richardmcsong.com' })

  test.beforeEach(async ({ page }) => {
    await login(page)
    // Navigate to settings via hash route
    await page.goto('/#/settings')
    // Verify the Settings NavBar title is visible
    await expect(page.getByText('Settings', { exact: true })).toBeVisible({ timeout: 10000 })
  })

  test('SPA-SET-01: Settings page loads with Host, Connection, and Account sections', async ({ page }) => {
    // Verify the three core section headers are present
    await expect(page.getByText('HOST', { exact: true })).toBeVisible()
    await expect(page.getByText('CONNECTION', { exact: true })).toBeVisible()
    await expect(page.getByText('ACCOUNT', { exact: true })).toBeVisible()
  })

  test('SPA-SET-01b: Server URL is displayed in settings', async ({ page }) => {
    // The HOST section shows the Server row with the origin URL
    await expect(page.getByText('Server')).toBeVisible()
    // The server URL should contain the deployment domain
    await expect(page.getByText('dev.mclaude.richardmcsong.com')).toBeVisible()
  })

  test('SPA-SET-01c: Connection status shows Connected', async ({ page }) => {
    // The CONNECTION section shows Status → Connected
    await expect(page.getByText('Status')).toBeVisible()
    await expect(page.getByText('Connected', { exact: true })).toBeVisible()
  })

  test('SPA-SET-01d: Session count is displayed', async ({ page }) => {
    // The CONNECTION section shows Sessions row with a numeric count
    await expect(page.getByText('Sessions')).toBeVisible()
    // Session count is rendered next to "Sessions" — just verify the row exists
    const sessionsRow = page.locator('div').filter({ hasText: /^Sessions/ })
    await expect(sessionsRow.first()).toBeVisible()
  })

  test('SPA-SET-03: PAT input button is available', async ({ page }) => {
    // The GIT PROVIDERS section should have the "+ Add provider with PAT" button
    const patButton = page.getByText('+ Add provider with PAT')
    await expect(patButton).toBeVisible({ timeout: 10000 })
  })

  test('SPA-SET-07: Sign Out button exists', async ({ page }) => {
    const signOutBtn = page.getByRole('button', { name: 'Sign Out' })
    await expect(signOutBtn).toBeVisible()
  })

  test('SPA-SET-08: Reset Client Cache button exists', async ({ page }) => {
    const cacheResetBtn = page.getByText('Reset Client Cache')
    await expect(cacheResetBtn).toBeVisible()
  })
})
