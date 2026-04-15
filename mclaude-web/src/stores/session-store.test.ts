import { describe, it, expect, beforeEach } from 'vitest'
import { SessionStore } from './session-store'
import { MockNATSClient } from '../testutil/mock-nats'
import { makeSessionKVState, makeProjectKVState } from '../testutil/fixtures'

describe('SessionStore', () => {
  let mockNats: MockNATSClient
  let store: SessionStore

  beforeEach(() => {
    mockNats = new MockNATSClient()
    store = new SessionStore(mockNats, 'user-1')
    store.startWatching()
  })

  describe('KV watch → SessionState updates', () => {
    it('updates sessions map when kvSet is called for a session', () => {
      const sessionState = makeSessionKVState({ id: 'session-1', projectId: 'project-1' })
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', sessionState)
      const result = store.sessions.get('session-1')
      expect(result).toBeDefined()
      expect(result?.id).toBe('session-1')
      expect(result?.projectId).toBe('project-1')
    })

    it('updates session with correct fields', () => {
      const sessionState = makeSessionKVState({
        id: 'session-2',
        projectId: 'project-2',
        name: 'Custom Name',
        state: 'running',
      })
      mockNats.kvSet('mclaude-sessions', 'user-1.project-2.session-2', sessionState)
      const result = store.sessions.get('session-2')
      expect(result?.name).toBe('Custom Name')
      expect(result?.state).toBe('running')
    })
  })

  describe('multiple sessions', () => {
    it('contains both sessions in the map after setting two', () => {
      const session1 = makeSessionKVState({ id: 'session-1', projectId: 'project-1' })
      const session2 = makeSessionKVState({ id: 'session-2', projectId: 'project-1' })
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', session1)
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-2', session2)
      expect(store.sessions.get('session-1')).toBeDefined()
      expect(store.sessions.get('session-2')).toBeDefined()
      expect(store.sessions.size).toBe(2)
    })
  })

  describe('onSessionChanged listener', () => {
    it('fires when a KV update arrives for sessions', () => {
      let callCount = 0
      store.onSessionChanged(() => { callCount++ })
      const sessionState = makeSessionKVState()
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', sessionState)
      expect(callCount).toBe(1)
    })

    it('fires multiple times for multiple updates', () => {
      const calls: number[] = []
      store.onSessionChanged((sessions) => calls.push(sessions.size))
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', makeSessionKVState({ id: 'session-1' }))
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-2', makeSessionKVState({ id: 'session-2' }))
      expect(calls).toEqual([1, 2])
    })

    it('unsubscribe stops listener', () => {
      let callCount = 0
      const unsub = store.onSessionChanged(() => { callCount++ })
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', makeSessionKVState())
      unsub()
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-2', makeSessionKVState({ id: 'session-2' }))
      expect(callCount).toBe(1)
    })
  })

  describe('getSessionsForProject', () => {
    it('returns only sessions for the specified projectId', () => {
      const s1 = makeSessionKVState({ id: 'session-1', projectId: 'project-1' })
      const s2 = makeSessionKVState({ id: 'session-2', projectId: 'project-1' })
      const s3 = makeSessionKVState({ id: 'session-3', projectId: 'project-2' })
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', s1)
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-2', s2)
      mockNats.kvSet('mclaude-sessions', 'user-1.project-2.session-3', s3)
      const project1Sessions = store.getSessionsForProject('project-1')
      expect(project1Sessions).toHaveLength(2)
      expect(project1Sessions.map(s => s.id)).toContain('session-1')
      expect(project1Sessions.map(s => s.id)).toContain('session-2')
      const project2Sessions = store.getSessionsForProject('project-2')
      expect(project2Sessions).toHaveLength(1)
      expect(project2Sessions[0].id).toBe('session-3')
    })

    it('returns empty array when no sessions for project', () => {
      const result = store.getSessionsForProject('nonexistent-project')
      expect(result).toHaveLength(0)
    })
  })

  describe('Project KV watch', () => {
    it('updates projects map when kvSet called for a project', () => {
      const projectState = makeProjectKVState({ id: 'project-1', name: 'My Project' })
      mockNats.kvSet('mclaude-projects', 'user-1.project-1', projectState)
      const result = store.projects.get('project-1')
      expect(result).toBeDefined()
      expect(result?.id).toBe('project-1')
      expect(result?.name).toBe('My Project')
    })

    it('contains multiple projects after setting two', () => {
      const p1 = makeProjectKVState({ id: 'project-1' })
      const p2 = makeProjectKVState({ id: 'project-2' })
      mockNats.kvSet('mclaude-projects', 'user-1.project-1', p1)
      mockNats.kvSet('mclaude-projects', 'user-1.project-2', p2)
      expect(store.projects.size).toBe(2)
    })

    it('onProjectChanged fires on project update', () => {
      let callCount = 0
      store.onProjectChanged(() => { callCount++ })
      mockNats.kvSet('mclaude-projects', 'user-1.project-1', makeProjectKVState())
      expect(callCount).toBe(1)
    })
  })

  describe('KV DEL → session removal', () => {
    it('removes session from map on DEL operation', () => {
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', makeSessionKVState({ id: 'session-1', projectId: 'project-1' }))
      expect(store.sessions.has('session-1')).toBe(true)

      mockNats.kvDelete('mclaude-sessions', 'user-1.project-1.session-1')
      expect(store.sessions.has('session-1')).toBe(false)
    })

    it('notifies session listeners on DEL', () => {
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', makeSessionKVState({ id: 'session-1', projectId: 'project-1' }))

      const calls: number[] = []
      store.onSessionChanged(sessions => calls.push(sessions.size))

      mockNats.kvDelete('mclaude-sessions', 'user-1.project-1.session-1')
      expect(calls).toEqual([0])
    })
  })

  describe('onSessionAdded', () => {
    it('fires callback when a new session appears in KV after registration', () => {
      const added: string[] = []
      store.onSessionAdded('project-1', session => added.push(session.id))

      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', makeSessionKVState({ id: 'session-1', projectId: 'project-1' }))
      expect(added).toEqual(['session-1'])
    })

    it('does NOT fire for sessions already known at registration time', () => {
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-existing', makeSessionKVState({ id: 'session-existing', projectId: 'project-1' }))

      const added: string[] = []
      store.onSessionAdded('project-1', session => added.push(session.id))

      // Re-delivering the same session (e.g. KV update) should not trigger onSessionAdded
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-existing', makeSessionKVState({ id: 'session-existing', projectId: 'project-1', state: 'running' }))
      expect(added).toHaveLength(0)
    })

    it('does NOT fire for sessions in a different project', () => {
      const added: string[] = []
      store.onSessionAdded('project-1', session => added.push(session.id))

      mockNats.kvSet('mclaude-sessions', 'user-1.project-2.session-other', makeSessionKVState({ id: 'session-other', projectId: 'project-2' }))
      expect(added).toHaveLength(0)
    })

    it('unsubscribe stops listener from firing', () => {
      const added: string[] = []
      const unsub = store.onSessionAdded('project-1', session => added.push(session.id))
      unsub()

      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', makeSessionKVState({ id: 'session-1', projectId: 'project-1' }))
      expect(added).toHaveLength(0)
    })
  })
})
