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
      // ADR-0054: mclaude-hosts key format is flat {hslug} (no user prefix)
      mockNats.kvSet('mclaude-hosts', 'host-1', { online: true })
      expect(monitor.isHealthy('host-1')).toBe(true)
    })

    it('returns false after online:false KV entry arrives', () => {
      monitor.start()
      mockNats.kvSet('mclaude-hosts', 'host-1', { online: false })
      expect(monitor.isHealthy('host-1')).toBe(false)
    })
  })

  describe('onHealthChanged', () => {
    it('fires when a host transitions from unknown to online', () => {
      monitor.start()
      const changes: Array<{ hostSlug: string; online: boolean }> = []
      monitor.onHealthChanged((hslug, online) => changes.push({ hostSlug: hslug, online }))

      // ADR-0054: key is just {hslug}, monitor extracts it directly
      mockNats.kvSet('mclaude-hosts', 'host-1', { online: true })

      expect(changes).toHaveLength(1)
      expect(changes[0]!.hostSlug).toBe('host-1')
      expect(changes[0]!.online).toBe(true)
    })

    it('fires when a host transitions from online to offline', () => {
      monitor.start()
      const changes: Array<{ hostSlug: string; online: boolean }> = []
      monitor.onHealthChanged((hslug, online) => changes.push({ hostSlug: hslug, online }))

      mockNats.kvSet('mclaude-hosts', 'host-1', { online: true })
      mockNats.kvSet('mclaude-hosts', 'host-1', { online: false })

      expect(changes).toHaveLength(2)
      expect(changes[1]!.hostSlug).toBe('host-1')
      expect(changes[1]!.online).toBe(false)
    })

    it('does not fire when online value does not change', () => {
      monitor.start()
      const changes: Array<{ online: boolean }> = []
      monitor.onHealthChanged((_, online) => changes.push({ online }))

      mockNats.kvSet('mclaude-hosts', 'host-1', { online: true })
      const countAfterFirst = changes.length

      // Same online value — no change event should fire
      mockNats.kvSet('mclaude-hosts', 'host-1', { online: true })
      expect(changes.length).toBe(countAfterFirst)
    })

    it('unsubscribe stops listener', () => {
      monitor.start()
      const changes: Array<unknown>[] = []
      const unsub = monitor.onHealthChanged(() => changes.push([]))

      unsub()

      mockNats.kvSet('mclaude-hosts', 'host-1', { online: true })
      expect(changes).toHaveLength(0)
    })
  })

  describe('stop', () => {
    it('stop() cancels the KV watcher — no further transitions', () => {
      monitor.start()
      const changes: Array<{ online: boolean }> = []
      monitor.onHealthChanged((_, online) => changes.push({ online }))

      mockNats.kvSet('mclaude-hosts', 'host-1', { online: true })

      monitor.stop()

      // Further KV updates should not trigger listeners after stop
      mockNats.kvSet('mclaude-hosts', 'host-1', { online: false })
      expect(changes.every(c => c.online === true)).toBe(true)
    })
  })

  // ── ADR-0054 regression ───────────────────────────────────────────────────
  // ADR-0054 changed mclaude-hosts KV key format: key is now flat {hslug}
  // (no user slug prefix). The HeartbeatMonitor watches with '>' wildcard
  // and extracts hostSlug directly from the key.
  describe('ADR-0054 regression — HeartbeatMonitor uses flat {hslug} key format', () => {
    const userId = '550e8400-e29b-41d4-a716-446655440000'
    const userSlug = 'dev'

    it('receives host data with flat key format (no user prefix)', () => {
      const nats = new MockNATSClient()
      const hb = new HeartbeatMonitor(nats, userId, userSlug)
      hb.start()

      // ADR-0054: key is just {hslug}, no user slug prefix
      nats.kvSet('mclaude-hosts', 'host-1', { online: true })
      expect(hb.isHealthy('host-1')).toBe(true)

      hb.stop()
    })

    it('correctly extracts hostSlug from flat key', () => {
      const nats = new MockNATSClient()
      const hb = new HeartbeatMonitor(nats, userId, userSlug)
      hb.start()

      const changes: Array<{ hostSlug: string; online: boolean }> = []
      hb.onHealthChanged((hslug, online) => changes.push({ hostSlug: hslug, online }))

      nats.kvSet('mclaude-hosts', 'laptop-a', { online: true })
      expect(changes[0]?.hostSlug).toBe('laptop-a')

      hb.stop()
    })

    it('works regardless of which userId/userSlug is passed (bucket is shared)', () => {
      const nats = new MockNATSClient()
      // Both a UUID-constructed and slug-constructed monitor watch the same shared bucket
      const hbSlug = new HeartbeatMonitor(nats, userId, userSlug)
      const hbUuid = new HeartbeatMonitor(nats, userId)
      hbSlug.start()
      hbUuid.start()

      nats.kvSet('mclaude-hosts', 'host-1', { online: true })
      // Both monitors see the same flat key
      expect(hbSlug.isHealthy('host-1')).toBe(true)
      expect(hbUuid.isHealthy('host-1')).toBe(true)

      hbSlug.stop()
      hbUuid.stop()
    })
  })
})
