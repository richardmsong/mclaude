import { test, expect } from '@playwright/test'

const DEV_EMAIL = process.env['DEV_EMAIL'] || 'dev@mclaude.local'
const DEV_TOKEN = process.env['DEV_TOKEN'] || 'dev'

// ── Terminal tab tests ──────────────────────────────────────────────────
// These run against the live dev deployment and verify the terminal UI.

test.describe('Terminal Tab', () => {
  test.use({ baseURL: process.env['BASE_URL'] || 'https://dev.mclaude.richardmcsong.com' })

  // SPA-TERM-01: Terminal tab exists
  test('SPA-TERM-01: Terminal tab button is visible in session detail', async ({ page }) => {
    await page.goto('/')

    // Login
    await page.getByPlaceholder(/Email/).fill(DEV_EMAIL)
    await page.getByPlaceholder(/Access token/).fill(DEV_TOKEN)
    await page.getByRole('button', { name: 'Connect' }).click()

    // Wait for dashboard
    await expect(page.locator('[data-testid="auth-screen"]')).not.toBeVisible({ timeout: 15_000 })

    // Open the first session (look for session row by name or status dot)
    const sessionItem = page.locator('button').filter({
      has: page.locator('span.pulse, span[style*="border-radius: 50%"]'),
    }).first()
    await expect(sessionItem).toBeVisible({ timeout: 30_000 })
    await sessionItem.click()

    // Wait for session detail to load
    await expect(page.locator('textarea[placeholder*="Message"]')).toBeVisible({ timeout: 10_000 })

    // Verify both Events and Terminal tab buttons are visible
    const eventsTab = page.locator('button', { hasText: 'Events' })
    const terminalTab = page.locator('button', { hasText: 'Terminal' })

    await expect(eventsTab).toBeVisible({ timeout: 5_000 })
    await expect(terminalTab).toBeVisible({ timeout: 5_000 })

    // Verify the Terminal tab is clickable
    await terminalTab.click()

    // After clicking Terminal tab, the Events tab input area should be hidden
    // and the terminal area (xterm or placeholder) should be shown
    await expect(page.locator('textarea[placeholder*="Message"]')).not.toBeVisible({ timeout: 3_000 })

    // The terminal area should show either an xterm instance or the NATS placeholder
    const terminalContent = page.locator('text=/Terminal|connect via NATS/')
    await expect(terminalContent).toBeVisible({ timeout: 5_000 })
  })
})
