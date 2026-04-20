import { describe, it, expect, beforeEach } from 'vitest'
import { LifecycleStore } from './lifecycle-store'
import { MockNATSClient } from '../testutil/mock-nats'
import type { LifecycleEvent } from '@/types'

describe('LifecycleStore', () => {
  let mockNats: MockNATSClient
  let store: LifecycleStore

  beforeEach(() => {
    mockNats = new MockNATSClient()
    store = new LifecycleStore(mockNats, 'user-1', 'project-1')
  })

  describe('start / stop', () => {
    it('subscribes to the correct lifecycle subject', () => {
      store.start()
      // Verify subscription was created by simulating a lifecycle event
      const events: LifecycleEvent[] = []
      store.onLifecycleEvent(e => events.push(e))
      mockNats.simulateReceive(
        'mclaude.users.user-1.projects.project-1.lifecycle.session-1',
        { type: 'session_created', sessionId: 'session-1' } as unknown as LifecycleEvent,
      )
      expect(events).toHaveLength(1)
      store.stop()
    })

    it('stop() prevents further events from being delivered', () => {
      store.start()
      const events: LifecycleEvent[] = []
      store.onLifecycleEvent(e => events.push(e))

      mockNats.simulateReceive(
        'mclaude.users.user-1.projects.project-1.lifecycle.session-1',
        { type: 'session_created', sessionId: 'session-1' } as unknown as LifecycleEvent,
      )
      expect(events).toHaveLength(1)

      store.stop()

      mockNats.simulateReceive(
        'mclaude.users.user-1.projects.project-1.lifecycle.session-1',
        { type: 'session_stopped', sessionId: 'session-1' } as unknown as LifecycleEvent,
      )
      // No new events after stop
      expect(events).toHaveLength(1)
    })

    it('calling start() twice replaces the subscription (no duplicates)', () => {
      store.start()
      store.start() // re-start should unsubscribe old one first

      const events: LifecycleEvent[] = []
      store.onLifecycleEvent(e => events.push(e))

      mockNats.simulateReceive(
        'mclaude.users.user-1.projects.project-1.lifecycle.session-1',
        { type: 'session_created', sessionId: 'session-1' } as unknown as LifecycleEvent,
      )
      // Should only receive once, not twice
      expect(events).toHaveLength(1)
      store.stop()
    })
  })

  describe('onLifecycleEvent', () => {
    it('delivers lifecycle events to all listeners', () => {
      store.start()
      const events1: LifecycleEvent[] = []
      const events2: LifecycleEvent[] = []
      store.onLifecycleEvent(e => events1.push(e))
      store.onLifecycleEvent(e => events2.push(e))

      mockNats.simulateReceive(
        'mclaude.users.user-1.projects.project-1.lifecycle.session-1',
        { type: 'session_restarting', sessionId: 'session-1' } as unknown as LifecycleEvent,
      )

      expect(events1).toHaveLength(1)
      expect(events2).toHaveLength(1)
      store.stop()
    })

    it('unsubscribe stops listener from receiving events', () => {
      store.start()
      const events: LifecycleEvent[] = []
      const unsub = store.onLifecycleEvent(e => events.push(e))

      mockNats.simulateReceive(
        'mclaude.users.user-1.projects.project-1.lifecycle.session-1',
        { type: 'session_created', sessionId: 'session-1' } as unknown as LifecycleEvent,
      )
      expect(events).toHaveLength(1)

      unsub()

      mockNats.simulateReceive(
        'mclaude.users.user-1.projects.project-1.lifecycle.session-1',
        { type: 'session_stopped', sessionId: 'session-1' } as unknown as LifecycleEvent,
      )
      expect(events).toHaveLength(1) // no new events
      store.stop()
    })

    it('matches the wildcard subject for any session-id suffix', () => {
      store.start()
      const events: LifecycleEvent[] = []
      store.onLifecycleEvent(e => events.push(e))

      // Different session IDs under same project
      mockNats.simulateReceive(
        'mclaude.users.user-1.projects.project-1.lifecycle.session-abc',
        { type: 'session_created', sessionId: 'session-abc' } as unknown as LifecycleEvent,
      )
      mockNats.simulateReceive(
        'mclaude.users.user-1.projects.project-1.lifecycle.session-xyz',
        { type: 'session_stopped', sessionId: 'session-xyz' } as unknown as LifecycleEvent,
      )
      expect(events).toHaveLength(2)
      store.stop()
    })

    it('silently ignores malformed JSON events', () => {
      store.start()
      const events: LifecycleEvent[] = []
      store.onLifecycleEvent(e => events.push(e))

      // Publish raw malformed bytes
      mockNats.publish('mclaude.users.user-1.projects.project-1.lifecycle.session-1', new TextEncoder().encode('not-json'))
      // Should not throw, and no events delivered
      expect(events).toHaveLength(0)
      store.stop()
    })
  })
})
