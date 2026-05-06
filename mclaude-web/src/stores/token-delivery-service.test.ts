/**
 * Tests for token-delivery-service.ts (ADR-0092)
 * - deliverToken: sends token_wrapped via NATS request/reply
 * - revokeTokenOnHost: publishes revoke request
 * - revokeTokenOnAllHosts: iterates accepted-grant hosts
 * - getAgentTokenStatus: queries agent.status subject
 * - retryPendingDeliveries: retries from localStorage
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { MockNATSClient } from '@/testutil/mock-nats'
import {
  deliverToken,
  revokeTokenOnHost,
  revokeTokenOnAllHosts,
  getAgentTokenStatus,
} from './token-delivery-service'

// Mock libsodium — just return a fixed base64 string
vi.mock('libsodium-wrappers', () => ({
  default: {
    ready: Promise.resolve(),
    from_base64: (_s: string, _v: number) => new Uint8Array(32),
    from_string: (s: string) => new TextEncoder().encode(s),
    crypto_box_seal: (_m: Uint8Array, _pk: Uint8Array) => new Uint8Array(64),
    to_base64: (_b: Uint8Array, _v: number) => 'SEALED_BOX_B64',
    base64_variants: { ORIGINAL: 0 },
  },
}))

function makeAuthClient(pubkey = 'AAAA', grants: Array<{ hostSlug: string; status: string }> = []) {
  return {
    getUserAgentPubkey: vi.fn().mockResolvedValue(pubkey),
    getGrants: vi.fn().mockResolvedValue(grants),
  } as unknown as import('@/transport/auth-client').AuthClient
}

describe('deliverToken', () => {
  let nc: MockNATSClient

  beforeEach(() => {
    nc = new MockNATSClient()
    // Default: request returns {ok: true}
    nc.requestHandlers.set(
      'mclaude.users.alice.hosts.my-host.agent.token-delivery',
      () => new TextEncoder().encode('{"ok":true}'),
    )
  })

  it('publishes token_wrapped to agent.token-delivery subject', async () => {
    const authClient = makeAuthClient()
    const result = await deliverToken(nc, authClient, 'alice', 'my-host', 'sk-ant-oat01-test', 'anthropic', 5000)
    expect(result.ok).toBe(true)
    expect(nc.requests.some(r => r.subject === 'mclaude.users.alice.hosts.my-host.agent.token-delivery')).toBe(true)
  })

  it('includes provider and token_wrapped in payload', async () => {
    const authClient = makeAuthClient()
    await deliverToken(nc, authClient, 'alice', 'my-host', 'sk-ant-oat01-test', 'anthropic', 5000)
    const req = nc.requests.find(r => r.subject === 'mclaude.users.alice.hosts.my-host.agent.token-delivery')
    expect(req).toBeDefined()
    const payload = JSON.parse(new TextDecoder().decode(req!.data)) as { provider: string; token_wrapped: string }
    expect(payload.provider).toBe('anthropic')
    expect(payload.token_wrapped).toBe('SEALED_BOX_B64')
  })

  it('returns ok:false when request throws', async () => {
    const failingNc = new MockNATSClient()
    // No handler set — request returns empty `{}` which won't throw but deliverToken should handle it
    // Override with a handler that returns an error
    failingNc.requestHandlers.set(
      'mclaude.users.alice.hosts.bad-host.agent.token-delivery',
      () => { throw new Error('timeout') },
    )
    const authClient = makeAuthClient()
    // Use the request override to simulate failure
    const original = failingNc.request.bind(failingNc)
    vi.spyOn(failingNc, 'request').mockRejectedValueOnce(new Error('NATS timeout'))
    const result = await deliverToken(failingNc, authClient, 'alice', 'bad-host', 'token', 'anthropic', 1)
    expect(result.ok).toBe(false)
    expect((result as { ok: false; error: string }).error).toBeDefined()
    original // satisfy lint
  })
})

describe('revokeTokenOnHost', () => {
  let nc: MockNATSClient

  beforeEach(() => {
    nc = new MockNATSClient()
    nc.requestHandlers.set(
      'mclaude.users.alice.hosts.my-host.agent.token-revoke',
      () => new TextEncoder().encode('{"ok":true}'),
    )
  })

  it('sends revoke request to agent.token-revoke subject', async () => {
    const result = await revokeTokenOnHost(nc, 'alice', 'my-host')
    expect(result.ok).toBe(true)
    expect(nc.requests.some(r => r.subject === 'mclaude.users.alice.hosts.my-host.agent.token-revoke')).toBe(true)
  })

  it('includes provider in payload', async () => {
    await revokeTokenOnHost(nc, 'alice', 'my-host', 'anthropic')
    const req = nc.requests.find(r => r.subject === 'mclaude.users.alice.hosts.my-host.agent.token-revoke')
    expect(req).toBeDefined()
    const payload = JSON.parse(new TextDecoder().decode(req!.data)) as { provider: string }
    expect(payload.provider).toBe('anthropic')
  })

  it('returns ok:false when user-agent reports error', async () => {
    nc.requestHandlers.set(
      'mclaude.users.alice.hosts.bad-host.agent.token-revoke',
      () => new TextEncoder().encode('{"ok":false,"error":"not found"}'),
    )
    const result = await revokeTokenOnHost(nc, 'alice', 'bad-host')
    expect(result.ok).toBe(false)
  })
})

describe('revokeTokenOnAllHosts', () => {
  let nc: MockNATSClient

  beforeEach(() => {
    nc = new MockNATSClient()
    // Default: both hosts respond ok
    nc.requestHandlers.set(
      'mclaude.users.alice.hosts.host-a.agent.token-revoke',
      () => new TextEncoder().encode('{"ok":true}'),
    )
    nc.requestHandlers.set(
      'mclaude.users.alice.hosts.host-b.agent.token-revoke',
      () => new TextEncoder().encode('{"ok":true}'),
    )
  })

  it('iterates all accepted-grant hosts', async () => {
    const authClient = makeAuthClient('AAAA', [
      { hostSlug: 'host-a', status: 'accepted' },
      { hostSlug: 'host-b', status: 'accepted' },
    ])
    const result = await revokeTokenOnAllHosts(nc, authClient, 'alice')
    expect(result.total).toBe(2)
    expect(result.succeeded).toBe(2)
    expect(result.failed).toHaveLength(0)
  })

  it('skips non-accepted grants', async () => {
    const authClient = makeAuthClient('AAAA', [
      { hostSlug: 'host-a', status: 'accepted' },
      { hostSlug: 'host-b', status: 'pending' },
    ])
    const result = await revokeTokenOnAllHosts(nc, authClient, 'alice')
    expect(result.total).toBe(1) // only accepted
    expect(result.succeeded).toBe(1)
  })

  it('reports failed hosts', async () => {
    nc.requestHandlers.set(
      'mclaude.users.alice.hosts.host-b.agent.token-revoke',
      () => new TextEncoder().encode('{"ok":false,"error":"offline"}'),
    )
    const authClient = makeAuthClient('AAAA', [
      { hostSlug: 'host-a', status: 'accepted' },
      { hostSlug: 'host-b', status: 'accepted' },
    ])
    const result = await revokeTokenOnAllHosts(nc, authClient, 'alice')
    expect(result.succeeded).toBe(1)
    expect(result.failed).toContain('host-b')
  })
})

describe('getAgentTokenStatus', () => {
  let nc: MockNATSClient

  beforeEach(() => {
    nc = new MockNATSClient()
    nc.requestHandlers.set(
      'mclaude.users.alice.hosts.my-host.agent.status',
      () => new TextEncoder().encode('{"hasToken":true,"tokenVersion":"a1b2c3d4"}'),
    )
  })

  it('returns token status from agent', async () => {
    const status = await getAgentTokenStatus(nc, 'alice', 'my-host')
    expect(status).not.toBeNull()
    expect(status!.hasToken).toBe(true)
    expect(status!.tokenVersion).toBe('a1b2c3d4')
  })

  it('returns null when request throws (offline host)', async () => {
    vi.spyOn(nc, 'request').mockRejectedValueOnce(new Error('timeout'))
    const status = await getAgentTokenStatus(nc, 'alice', 'offline-host')
    expect(status).toBeNull()
  })
})
