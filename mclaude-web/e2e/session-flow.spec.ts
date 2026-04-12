import { test, expect } from '@playwright/test'

// ── Smoke tests (always run — no server required) ──────────────────────────
// These validate the static UI without requiring a NATS connection.

test.describe('Smoke tests', () => {
  test('app loads without JS errors', async ({ page }) => {
    const errors: string[] = []
    page.on('console', msg => {
      if (msg.type() === 'error') errors.push(msg.text())
    })
    page.on('pageerror', err => errors.push(err.message))

    await page.goto('/')
    await page.waitForTimeout(500)

    // Filter out benign browser errors
    const realErrors = errors.filter(e =>
      !e.includes('favicon') &&
      !e.includes('net::ERR_') &&
      !e.includes('Failed to fetch') &&
      !e.includes('WebSocket')
    )
    expect(realErrors, `JS errors: ${realErrors.join('; ')}`).toHaveLength(0)
  })

  test('shows auth screen when unauthenticated', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByTestId('auth-screen')).toBeVisible({ timeout: 5000 })
    await expect(page.getByTestId('auth-title')).toHaveText('MClaude')
  })

  test('auth screen has email and token fields (no server URL field)', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByPlaceholder(/Email/)).toBeVisible()
    await expect(page.getByPlaceholder(/Access token/)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Connect' })).toBeVisible()
    // Server URL field was removed — derived from window.location.origin
    await expect(page.getByPlaceholder(/Server URL/)).not.toBeVisible()
  })

  test('connect button is disabled when token is empty', async ({ page }) => {
    await page.goto('/')
    const connectBtn = page.getByRole('button', { name: 'Connect' })
    await expect(connectBtn).toBeDisabled()
  })

  test('connect button enables when token is filled', async ({ page }) => {
    await page.goto('/')
    await page.getByPlaceholder(/Access token/).fill('test-token')
    const connectBtn = page.getByRole('button', { name: 'Connect' })
    await expect(connectBtn).toBeEnabled()
  })

  test('connect button disabled without token even if email is filled', async ({ page }) => {
    await page.goto('/')
    await page.getByPlaceholder(/Email/).fill('user@example.com')
    const connectBtn = page.getByRole('button', { name: 'Connect' })
    await expect(connectBtn).toBeDisabled()
  })

  test('hash navigation does not crash the app', async ({ page }) => {
    const errors: string[] = []
    page.on('pageerror', err => errors.push(err.message))

    // Navigate to various routes while unauthenticated — should show auth screen
    await page.goto('/#settings')
    await expect(page.getByTestId('auth-screen')).toBeVisible({ timeout: 3000 })

    await page.goto('/#usage')
    await expect(page.getByTestId('auth-screen')).toBeVisible({ timeout: 3000 })

    expect(errors).toHaveLength(0)
  })
})

// ── Full session flow (requires E2E_SERVER=1 + running dev + NATS mock) ────

test.describe('Session flow', () => {
  test.skip(!process.env['E2E_SERVER'], 'Requires E2E_SERVER=1 and running dev server + NATS')

  test('auth → session list → open session → send message → approve permission → see result', async ({ page }) => {
    await page.goto('/')

    // Login form — no Server URL field, only email + token
    await expect(page.getByTestId('auth-screen')).toBeVisible()
    await page.getByPlaceholder(/Email/).fill('test@example.com')
    await page.getByPlaceholder(/Access token/).fill(process.env['E2E_TOKEN'] ?? 'test-token')
    await page.getByRole('button', { name: 'Connect' }).click()

    // After login, dashboard should appear
    await expect(page.locator('[data-testid="auth-screen"]')).not.toBeVisible({ timeout: 10000 })

    // Open the first session if any
    const sessionItem = page.locator('button').filter({ hasText: /Working|Idle/ }).first()
    if (await sessionItem.isVisible({ timeout: 5000 })) {
      await sessionItem.click()

      // Conversation view
      await expect(page.locator('textarea[placeholder*="Message"]')).toBeVisible({ timeout: 5000 })

      // Send a message
      const input = page.locator('textarea[placeholder*="Message"]')
      await input.fill('Hello, Claude!')
      await input.press('Enter')

      // Message should appear
      await expect(page.getByText('Hello, Claude!')).toBeVisible({ timeout: 5000 })

      // Approve permission if it appears
      const approveBtn = page.getByRole('button', { name: /Approve/ })
      if (await approveBtn.isVisible({ timeout: 3000 }).catch(() => false)) {
        await approveBtn.click()
      }

      // Wait for assistant response
      await expect(page.locator('.ev-text, [data-testid="assistant-turn"]')).toBeVisible({ timeout: 30000 })
    }
  })

  test('version block screen shown when client is outdated', async ({ page }) => {
    await page.goto('/')

    await page.evaluate(() => {
      window.dispatchEvent(new CustomEvent('mclaude:version-check', {
        detail: { currentVersion: '0.1.0', minClientVersion: '1.0.0', reloadCount: 1 },
      }))
    })

    await expect(page.getByTestId('version-block-screen')).toBeVisible({ timeout: 3000 })
    await expect(page.getByText(/updating/i)).toBeVisible()
  })
})
