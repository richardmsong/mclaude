import { FullConfig } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import { fileURLToPath } from 'url'
import { createUser } from 'nkeys.js'

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

  if (!adminURL) {
    console.warn('global-teardown: ADMIN_URL is not set — cannot delete test user')
    return
  }

  // Step 3: Login as bootstrap admin to obtain adminJWT.
  // ADR-0100: generate NKey pair and send nkey_public in login request body.
  let adminJWT: string
  try {
    const adminKp = createUser()
    const adminNkeyPublic = adminKp.getPublicKey()
    adminKp.clear()

    const loginRes = await fetch(`${adminURL}/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: process.env['BOOTSTRAP_ADMIN_EMAIL'],
        password: process.env['BOOTSTRAP_ADMIN_PASSWORD'],
        nkey_public: adminNkeyPublic,
      }),
    })
    if (!loginRes.ok) {
      const body = await loginRes.text()
      console.warn(`global-teardown: admin login failed ${loginRes.status}: ${body} — cannot delete test user`)
      return
    }
    const loginBody = await loginRes.json() as { jwt: string }
    adminJWT = loginBody.jwt
  } catch (err) {
    console.warn(`global-teardown: admin login error: ${err} — cannot delete test user`)
    return
  }

  // Step 4: Delete the test user
  try {
    const res = await fetch(`${adminURL}/admin/users/${userId}`, {
      method: 'DELETE',
      headers: { 'Authorization': `Bearer ${adminJWT}` },
    })
    if (!res.ok && res.status !== 404) {
      console.warn(`global-teardown: DELETE /admin/users/${userId} returned ${res.status}`)
    }
  } catch (err) {
    console.warn(`global-teardown: failed to delete test user: ${err}`)
  }
}
