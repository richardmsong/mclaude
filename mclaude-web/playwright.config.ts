import { defineConfig, devices } from '@playwright/test'

const baseURL = process.env['BASE_URL'] || 'http://localhost:5173'
const isLive = baseURL.startsWith('https://')

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  forbidOnly: !!process.env['CI'],
  retries: 0,
  workers: 1,
  reporter: 'list',
  timeout: 60_000,
  expect: {
    timeout: 30_000,
  },
  use: {
    baseURL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'live',
      use: {
        ...devices['Desktop Chrome'],
        baseURL: 'https://dev.mclaude.richardmcsong.com',
      },
    },
  ],
  // Auto-start the Vite dev server for local e2e tests (skipped for live)
  ...(!isLive && {
    webServer: {
      command: 'npm run dev',
      url: 'http://localhost:5173',
      reuseExistingServer: true,
      timeout: 30_000,
    },
  }),
})
