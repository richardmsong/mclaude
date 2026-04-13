import { describe, it, expect, beforeEach } from 'vitest'
import { SessionListVM } from './session-list-vm'
import { SessionStore } from '../stores/session-store'
import { HeartbeatMonitor } from '../stores/heartbeat-monitor'
import { MockNATSClient } from '../testutil/mock-nats'
import { makeSessionKVState, makeProjectKVState } from '../testutil/fixtures'

const enc = new TextEncoder()
const dec = new TextDecoder()

function parsePublished(data: Uint8Array): unknown {
  return JSON.parse(dec.decode(data))
}

describe('SessionListVM', () => {
  let mockNats: MockNATSClient
  let sessionStore: SessionStore
  let heartbeat: HeartbeatMonitor
  let vm: SessionListVM

  beforeEach(() => {
    mockNats = new MockNATSClient()
    sessionStore = new SessionStore(mockNats, 'user-1')
    sessionStore.startWatching()
    heartbeat = new HeartbeatMonitor(mockNats, 'user-1')
    vm = new SessionListVM(sessionStore, heartbeat, mockNats, 'user-1')
  })

  describe('projects getter', () => {
    it('returns empty array when no projects in store', () => {
      expect(vm.projects).toEqual([])
    })

    it('returns projects with their sessions', () => {
      mockNats.kvSet('mclaude-projects', 'user-1.project-1', makeProjectKVState({ id: 'project-1', name: 'Alpha' }))
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', makeSessionKVState({ id: 'session-1', projectId: 'project-1' }))

      const projects = vm.projects
      expect(projects).toHaveLength(1)
      expect(projects[0]!.id).toBe('project-1')
      expect(projects[0]!.name).toBe('Alpha')
      expect(projects[0]!.sessions).toHaveLength(1)
      expect(projects[0]!.sessions[0]!.id).toBe('session-1')
    })

    it('maps cwd from SessionKVState to SessionVM', () => {
      mockNats.kvSet('mclaude-projects', 'user-1.project-1', makeProjectKVState({ id: 'project-1', name: 'Alpha' }))
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', makeSessionKVState({
        id: 'session-1',
        projectId: 'project-1',
        cwd: '/home/user/work/myproject',
      }))

      const session = vm.projects[0]!.sessions[0]!
      expect(session.cwd).toBe('/home/user/work/myproject')
    })

    it('P6: healthy is false when no heartbeat seen', () => {
      mockNats.kvSet('mclaude-projects', 'user-1.project-1', makeProjectKVState({ id: 'project-1', name: 'Alpha' }))
      heartbeat.start()
      const project = vm.projects[0]!
      expect(project.healthy).toBe(false)
    })

    it('P6: healthy is true after recent heartbeat arrives', () => {
      mockNats.kvSet('mclaude-projects', 'user-1.project-1', makeProjectKVState({ id: 'project-1', name: 'Alpha' }))
      heartbeat.start()
      const now = new Date().toISOString()
      mockNats.kvSet('mclaude-heartbeats', 'user-1.project-1', { ts: now })
      expect(vm.projects[0]!.healthy).toBe(true)
    })
  })

  describe('createProject', () => {
    it('publishes to mclaude.{userId}.api.projects.create and returns project id', async () => {
      mockNats.requestHandlers.set(
        'mclaude.user-1.api.projects.create',
        () => enc.encode(JSON.stringify({ id: 'proj-new' }))
      )

      const projectId = await vm.createProject('My Project')
      expect(projectId).toBe('proj-new')

      const req = mockNats.requests.find(r => r.subject === 'mclaude.user-1.api.projects.create')
      expect(req).toBeDefined()
      expect(parsePublished(req!.data)).toMatchObject({ name: 'My Project' })
    })

    it('includes gitUrl in payload when provided', async () => {
      mockNats.requestHandlers.set(
        'mclaude.user-1.api.projects.create',
        () => enc.encode(JSON.stringify({ id: 'proj-cloned' }))
      )

      const projectId = await vm.createProject('Cloned', 'https://github.com/org/repo')
      expect(projectId).toBe('proj-cloned')

      const req = mockNats.requests.find(r => r.subject === 'mclaude.user-1.api.projects.create')
      expect(parsePublished(req!.data)).toMatchObject({
        name: 'Cloned',
        gitUrl: 'https://github.com/org/repo',
      })
    })

    it('omits gitUrl from payload when not provided', async () => {
      mockNats.requestHandlers.set(
        'mclaude.user-1.api.projects.create',
        () => enc.encode(JSON.stringify({ id: 'proj-scratch' }))
      )

      await vm.createProject('Scratch')

      const req = mockNats.requests.find(r => r.subject === 'mclaude.user-1.api.projects.create')
      const payload = parsePublished(req!.data) as Record<string, unknown>
      expect('gitUrl' in payload).toBe(false)
    })
  })

  describe('createSession', () => {
    it('publishes to mclaude.{userId}.{projectId}.api.sessions.create and returns session id', async () => {
      mockNats.requestHandlers.set(
        'mclaude.user-1.project-1.api.sessions.create',
        () => enc.encode(JSON.stringify({ id: 'sess-new' }))
      )

      const sessionId = await vm.createSession('project-1', 'main', 'My Session')
      expect(sessionId).toBe('sess-new')

      const req = mockNats.requests.find(r => r.subject === 'mclaude.user-1.project-1.api.sessions.create')
      expect(req).toBeDefined()
      expect(parsePublished(req!.data)).toMatchObject({
        projectId: 'project-1',
        branch: 'main',
        name: 'My Session',
      })
    })
  })

  describe('deleteSession', () => {
    it('publishes to the session project delete subject', async () => {
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', makeSessionKVState({
        id: 'session-1', projectId: 'project-1',
      }))
      sessionStore.startWatching()

      await vm.deleteSession('session-1')

      const req = mockNats.requests.find(r => r.subject === 'mclaude.user-1.project-1.api.sessions.delete')
      expect(req).toBeDefined()
      expect(parsePublished(req!.data)).toMatchObject({ sessionId: 'session-1' })
    })

    it('does nothing when session not found', async () => {
      await vm.deleteSession('nonexistent')
      expect(mockNats.requests).toHaveLength(0)
    })
  })

  describe('onProjectsChanged', () => {
    it('fires listener when session KV changes', () => {
      const calls: number[] = []
      vm.onProjectsChanged(projects => calls.push(projects.length))

      mockNats.kvSet('mclaude-projects', 'user-1.project-1', makeProjectKVState())
      expect(calls.length).toBeGreaterThan(0)
    })

    it('unsubscribe stops listener', () => {
      const calls: number[] = []
      const unsub = vm.onProjectsChanged(() => calls.push(1))
      unsub()

      mockNats.kvSet('mclaude-projects', 'user-1.project-1', makeProjectKVState())
      expect(calls).toHaveLength(0)
    })
  })

  describe('destroy', () => {
    it('stops all listeners after destroy', () => {
      const calls: number[] = []
      vm.onProjectsChanged(() => calls.push(1))
      vm.destroy()

      mockNats.kvSet('mclaude-projects', 'user-1.project-1', makeProjectKVState())
      expect(calls).toHaveLength(0)
    })
  })
})
