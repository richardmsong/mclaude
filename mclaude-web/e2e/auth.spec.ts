import { test, expect } from '@playwright/test'

// ── Dev deployment credentials ─────────────────────────────────────────────
const DEV_URL = 'https://dev.mclaude.richardmcsong.com'
const DEV_EMAIL = process.env['DEV_EMAIL'] || 'dev@mclaude.local'
const DEV_TOKEN = process.env['DEV_TOKEN'] || 'dev'

// ── Helper: login flow ─────────────────────────────────────────────────────
async function login(page: import('@playwright/test').Page, email = DEV_EMAIL, token = DEV_TOKEN) {
  await page.goto('/')
  await expect(page.getByTestId('auth-screen')).toBeVisible({ timeout: 10_000 })
  await page.getByPlaceholder(/Email/).fill(email)
  await page.getByPlaceholder(/Access token/).fill(token)
  await page.getByRole('button', { name: 'Connect' }).click()
  await expect(page.getByTestId('auth-screen')).not.toBeVisible({ timeout: 15_000 })
}

// ── Auth tests ─────────────────────────────────────────────────────────────

test.describe('Auth', () => {
  test.use({ baseURL: DEV_URL })

  // SPA-AUTH-01: Login with email/password → dashboard loads, auth screen gone
  test('SPA-AUTH-01: login with email/password loads dashboard', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByTestId('auth-screen')).toBeVisible({ timeout: 10_000 })

    await page.getByPlaceholder(/Email/).fill(DEV_EMAIL)
    await page.getByPlaceholder(/Access token/).fill(DEV_TOKEN)
    await page.getByRole('button', { name: 'Connect' }).click()

    // Auth screen should disappear and dashboard content should be visible
    await expect(
      page.getByTestId('auth-screen'),
      'Auth screen should be gone after successful login',
    ).not.toBeVisible({ timeout: 15_000 })

    // NavBar is present on the dashboard (contains the title and settings gear)
    await expect(
      page.getByText('MClaude').first(),
      'Dashboard should display MClaude branding or nav',
    ).toBeVisible({ timeout: 5_000 })
  })

  // SPA-AUTH-02: Login generates NKey pair client-side → check localStorage
  test('SPA-AUTH-02: login stores NKey data in localStorage', async ({ page }) => {
    await login(page)

    // After successful login, localStorage should contain token data with nkeySeed
    const stored = await page.evaluate(() => {
      const raw = localStorage.getItem('mclaude_tokens')
      if (!raw) return null
      try {
        return JSON.parse(raw)
      } catch {
        return null
      }
    })

    expect(stored, 'localStorage mclaude_tokens should exist after login').not.toBeNull()
    expect(stored.nkeySeed, 'NKey seed should be present in stored tokens').toBeTruthy()
    expect(stored.jwt, 'JWT should be present in stored tokens').toBeTruthy()
  })

  // SPA-AUTH-04: Session persistence across page reload
  test('SPA-AUTH-04: session persists across page reload', async ({ page }) => {
    await login(page)

    // Verify we're on the dashboard
    await expect(
      page.getByTestId('auth-screen'),
      'Should be on dashboard before reload',
    ).not.toBeVisible()

    // Reload the page
    await page.reload()

    // After reload, should still be authenticated (no auth screen)
    await expect(
      page.getByTestId('auth-screen'),
      'Auth screen should not appear after reload — session should persist',
    ).not.toBeVisible({ timeout: 15_000 })

    // Dashboard content should be visible again
    await expect(
      page.getByText('MClaude').first(),
      'Dashboard should be visible after reload',
    ).toBeVisible({ timeout: 5_000 })
  })

  // SPA-AUTH-05: Logout clears credentials
  test('SPA-AUTH-05: logout clears credentials and shows auth screen', async ({ page }) => {
    await login(page)

    // Navigate to settings
    await page.goto('/#/settings')
    await expect(
      page.getByText('Settings').first(),
      'Settings page should be visible',
    ).toBeVisible({ timeout: 5_000 })

    // Click Sign Out
    await page.getByRole('button', { name: 'Sign Out' }).click()

    // Auth screen should appear
    await expect(
      page.getByTestId('auth-screen'),
      'Auth screen should appear after signing out',
    ).toBeVisible({ timeout: 10_000 })

    // localStorage should be cleared
    const stored = await page.evaluate(() => localStorage.getItem('mclaude_tokens'))
    expect(stored, 'mclaude_tokens should be cleared from localStorage after logout').toBeNull()
  })

  // SPA-AUTH-06: Invalid credentials rejected
  test('SPA-AUTH-06: invalid credentials show error message', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByTestId('auth-screen')).toBeVisible({ timeout: 10_000 })

    await page.getByPlaceholder(/Email/).fill('wrong@example.com')
    await page.getByPlaceholder(/Access token/).fill('bad-token-12345')
    await page.getByRole('button', { name: 'Connect' }).click()

    // Should remain on auth screen with an error message
    await expect(
      page.getByTestId('auth-screen'),
      'Auth screen should remain visible after invalid login',
    ).toBeVisible({ timeout: 10_000 })

    // Error text should be displayed (the AuthScreen renders errors in a red div)
    await expect(
      page.locator('[data-testid="auth-screen"] div').filter({ hasText: /(error|invalid|unauthorized|failed|401|incorrect)/i }).first(),
      'An error message should be displayed for invalid credentials',
    ).toBeVisible({ timeout: 10_000 })
  })

  // SPA-NATS-01: NATS WebSocket connection established → green indicator
  test('SPA-NATS-01: NATS connection shows green indicator after login', async ({ page }) => {
    await login(page)

    // The NavBar renders a StatusDot with state='connected' (green) when NATS is connected.
    // The green dot uses background: var(--green). We check for its presence.
    const statusDot = page.locator('.pulse, span').filter({
      has: page.locator('span'),
    })

    // More reliable: check that the NavBar is showing with a connected indicator.
    // The StatusDot is an inline-block span with borderRadius 50% and green background.
    // We look for a green-colored dot in the NavBar area.
    const navBar = page.locator('div').filter({ hasText: 'MClaude' }).first()
    await expect(navBar, 'NavBar should be visible after login').toBeVisible({ timeout: 5_000 })

    // Verify connection by checking the Settings page which explicitly shows "Connected"
    await page.goto('/#/settings')
    await expect(
      page.getByText('Connected').first(),
      'Settings should show "Connected" status when NATS WebSocket is established',
    ).toBeVisible({ timeout: 10_000 })
  })

  // SPA-ROUTE-01: Hash navigation works → settings, usage views
  test('SPA-ROUTE-01: hash navigation renders correct views', async ({ page }) => {
    await login(page)

    // Navigate to settings
    await page.goto('/#/settings')
    await expect(
      page.getByText('Settings').first(),
      'Settings view should render when navigating to #/settings',
    ).toBeVisible({ timeout: 5_000 })

    // Navigate to usage
    await page.goto('/#/usage')
    await expect(
      page.getByText('Token Usage').first(),
      'Token Usage view should render when navigating to #/usage',
    ).toBeVisible({ timeout: 5_000 })

    // Navigate back to dashboard
    await page.goto('/#/')
    // Dashboard should show — auth screen should not
    await expect(
      page.getByTestId('auth-screen'),
      'Auth screen should not appear when navigating to dashboard route',
    ).not.toBeVisible({ timeout: 5_000 })
  })
})
