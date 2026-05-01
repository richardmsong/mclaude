import { test, expect, type Page } from '@playwright/test'

// ── Quota-Managed Session UI ────────────────────────────────────────────────
// These tests verify quota-managed session display:
//   - Paused sessions with pausedVia label
//   - needs_spec_fix state with blocked-on tool display
//   - Resume countdown timer for autoContinue sessions
//   - Distinguishing quota-managed from interactive sessions
//
// NOTE: The quota UI states (paused, needs_spec_fix, softThreshold, resumeAt,
// autoContinue) are NOT yet implemented in the SPA. The SPA SessionState type
// does not include 'paused' or 'needs_spec_fix'. These tests are marked as
// fixme and will be unblocked once the SPA implements the quota UI.

const BASE_URL = 'https://dev.mclaude.richardmcsong.com'

async function login(page: Page): Promise<void> {
  await page.goto('/')
  await expect(page.getByTestId('auth-screen')).toBeVisible({ timeout: 10_000 })
  await page.getByPlaceholder(/Email/).fill('dev@mclaude.local')
  await page.getByPlaceholder(/Access token/).fill('dev')
  await page.getByRole('button', { name: 'Connect' }).click()
  await expect(page.getByTestId('auth-screen')).not.toBeVisible({ timeout: 20_000 })
}

test.describe('Quota-Managed Session UI', () => {
  test.use({ baseURL: BASE_URL })

  test.beforeEach(async ({ page }) => {
    await login(page)
    // Wait for dashboard to load
    await page.locator('span[style*="border-radius: 50%"]').first().waitFor({ state: 'visible', timeout: 30_000 }).catch(() => {})
  })

  // SPA-QUOTA-01: Display paused sessions
  // Marked fixme: SPA does not implement 'paused' state or pausedVia label
  test.fixme('SPA-QUOTA-01: paused sessions show pause icon and pausedVia label', async ({ page }) => {
    // When a session is in 'paused' state with pausedVia set (e.g. "quota_soft"),
    // the session list should show:
    // - A pause icon (⏸ or similar)
    // - A label showing the pause reason (e.g. "quota_soft")

    // This requires: (a) a quota-managed session to exist in paused state,
    // and (b) the SPA to implement 'paused' as a SessionState with pausedVia display.

    const pausedSession = page.locator('div, span').filter({ hasText: /paused|quota_soft|⏸/i }).first()
    await expect(pausedSession).toBeVisible({ timeout: 5_000 })
  })

  // SPA-QUOTA-02: Display needs_spec_fix status
  // Marked fixme: SPA does not implement 'needs_spec_fix' state
  test.fixme('SPA-QUOTA-02: needs_spec_fix state shows blocked-on tool from KV', async ({ page }) => {
    // When a session is in 'needs_spec_fix' state, the dashboard should show:
    // "blocked on: {failedTool}" from the session KV entry

    const blockedOn = page.locator('div, span').filter({ hasText: /blocked on:/i }).first()
    await expect(blockedOn).toBeVisible({ timeout: 5_000 })
  })

  // SPA-QUOTA-03: Display resume countdown
  // Marked fixme: SPA does not implement autoContinue/resumeAt display
  test.fixme('SPA-QUOTA-03: resume countdown shows time until resumeAt for autoContinue sessions', async ({ page }) => {
    // When a session has autoContinue=true and a resumeAt timestamp,
    // the UI should show a countdown timer to the resume time.

    const countdown = page.locator('div, span').filter({ hasText: /resume.*in|resuming.*in|auto.?continue/i }).first()
    await expect(countdown).toBeVisible({ timeout: 5_000 })
  })

  // SPA-QUOTA-04: Distinguish quota-managed sessions
  // Partial: can check for softThreshold > 0 visual indicator
  test.fixme('SPA-QUOTA-04: quota-managed sessions identifiable and grouped/dimmed when paused', async ({ page }) => {
    // Quota-managed sessions (softThreshold > 0) should be visually distinguishable.
    // When paused, they should be grouped or dimmed in the session list.

    // This requires: (a) quota-managed sessions to exist,
    // and (b) SPA to implement softThreshold-based visual grouping.

    const quotaSession = page.locator('div').filter({ hasText: /quota/i }).first()
    await expect(quotaSession).toBeVisible({ timeout: 5_000 })
  })

  // Accessible/implemented: verify the SPA correctly shows known session states
  test('SPA-QUOTA-04 (partial): session list renders without quota-state crashes', async ({ page }) => {
    // The SPA should render session list cleanly even without quota-managed sessions
    const errors: string[] = []
    page.on('pageerror', e => errors.push(e.message))
    await page.waitForTimeout(2_000)

    const realErrors = errors.filter(e =>
      !e.includes('favicon') && !e.includes('net::ERR_') && !e.includes('WebSocket')
    )
    expect(realErrors).toHaveLength(0)

    // Session list renders correctly
    const sessionItems = page.locator('span[style*="border-radius: 50%"]')
    const count = await sessionItems.count()
    test.info().annotations.push({
      type: 'info',
      description: `Session list renders without crashes. ${count} session status dots visible.`,
    })
  })
})
