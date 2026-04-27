import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { HeartbeatMonitor } from './heartbeat-monitor'
import { MockNATSClient } from '../testutil/mock-nats'

function makeHeartbeat(ts: string): object {
  return { ts }
}

describe('HeartbeatMonitor', () => {
  let mockNats: MockNATSClient
  let monitor: HeartbeatMonitor

  beforeEach(() => {
    vi.useFakeTimers()
    mockNats = new MockNATSClient()
    // Use a 60s threshold (default)
    monitor = new HeartbeatMonitor(mockNats, 'user-1', 60_000)
  })

  afterEach(() => {
    monitor.stop()
    vi.useRealTimers()
  })

  describe('isHealthy', () => {
    it('returns false when no heartbeat seen', () => {
      monitor.start()
      expect(monitor.isHealthy('project-1')).toBe(false)
    })

    it('returns true immediately after a recent heartbeat', () => {
      monitor.start()
      const now = new Date().toISOString()
      mockNats.kvSet('mclaude-hosts', 'user-1.project-1', makeHeartbeat(now))
      expect(monitor.isHealthy('project-1')).toBe(true)
    })

    it('returns false when heartbeat is older than threshold', () => {
      monitor.start()
      // Timestamp 90s in the past
      const old = new Date(Date.now() - 90_000).toISOString()
      mockNats.kvSet('mclaude-hosts', 'user-1.project-1', makeHeartbeat(old))
      expect(monitor.isHealthy('project-1')).toBe(false)
    })
  })

  describe('onHealthChanged', () => {
    it('fires when a project transitions from unknown to healthy', () => {
      monitor.start()
      const changes: Array<{ projectId: string; healthy: boolean }> = []
      monitor.onHealthChanged((pid, h) => changes.push({ projectId: pid, healthy: h }))

      const now = new Date().toISOString()
      mockNats.kvSet('mclaude-hosts', 'user-1.project-1', makeHeartbeat(now))

      expect(changes).toHaveLength(1)
      expect(changes[0]!.projectId).toBe('project-1')
      expect(changes[0]!.healthy).toBe(true)
    })

    it('fires when a healthy project transitions to unhealthy on check interval', async () => {
      monitor.start(1000) // 1s check interval
      const changes: Array<{ projectId: string; healthy: boolean }> = []
      monitor.onHealthChanged((pid, h) => changes.push({ projectId: pid, healthy: h }))

      // Start with a recent heartbeat — healthy
      const now = new Date().toISOString()
      mockNats.kvSet('mclaude-hosts', 'user-1.project-1', makeHeartbeat(now))
      expect(changes.find(c => c.healthy === true)).toBeDefined()

      // Advance time past threshold (60s) + check interval (1s)
      await vi.advanceTimersByTimeAsync(61_000 + 1_100)

      const unhealthyChange = changes.find(c => c.healthy === false)
      expect(unhealthyChange).toBeDefined()
      expect(unhealthyChange?.projectId).toBe('project-1')
    })

    it('does not fire when health does not change', () => {
      monitor.start(1000)
      const changes: Array<{ healthy: boolean }> = []
      monitor.onHealthChanged((_, h) => changes.push({ healthy: h }))

      const now = new Date().toISOString()
      mockNats.kvSet('mclaude-hosts', 'user-1.project-1', makeHeartbeat(now))
      const countAfterFirst = changes.length

      // Same timestamp — health should stay the same
      mockNats.kvSet('mclaude-hosts', 'user-1.project-1', makeHeartbeat(now))
      // No new change event since health didn't change (still healthy)
      expect(changes.length).toBe(countAfterFirst)
    })

    it('unsubscribe stops listener', () => {
      monitor.start()
      const changes: Array<unknown>[] = []
      const unsub = monitor.onHealthChanged(() => changes.push([]))

      unsub()

      const now = new Date().toISOString()
      mockNats.kvSet('mclaude-hosts', 'user-1.project-1', makeHeartbeat(now))
      expect(changes).toHaveLength(0)
    })
  })

  describe('stop', () => {
    it('stop() cancels the check interval — no further transitions', async () => {
      monitor.start(1000)
      const changes: Array<{ healthy: boolean }> = []
      monitor.onHealthChanged((_, h) => changes.push({ healthy: h }))

      const now = new Date().toISOString()
      mockNats.kvSet('mclaude-hosts', 'user-1.project-1', makeHeartbeat(now))

      monitor.stop()

      // Advance past threshold — check timer is cancelled so no unhealthy event
      await vi.advanceTimersByTimeAsync(120_000)
      expect(changes.every(c => c.healthy === true)).toBe(true)
    })
  })
})
