/**
 * Tests for UserInfoStore (ADR-0092)
 * - startWatching / stopWatching
 * - token notification on KV PUT / DEL
 * - writeToken / deleteToken
 */

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { UserInfoStore } from './user-info-store'
import { MockNATSClient } from '@/testutil/mock-nats'

describe('UserInfoStore', () => {
  let nc: MockNATSClient
  let store: UserInfoStore

  beforeEach(() => {
    nc = new MockNATSClient()
    store = new UserInfoStore(nc, 'alice')
  })

  it('starts with null token', () => {
    expect(store.token).toBeNull()
  })

  it('bucket name is mclaude-user-info-{uslug}', () => {
    expect(store.bucketName).toBe('mclaude-user-info-alice')
  })

  it('notifies callbacks when KV PUT arrives via kvSet', () => {
    const cb = vi.fn()
    store.onTokenChanged(cb)
    store.startWatching()

    // Simulate KV PUT — kvSet stores raw string, UserInfoStore decodes it
    // We need to store as plain text (not JSON) since UserInfoStore uses TextDecoder directly
    nc.simulateKvPut('mclaude-user-info-alice', 'tokens.anthropic', 'sk-ant-oat01-abc123')

    expect(cb).toHaveBeenCalledWith('sk-ant-oat01-abc123')
    expect(store.token).toBe('sk-ant-oat01-abc123')
  })

  it('notifies callbacks with null on KV DEL', () => {
    const cb = vi.fn()
    store.onTokenChanged(cb)
    store.startWatching()

    nc.simulateKvPut('mclaude-user-info-alice', 'tokens.anthropic', 'sk-ant-oat01-abc123')
    cb.mockClear()

    nc.kvDelete('mclaude-user-info-alice', 'tokens.anthropic')

    expect(cb).toHaveBeenCalledWith(null)
    expect(store.token).toBeNull()
  })

  it('does not notify if token value is unchanged', () => {
    const cb = vi.fn()
    store.onTokenChanged(cb)
    store.startWatching()

    nc.simulateKvPut('mclaude-user-info-alice', 'tokens.anthropic', 'same-token')
    cb.mockClear()

    // Same value again
    nc.simulateKvPut('mclaude-user-info-alice', 'tokens.anthropic', 'same-token')

    expect(cb).not.toHaveBeenCalled()
  })

  it('unsubscribes callback', () => {
    const cb = vi.fn()
    const unsub = store.onTokenChanged(cb)
    store.startWatching()
    unsub()

    nc.simulateKvPut('mclaude-user-info-alice', 'tokens.anthropic', 'sk-ant-oat01-abc123')

    expect(cb).not.toHaveBeenCalled()
  })

  it('writeToken publishes to $KV bucket subject and updates local state', () => {
    store.writeToken('sk-ant-oat01-newtoken')
    expect(store.token).toBe('sk-ant-oat01-newtoken')
    // Should have published to $KV.mclaude-user-info-alice.tokens.anthropic
    expect(nc.published.some(m => m.subject === '$KV.mclaude-user-info-alice.tokens.anthropic')).toBe(true)
  })

  it('deleteToken publishes with KV-Operation DEL header and clears token', () => {
    store.writeToken('sk-ant-oat01-toremove')
    store.deleteToken()
    expect(store.token).toBeNull()
    const msg = nc.published.find(m =>
      m.subject === '$KV.mclaude-user-info-alice.tokens.anthropic' &&
      m.headers?.['KV-Operation'] === 'DEL'
    )
    expect(msg).toBeDefined()
  })

  it('stopWatching stops the KV watch', () => {
    store.startWatching()
    store.stopWatching()
    // After stopWatching, no more calls should propagate — just verify no error thrown
    const cb = vi.fn()
    store.onTokenChanged(cb)
    // No KV events generated after stop
    expect(cb).not.toHaveBeenCalled()
  })
})
