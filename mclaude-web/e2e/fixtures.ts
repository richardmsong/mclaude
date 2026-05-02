import { test as base, expect, type Page } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import { fileURLToPath } from 'url'

// ── Constants ────────────────────────────────────────────────────────────────

const TEST_USER_FILE = path.join(path.dirname(fileURLToPath(import.meta.url)), '.test-user.json')

function loadTestUser(): { email: string; token: string; projectSlug?: string; sessionSlug?: string } | null {
  try {
    if (fs.existsSync(TEST_USER_FILE)) {
      const record = JSON.parse(fs.readFileSync(TEST_USER_FILE, 'utf-8'))
      if (!record.skipped && record.email && record.token) {
        return {
          email: record.email as string,
          token: record.token as string,
          projectSlug: typeof record.projectSlug === 'string' ? record.projectSlug : undefined,
          sessionSlug: typeof record.sessionSlug === 'string' ? record.sessionSlug : undefined,
        }
      }
    }
  } catch {}
  return null
}

const testUser = loadTestUser()
export const DEV_EMAIL = process.env['DEV_EMAIL'] || testUser?.email || 'dev@mclaude.local'
export const DEV_TOKEN = process.env['DEV_TOKEN'] || testUser?.token || 'dev'
export const DEV_PROJECT_SLUG = process.env['DEV_PROJECT_SLUG'] || testUser?.projectSlug || 'default-project'
export const DEV_SESSION_SLUG = process.env['DEV_SESSION_SLUG'] || testUser?.sessionSlug || ''

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
