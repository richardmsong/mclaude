import { FullConfig } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import * as crypto from 'crypto'
import { fileURLToPath } from 'url'

const TEST_USER_FILE = path.join(path.dirname(fileURLToPath(import.meta.url)), '.test-user.json')

export default async function globalSetup(_config: FullConfig) {
  // If DEV_EMAIL and DEV_TOKEN are both set, use them directly (CI override)
  const devEmail = process.env['DEV_EMAIL']
  const devToken = process.env['DEV_TOKEN']
  if (devEmail && devToken) {
    fs.writeFileSync(TEST_USER_FILE, JSON.stringify({ skipped: true }))
    return
  }

  // If ADMIN_URL is not set, skip user creation — spec files fall back to hardcoded defaults
  const adminURL = process.env['ADMIN_URL']
  if (!adminURL) {
    fs.writeFileSync(TEST_USER_FILE, JSON.stringify({ skipped: true }))
    return
  }

  const adminToken = process.env['ADMIN_TOKEN'] || 'dev-admin-token'
  const email = `e2e-${Date.now()}@mclaude.local`
  const token = crypto.randomBytes(8).toString('hex')

  const res = await fetch(`${adminURL}/admin/users`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${adminToken}`,
    },
    body: JSON.stringify({ email, name: 'E2E Test User', password: token }),
  })

  if (!res.ok) {
    const body = await res.text()
    throw new Error(`global-setup: POST /admin/users failed ${res.status}: ${body}`)
  }

  const data = await res.json() as { id: string }

  // Propagate credentials to worker processes via environment
  process.env['DEV_EMAIL'] = email
  process.env['DEV_TOKEN'] = token

  fs.writeFileSync(TEST_USER_FILE, JSON.stringify({ userId: data.id, email, token }))
}
