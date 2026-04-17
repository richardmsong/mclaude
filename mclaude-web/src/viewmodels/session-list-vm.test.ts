import { describe, it, expect, beforeEach } from 'vitest'
import { SessionListVM } from './session-list-vm'
import type { IStorage } from './session-list-vm'
import { SessionStore } from '../stores/session-store'
import { HeartbeatMonitor } from '../stores/heartbeat-monitor'
import { MockNATSClient } from '../testutil/mock-nats'
import { makeSessionKVState, makeProjectKVState } from '../testutil/fixtures'

/** In-memory fake storage — compatible with IStorage, safe in Node.js (no browser globals). */
function makeMemoryStorage(): IStorage & { clear(): void } {
  const store = new Map<string, string>()
  return {
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => { store.set(k, v) },
    removeItem: (k) => { store.delete(k) },
    clear: () => { store.clear() },
  }
}

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
  let storage: ReturnType<typeof makeMemoryStorage>

  beforeEach(() => {
    storage = makeMemoryStorage()
    mockNats = new MockNATSClient()
    sessionStore = new SessionStore(mockNats, 'user-1')
    sessionStore.startWatching()
    heartbeat = new HeartbeatMonitor(mockNats, 'user-1')
    vm = new SessionListVM(sessionStore, heartbeat, mockNats, 'user-1', storage)
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

  describe('sortedGroups', () => {
    it('returns projects sorted alphabetically by name', () => {
      mockNats.kvSet('mclaude-projects', 'user-1.p-z', makeProjectKVState({ id: 'p-z', name: 'Zebra' }))
      mockNats.kvSet('mclaude-projects', 'user-1.p-a', makeProjectKVState({ id: 'p-a', name: 'Alpha' }))
      mockNats.kvSet('mclaude-projects', 'user-1.p-m', makeProjectKVState({ id: 'p-m', name: 'Middle' }))

      const groups = vm.sortedGroups
      expect(groups.map(g => g.project.name)).toEqual(['Alpha', 'Middle', 'Zebra'])
    })

    it('returns all groups when no filter is set', () => {
      mockNats.kvSet('mclaude-projects', 'user-1.p-1', makeProjectKVState({ id: 'p-1', name: 'Alpha' }))
      mockNats.kvSet('mclaude-projects', 'user-1.p-2', makeProjectKVState({ id: 'p-2', name: 'Beta' }))

      expect(vm.sortedGroups).toHaveLength(2)
    })

    it('returns only the filtered project when filter is set', () => {
      mockNats.kvSet('mclaude-projects', 'user-1.p-1', makeProjectKVState({ id: 'p-1', name: 'Alpha' }))
      mockNats.kvSet('mclaude-projects', 'user-1.p-2', makeProjectKVState({ id: 'p-2', name: 'Beta' }))

      vm.setFilter('p-1')
      const groups = vm.sortedGroups
      expect(groups).toHaveLength(1)
      expect(groups[0]!.project.id).toBe('p-1')
    })

    it('filtered group includes sessions belonging to the selected project', () => {
      // Two projects, p-1 has two sessions, p-2 has one session.
      // After filtering to p-1, the group must contain both sessions — not zero.
      // This is the regression test for the "No sessions in this project" bug.
      mockNats.kvSet('mclaude-projects', 'user-1.p-1', makeProjectKVState({ id: 'p-1', name: 'Alpha' }))
      mockNats.kvSet('mclaude-projects', 'user-1.p-2', makeProjectKVState({ id: 'p-2', name: 'Beta' }))
      mockNats.kvSet('mclaude-sessions', 'user-1.p-1.sess-a', makeSessionKVState({ id: 'sess-a', projectId: 'p-1' }))
      mockNats.kvSet('mclaude-sessions', 'user-1.p-1.sess-b', makeSessionKVState({ id: 'sess-b', projectId: 'p-1' }))
      mockNats.kvSet('mclaude-sessions', 'user-1.p-2.sess-c', makeSessionKVState({ id: 'sess-c', projectId: 'p-2' }))

      vm.setFilter('p-1')
      const groups = vm.sortedGroups
      expect(groups).toHaveLength(1)
      expect(groups[0]!.project.id).toBe('p-1')
      // Must include p-1's sessions — not show empty "No sessions in this project" state
      expect(groups[0]!.sessions).toHaveLength(2)
      expect(groups[0]!.sessions.map(s => s.id)).toContain('sess-a')
      expect(groups[0]!.sessions.map(s => s.id)).toContain('sess-b')
    })

    it('filtered group sessions are empty only when project genuinely has no sessions', () => {
      // p-2 has no sessions. Filtering to p-2 should yield an empty sessions array.
      mockNats.kvSet('mclaude-projects', 'user-1.p-1', makeProjectKVState({ id: 'p-1', name: 'Alpha' }))
      mockNats.kvSet('mclaude-projects', 'user-1.p-2', makeProjectKVState({ id: 'p-2', name: 'Beta' }))
      mockNats.kvSet('mclaude-sessions', 'user-1.p-1.sess-a', makeSessionKVState({ id: 'sess-a', projectId: 'p-1' }))

      vm.setFilter('p-2')
      const groups = vm.sortedGroups
      expect(groups).toHaveLength(1)
      expect(groups[0]!.project.id).toBe('p-2')
      expect(groups[0]!.sessions).toHaveLength(0)
    })

    it('sorts sessions within a project by descending stateSince', () => {
      mockNats.kvSet('mclaude-projects', 'user-1.p-1', makeProjectKVState({ id: 'p-1', name: 'Alpha' }))
      // Add three sessions with different stateSince values
      mockNats.kvSet('mclaude-sessions', 'user-1.p-1.sess-old', makeSessionKVState({
        id: 'sess-old',
        projectId: 'p-1',
        stateSince: '2024-01-01T00:00:00.000Z',
      }))
      mockNats.kvSet('mclaude-sessions', 'user-1.p-1.sess-new', makeSessionKVState({
        id: 'sess-new',
        projectId: 'p-1',
        stateSince: '2024-03-01T00:00:00.000Z',
      }))
      mockNats.kvSet('mclaude-sessions', 'user-1.p-1.sess-mid', makeSessionKVState({
        id: 'sess-mid',
        projectId: 'p-1',
        stateSince: '2024-02-01T00:00:00.000Z',
      }))

      const groups = vm.sortedGroups
      expect(groups).toHaveLength(1)
      const sessions = groups[0]!.sessions
      expect(sessions.map(s => s.id)).toEqual(['sess-new', 'sess-mid', 'sess-old'])
    })
  })

  describe('filter state', () => {
    it('filterProjectId returns empty string when no filter stored', () => {
      expect(vm.filterProjectId).toBe('')
    })

    it('setFilter persists to localStorage', () => {
      mockNats.kvSet('mclaude-projects', 'user-1.p-1', makeProjectKVState({ id: 'p-1', name: 'Alpha' }))
      vm.setFilter('p-1')
      expect(storage.getItem('mclaude.filterProjectId')).toBe('p-1')
      expect(vm.filterProjectId).toBe('p-1')
    })

    it('setFilter with empty string clears localStorage', () => {
      storage.setItem('mclaude.filterProjectId', 'p-1')
      vm.setFilter('')
      expect(storage.getItem('mclaude.filterProjectId')).toBeNull()
      expect(vm.filterProjectId).toBe('')
    })

    it('filter survives reload — reads from localStorage on construction', () => {
      // Simulate: first VM sets a filter
      mockNats.kvSet('mclaude-projects', 'user-1.p-1', makeProjectKVState({ id: 'p-1', name: 'Alpha' }))
      vm.setFilter('p-1')
      expect(storage.getItem('mclaude.filterProjectId')).toBe('p-1')

      // Second VM reads from the same storage (simulates page reload)
      const vm2 = new SessionListVM(sessionStore, heartbeat, mockNats, 'user-1', storage)
      expect(vm2.filterProjectId).toBe('p-1')
      vm2.destroy()
    })

    it('resolveFilter returns empty and clears storage when stored project no longer exists', () => {
      storage.setItem('mclaude.filterProjectId', 'stale-project-id')
      // No project with that ID in the store

      const resolved = vm.resolveFilter()
      expect(resolved).toBe('')
      expect(storage.getItem('mclaude.filterProjectId')).toBeNull()
    })

    it('resolveFilter returns the stored ID when project still exists', () => {
      mockNats.kvSet('mclaude-projects', 'user-1.p-1', makeProjectKVState({ id: 'p-1', name: 'Alpha' }))
      storage.setItem('mclaude.filterProjectId', 'p-1')

      expect(vm.resolveFilter()).toBe('p-1')
    })

    it('setFilter notifies listeners', () => {
      mockNats.kvSet('mclaude-projects', 'user-1.p-1', makeProjectKVState({ id: 'p-1', name: 'Alpha' }))
      const calls: number[] = []
      vm.onProjectsChanged(() => calls.push(1))

      vm.setFilter('p-1')
      expect(calls.length).toBeGreaterThan(0)
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
    it('publishes to mclaude.{userId}.{projectId}.api.sessions.create and resolves when session appears in KV', async () => {
      // Start the createSession call which publishes and waits for KV
      const createPromise = vm.createSession('project-1', 'main', 'My Session')

      // Verify publish happened
      const pub = mockNats.published.find(p => p.subject === 'mclaude.user-1.project-1.api.sessions.create')
      expect(pub).toBeDefined()
      const payload = parsePublished(pub!.data) as Record<string, unknown>
      expect(payload).toMatchObject({ projectId: 'project-1', branch: 'main', name: 'My Session' })
      expect(typeof payload['requestId']).toBe('string')

      // Simulate session appearing in KV (what session-agent would do on success)
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.sess-new', makeSessionKVState({
        id: 'sess-new',
        projectId: 'project-1',
      }))

      const sessionId = await createPromise
      expect(sessionId).toBe('sess-new')
    })

    it('rejects on api_error event matching requestId', async () => {
      const createPromise = vm.createSession('project-1', 'main', 'My Session')

      // Get the requestId from the published message
      const pub = mockNats.published.find(p => p.subject === 'mclaude.user-1.project-1.api.sessions.create')
      expect(pub).toBeDefined()
      const payload = parsePublished(pub!.data) as Record<string, unknown>
      const requestId = payload['requestId'] as string

      // Simulate api_error event on the _api subject
      mockNats.simulateReceive('mclaude.user-1.project-1.events._api', {
        type: 'api_error',
        request_id: requestId,
        operation: 'create',
        error: 'session limit exceeded',
      })

      await expect(createPromise).rejects.toThrow('session limit exceeded')
    })

    it('ignores api_error events with a different requestId', async () => {
      // Start a create (will wait for KV)
      const createPromise = vm.createSession('project-1', 'main', 'My Session')

      // Send an error event for a DIFFERENT request
      mockNats.simulateReceive('mclaude.user-1.project-1.events._api', {
        type: 'api_error',
        request_id: 'other-request-id',
        operation: 'create',
        error: 'should be ignored',
      })

      // Resolve via KV
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.sess-new', makeSessionKVState({
        id: 'sess-new',
        projectId: 'project-1',
      }))

      const sessionId = await createPromise
      expect(sessionId).toBe('sess-new')
    })
  })

  describe('deleteSession', () => {
    it('publishes to the session project delete subject (fire-and-forget)', async () => {
      mockNats.kvSet('mclaude-sessions', 'user-1.project-1.session-1', makeSessionKVState({
        id: 'session-1', projectId: 'project-1',
      }))
      sessionStore.startWatching()

      await vm.deleteSession('session-1')

      const pub = mockNats.published.find(p => p.subject === 'mclaude.user-1.project-1.api.sessions.delete')
      expect(pub).toBeDefined()
      expect(parsePublished(pub!.data)).toMatchObject({ sessionId: 'session-1' })
    })

    it('does nothing when session not found', async () => {
      await vm.deleteSession('nonexistent')
      expect(mockNats.published.filter(p => p.subject.includes('.api.sessions.delete'))).toHaveLength(0)
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
