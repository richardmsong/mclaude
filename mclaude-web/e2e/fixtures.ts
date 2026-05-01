import { test as base, expect, type Page } from '@playwright/test'

// ── Constants ────────────────────────────────────────────────────────────────

export const DEV_EMAIL = process.env['DEV_EMAIL'] || 'dev@mclaude.local'
export const DEV_TOKEN = process.env['DEV_TOKEN'] || 'dev'

// ── Helpers ──────────────────────────────────────────────────────────────────

/** Wait for NATS connection — auth screen disappears and dashboard is visible. */
export async function waitForNatsConnected(page: Page): Promise<void> {
  await expect(page.locator('[data-testid="auth-screen"]')).not.toBeVisible({ timeout: 15_000 })
}

/** Click a session button by its display name. */
export async function navigateToSession(page: Page, sessionName: string): Promise<void> {
  await page.locator('button').filter({ hasText: sessionName }).click()
}

/** Click the settings gear icon in the nav bar. */
export async function navigateToSettings(page: Page): Promise<void> {
  await page.locator('button').filter({ hasText: '⚙' }).click()
}

/** Click the metrics/usage chart icon in the nav bar. */
export async function navigateToMetrics(page: Page): Promise<void> {
  await page.locator('button').filter({ hasText: '📊' }).click()
}

/** Click the back button in the nav bar. */
export async function navigateBack(page: Page): Promise<void> {
  await page.locator('button').filter({ hasText: '‹ Back' }).first().click()
}

// ── Fixtures ─────────────────────────────────────────────────────────────────

type Fixtures = {
  authenticatedPage: Page
}

export const test = base.extend<Fixtures>({
  authenticatedPage: async ({ page }, use) => {
    await page.goto('/')
    await expect(page.getByTestId('auth-screen')).toBeVisible({ timeout: 10_000 })

    await page.getByPlaceholder(/Email/).fill(DEV_EMAIL)
    await page.getByPlaceholder(/Access token/).fill(DEV_TOKEN)
    await page.getByRole('button', { name: 'Connect' }).click()

    // Wait for auth screen to disappear (dashboard loaded)
    await waitForNatsConnected(page)

    await use(page)
  },
})

export { expect }
