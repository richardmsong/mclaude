import { FullConfig } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import { fileURLToPath } from 'url'

const TEST_USER_FILE = path.join(path.dirname(fileURLToPath(import.meta.url)), '.test-user.json')

export default async function globalTeardown(_config: FullConfig) {
  if (!fs.existsSync(TEST_USER_FILE)) return

  const raw = fs.readFileSync(TEST_USER_FILE, 'utf-8')
  const record = JSON.parse(raw)

  // Delete the file eagerly before the API call — prevents re-read on a subsequent crashed run
  fs.rmSync(TEST_USER_FILE, { force: true })

  if (record.skipped) return

  const { userId } = record
  const adminURL = process.env['ADMIN_URL']
  const adminToken = process.env['ADMIN_TOKEN'] || 'dev-admin-token'

  if (!adminURL) {
    console.warn('global-teardown: ADMIN_URL is not set — cannot delete test user')
    return
  }

  try {
    const res = await fetch(`${adminURL}/admin/users/${userId}`, {
      method: 'DELETE',
      headers: { 'Authorization': `Bearer ${adminToken}` },
    })
    if (!res.ok && res.status !== 404) {
      console.warn(`global-teardown: DELETE /admin/users/${userId} returned ${res.status}`)
    }
  } catch (err) {
    console.warn(`global-teardown: failed to delete test user: ${err}`)
  }
}
