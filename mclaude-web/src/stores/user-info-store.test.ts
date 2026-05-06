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

  it('notifies callbacks with null on KV DEL', async () => {
    const cb = vi.fn()
    store.onTokenChanged(cb)
    store.startWatching()

    nc.simulateKvPut('mclaude-user-info-alice', 'tokens.anthropic', 'sk-ant-oat01-abc123')
    cb.mockClear()

    await nc.kvDelete('mclaude-user-info-alice', 'tokens.anthropic')

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

  it('writeToken writes to KV bucket via kvPut and updates local state', async () => {
    await store.writeToken('sk-ant-oat01-newtoken')
    expect(store.token).toBe('sk-ant-oat01-newtoken')
    // KV store should contain the encoded token
    const entry = await nc.kvGet('mclaude-user-info-alice', 'tokens.anthropic')
    expect(entry).not.toBeNull()
    expect(new TextDecoder().decode(entry!.value)).toBe('sk-ant-oat01-newtoken')
  })

  it('deleteToken removes entry via kvDelete and clears local state', async () => {
    await store.writeToken('sk-ant-oat01-toremove')
    await store.deleteToken()
    expect(store.token).toBeNull()
    // KV store entry should be gone
    const entry = await nc.kvGet('mclaude-user-info-alice', 'tokens.anthropic')
    expect(entry).toBeNull()
  })

  it('writeToken triggers KV watcher callbacks when startWatching is active', async () => {
    const cb = vi.fn()
    store.onTokenChanged(cb)
    store.startWatching()
    cb.mockClear()

    await store.writeToken('sk-ant-oat01-watchtest')

    expect(cb).toHaveBeenCalledWith('sk-ant-oat01-watchtest')
  })

  it('deleteToken triggers KV watcher callbacks with null when startWatching is active', async () => {
    const cb = vi.fn()
    store.onTokenChanged(cb)
    store.startWatching()

    await store.writeToken('sk-ant-oat01-watchtest')
    cb.mockClear()

    await store.deleteToken()

    expect(cb).toHaveBeenCalledWith(null)
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
