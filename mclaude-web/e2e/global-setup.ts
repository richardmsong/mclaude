import { FullConfig } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import * as crypto from 'crypto'
import { fileURLToPath } from 'url'
import { connect, jwtAuthenticator } from 'nats.ws'

function slugifyEmail(email: string): string {
  return email
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 63)
    .replace(/-+$/, '')
}

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

  const baseURL = process.env['BASE_URL'] || 'https://dev.mclaude.richardmcsong.com'
  const adminToken = process.env['ADMIN_TOKEN'] || 'dev-admin-token'
  const userId = crypto.randomUUID()
  const email = `e2e-${Date.now()}@mclaude.local`
  const token = crypto.randomBytes(8).toString('hex')

  // Step 4: Create test user via admin API
  const res = await fetch(`${adminURL}/admin/users`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${adminToken}`,
    },
    body: JSON.stringify({ id: userId, email, name: 'E2E Test User', password: token }),
  })

  if (!res.ok) {
    const body = await res.text()
    throw new Error(`global-setup: POST /admin/users failed ${res.status}: ${body}`)
  }

  // Step 5: Propagate credentials to worker processes via environment
  process.env['DEV_EMAIL'] = email
  process.env['DEV_TOKEN'] = token
  const uslug = slugifyEmail(email)
  process.env['DEV_USER_SLUG'] = uslug

  // Step 6: Login to obtain JWT/nkeySeed for NATS authentication
  const loginRes = await fetch(`${baseURL}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password: token }),
  })
  if (!loginRes.ok) {
    const body = await loginRes.text()
    throw new Error(`global-setup: login failed ${loginRes.status}: ${body}`)
  }
  const loginBody = await loginRes.json() as { jwt: string; nkeySeed: string; natsUrl: string; userSlug: string }
  const { jwt, nkeySeed } = loginBody

  // Derive NATS WebSocket URL: use natsUrl from response if non-empty, otherwise derive from BASE_URL
  const natsWsUrl = loginBody.natsUrl
    ? loginBody.natsUrl
    : baseURL.replace(/^https:\/\//, 'wss://') + '/nats'

  const seed = new TextEncoder().encode(nkeySeed)
  const nc = await connect({ servers: [natsWsUrl], authenticator: jwtAuthenticator(jwt, seed) })

  try {
    // Step 7: Seed project via NATS request
    const projectSubject = `mclaude.users.${uslug}.hosts.local.api.projects.create`
    const projectPayload = new TextEncoder().encode(JSON.stringify({ name: 'e2e-default', hostSlug: 'local' }))
    const projectReply = await nc.request(projectSubject, projectPayload, { timeout: 30000 })
    const projectReplyText = new TextDecoder().decode(projectReply.data)
    if (projectReplyText) {
      let replyObj: Record<string, unknown>
      try {
        replyObj = JSON.parse(projectReplyText) as Record<string, unknown>
      } catch {
        replyObj = {}
      }
      if (replyObj['error']) {
        throw new Error(`global-setup: project create failed: ${String(replyObj['error'])}`)
      }
    }

    // Wait up to 30s for project to appear in KV
    const projectSlug = await waitForKVEntry(
      nc,
      `mclaude-projects-${uslug}`,
      'hosts.local.projects.',
      30000,
      'global-setup: timed out waiting for project KV entry',
      (key, value) => {
        // Extract slug from the KV entry JSON value
        try {
          const obj = JSON.parse(new TextDecoder().decode(value)) as Record<string, unknown>
          if (typeof obj['slug'] === 'string') return obj['slug']
        } catch {
          // ignore parse errors, keep waiting
        }
        // Fall back to deriving from the key
        const parts = key.split('.')
        return parts[parts.length - 1] ?? null
      },
    )

    process.env['DEV_PROJECT_SLUG'] = projectSlug

    // Step 8: Seed session via NATS publish
    const sessionSubject = `mclaude.users.${uslug}.hosts.local.projects.${projectSlug}.sessions.create`
    const sessionPayload = new TextEncoder().encode(JSON.stringify({}))
    nc.publish(sessionSubject, sessionPayload)

    // Wait up to 60s for session to appear in KV
    const sessionPrefix = `hosts.local.projects.${projectSlug}.sessions.`
    const sessionSlug = await waitForKVEntry(
      nc,
      `mclaude-sessions-${uslug}`,
      sessionPrefix,
      60000,
      'global-setup: timed out waiting for session KV entry — check kubectl get pods -n mclaude-system for Pending pods',
      (key, _value) => {
        // Session slug is the last dot-delimited segment of the key
        const parts = key.split('.')
        return parts[parts.length - 1] ?? null
      },
    )

    process.env['DEV_SESSION_SLUG'] = sessionSlug

    // Step 9: Write extended credentials file
    fs.writeFileSync(TEST_USER_FILE, JSON.stringify({ userId, email, token, projectSlug, sessionSlug }))
  } finally {
    await nc.close()
  }
}

/**
 * Watch a NATS KV bucket for a key with the given prefix and extract a slug from the first matching entry.
 * The extractor receives (key, value) and returns the slug string, or null to skip the entry.
 * Throws with timeoutMessage if no matching entry appears within timeoutMs.
 */
async function waitForKVEntry(
  nc: Awaited<ReturnType<typeof connect>>,
  bucket: string,
  keyPrefix: string,
  timeoutMs: number,
  timeoutMessage: string,
  extractor: (key: string, value: Uint8Array) => string | null,
): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(timeoutMessage)), timeoutMs)

    ;(async () => {
      try {
        const js = nc.jetstream()
        const kv = await js.views.kv(bucket)
        const iter = await kv.watch({ key: '>' })
        for await (const entry of iter) {
          if (entry.operation === 'DEL' || entry.operation === 'PURGE') continue
          if (!entry.key.startsWith(keyPrefix)) continue
          const result = extractor(entry.key, entry.value)
          if (result !== null) {
            clearTimeout(timer)
            const stoppable = iter as unknown as { stop: () => void }
            stoppable.stop()
            resolve(result)
            return
          }
        }
      } catch (err) {
        clearTimeout(timer)
        reject(err)
      }
    })()
  })
}
