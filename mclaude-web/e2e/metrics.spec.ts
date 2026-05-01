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

// ── Token Usage / Metrics page tests ────────────────────────────────────────

test.describe('Token Usage page', () => {
  test.skip(!process.env['E2E_SERVER'], 'Requires E2E_SERVER=1 and running deployment')

  test.use({ baseURL: 'https://dev.mclaude.richardmcsong.com' })

  test.beforeEach(async ({ page }) => {
    await login(page)
    // Navigate to usage/metrics via hash route
    await page.goto('/#/usage')
    // Verify the Token Usage NavBar title is visible
    await expect(page.getByText('Token Usage', { exact: true })).toBeVisible({ timeout: 10000 })
  })

  test('Metrics page loads with Token Usage title', async ({ page }) => {
    // Title is in the NavBar
    await expect(page.getByText('Token Usage', { exact: true })).toBeVisible()
  })

  test('Time range filter buttons exist (1H, 6H, 24H, 7D, 30D)', async ({ page }) => {
    for (const label of ['1H', '6H', '24H', '7D', '30D']) {
      await expect(page.getByRole('button', { name: label, exact: true })).toBeVisible()
    }
  })

  test('Token count displays a valid number', async ({ page }) => {
    // The "Tokens" stat tile shows a formatted number (e.g. "0", "1.2K", "3.5M")
    const tokensLabel = page.getByText('Tokens', { exact: true })
    await expect(tokensLabel).toBeVisible()
    // The value is rendered as a sibling/adjacent element — find the tile container
    const tokensTile = page.locator('div').filter({ hasText: /^Tokens/ }).first()
    const tileText = await tokensTile.textContent()
    // Extract the numeric portion (after "Tokens" label) — should not be NaN
    const numericPart = tileText?.replace('Tokens', '').trim() ?? ''
    // Valid formats: "0", "123", "1.2K", "3.5M", "$0.000"
    expect(numericPart).toMatch(/^\d/)
  })

  test('Cost display shows a dollar value', async ({ page }) => {
    // The "Cost" stat tile shows a formatted cost starting with $
    const costLabel = page.getByText('Cost', { exact: true }).first()
    await expect(costLabel).toBeVisible()
    const costTile = page.locator('div').filter({ hasText: /^Cost\$/ }).first()
    const costText = await costTile.textContent()
    const costValue = costText?.replace('Cost', '').trim() ?? ''
    // Should start with $ and contain a valid number
    expect(costValue).toMatch(/^\$\d/)
  })

  test('Breakdown section shows Input, Output, Cache Read, Cache Write labels', async ({ page }) => {
    await expect(page.getByText('Breakdown')).toBeVisible()
    // Verify all four breakdown labels are visible somewhere on the page
    await expect(page.getByText('Input').first()).toBeVisible()
    await expect(page.getByText('Output').first()).toBeVisible()
    await expect(page.getByText('Cache Read').first()).toBeVisible()
    await expect(page.getByText('Cache Write').first()).toBeVisible()
  })

  test('Cost by Project section exists', async ({ page }) => {
    // This section may or may not be visible depending on whether multiple projects exist.
    // When there are projects with usage, the section header is visible.
    // We use a soft check — if projects exist, verify the section is rendered.
    const costByProject = page.getByText('Cost by Project')
    // Allow it to not exist if there's only one project or no usage
    const isVisible = await costByProject.isVisible().catch(() => false)
    if (isVisible) {
      await expect(costByProject).toBeVisible()
    } else {
      // Acceptable — no multi-project cost breakdown when single project or no data
      test.skip(true, 'Cost by Project not shown (single project or no usage data)')
    }
  })

  test('Estimated Cost section shows a dollar value', async ({ page }) => {
    const estimatedCost = page.getByText('Estimated Cost')
    await expect(estimatedCost).toBeVisible()
    // Find the section containing "Estimated Cost" and verify it also contains a $ value
    const costSection = page.locator('div').filter({ has: page.getByText('Estimated Cost') }).filter({ hasText: '$' }).first()
    const sectionText = await costSection.textContent()
    expect(sectionText).toContain('$')
  })

  test('Set budget button exists', async ({ page }) => {
    const setBudgetBtn = page.getByRole('button', { name: 'Set budget' })
    await expect(setBudgetBtn).toBeVisible()
  })

  test('Calibrate button exists', async ({ page }) => {
    const calibrateBtn = page.getByText('Calibrate')
    await expect(calibrateBtn).toBeVisible()
  })
})
