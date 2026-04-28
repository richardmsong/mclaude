import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { HeartbeatMonitor } from './heartbeat-monitor'
import { MockNATSClient } from '../testutil/mock-nats'

describe('HeartbeatMonitor', () => {
  let mockNats: MockNATSClient
  let monitor: HeartbeatMonitor

  beforeEach(() => {
    mockNats = new MockNATSClient()
    monitor = new HeartbeatMonitor(mockNats, 'user-1')
  })

  afterEach(() => {
    monitor.stop()
  })

  describe('isHealthy', () => {
    it('returns false when no heartbeat seen', () => {
      monitor.start()
      expect(monitor.isHealthy('host-1')).toBe(false)
    })

    it('returns true after online:true KV entry arrives', () => {
      monitor.start()
      mockNats.kvSet('mclaude-hosts', 'user-1.host-1', { online: true })
      expect(monitor.isHealthy('host-1')).toBe(true)
    })

    it('returns false after online:false KV entry arrives', () => {
      monitor.start()
      mockNats.kvSet('mclaude-hosts', 'user-1.host-1', { online: false })
      expect(monitor.isHealthy('host-1')).toBe(false)
    })
  })

  describe('onHealthChanged', () => {
    it('fires when a host transitions from unknown to online', () => {
      monitor.start()
      const changes: Array<{ hostSlug: string; online: boolean }> = []
      monitor.onHealthChanged((hslug, online) => changes.push({ hostSlug: hslug, online }))

      mockNats.kvSet('mclaude-hosts', 'user-1.host-1', { online: true })

      expect(changes).toHaveLength(1)
      expect(changes[0]!.hostSlug).toBe('host-1')
      expect(changes[0]!.online).toBe(true)
    })

    it('fires when a host transitions from online to offline', () => {
      monitor.start()
      const changes: Array<{ hostSlug: string; online: boolean }> = []
      monitor.onHealthChanged((hslug, online) => changes.push({ hostSlug: hslug, online }))

      mockNats.kvSet('mclaude-hosts', 'user-1.host-1', { online: true })
      mockNats.kvSet('mclaude-hosts', 'user-1.host-1', { online: false })

      expect(changes).toHaveLength(2)
      expect(changes[1]!.hostSlug).toBe('host-1')
      expect(changes[1]!.online).toBe(false)
    })

    it('does not fire when online value does not change', () => {
      monitor.start()
      const changes: Array<{ online: boolean }> = []
      monitor.onHealthChanged((_, online) => changes.push({ online }))

      mockNats.kvSet('mclaude-hosts', 'user-1.host-1', { online: true })
      const countAfterFirst = changes.length

      // Same online value — no change event should fire
      mockNats.kvSet('mclaude-hosts', 'user-1.host-1', { online: true })
      expect(changes.length).toBe(countAfterFirst)
    })

    it('unsubscribe stops listener', () => {
      monitor.start()
      const changes: Array<unknown>[] = []
      const unsub = monitor.onHealthChanged(() => changes.push([]))

      unsub()

      mockNats.kvSet('mclaude-hosts', 'user-1.host-1', { online: true })
      expect(changes).toHaveLength(0)
    })
  })

  describe('stop', () => {
    it('stop() cancels the KV watcher — no further transitions', () => {
      monitor.start()
      const changes: Array<{ online: boolean }> = []
      monitor.onHealthChanged((_, online) => changes.push({ online }))

      mockNats.kvSet('mclaude-hosts', 'user-1.host-1', { online: true })

      monitor.stop()

      // Further KV updates should not trigger listeners after stop
      mockNats.kvSet('mclaude-hosts', 'user-1.host-1', { online: false })
      expect(changes.every(c => c.online === true)).toBe(true)
    })
  })

  // ── ADR-0048 regression ───────────────────────────────────────────────────
  // App.tsx passes authState.userSlug ?? authState.userId to HeartbeatMonitor
  // so that it watches slug-keyed mclaude-hosts entries (e.g. "dev.local")
  // written by the control-plane per ADR-0046.
  describe('ADR-0048 regression — HeartbeatMonitor uses userSlug (not userId) for mclaude-hosts KV prefix', () => {
    const userId = '550e8400-e29b-41d4-a716-446655440000'
    const userSlug = 'dev'

    it('watches with slug prefix when userSlug differs from userId', () => {
      const nats = new MockNATSClient()
      // App.tsx passes userSlug ?? userId as the third argument (ADR-0048)
      const hb = new HeartbeatMonitor(nats, userId, userSlug)
      hb.start()

      // Slug-prefixed key — must be received
      nats.kvSet('mclaude-hosts', `${userSlug}.host-1`, { online: true })
      expect(hb.isHealthy('host-1')).toBe(true)

      hb.stop()
    })

    it('does NOT receive UUID-prefixed host data when constructed with slug', () => {
      const nats = new MockNATSClient()
      // When userSlug='dev' is passed, mclaude-hosts watch pattern is 'dev.*'.
      // A UUID-prefixed entry like '550e8400-….host-1' must NOT match.
      const hb = new HeartbeatMonitor(nats, userId, userSlug)
      hb.start()

      nats.kvSet('mclaude-hosts', `${userId}.host-1`, { online: true })
      expect(hb.isHealthy('host-1')).toBe(false)

      hb.stop()
    })

    it('falls back to userId prefix when userSlug is not provided', () => {
      const nats = new MockNATSClient()
      // When no third arg is given, userSlug defaults to userId (UUID).
      const hb = new HeartbeatMonitor(nats, userId)
      hb.start()

      nats.kvSet('mclaude-hosts', `${userId}.host-1`, { online: true })
      expect(hb.isHealthy('host-1')).toBe(true)

      hb.stop()
    })
  })
})
