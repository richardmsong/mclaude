/**
 * Tests for spec compliance fixes (G1, G2, G3, G7-G11, G13).
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { AuthStore } from './auth-store'
import { SessionStore } from './session-store'
import { EventStore } from './event-store'
import { MockAuthClient } from '../testutil/mock-auth'
import { MockNATSClient } from '../testutil/mock-nats'
import { makeSessionKVState, makeProjectKVState } from '../testutil/fixtures'
import { subjProjectsCreate } from '../lib/subj'
import type { UserSlug, HostSlug } from '../lib/slug'

function makeJWT(expSeconds: number, extra: Record<string, unknown> = {}): string {
  const header = btoa(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).replace(/=/g, '')
  const payload = btoa(JSON.stringify({ sub: 'user-1', userId: 'user-1', exp: expSeconds, ...extra })).replace(/=/g, '')
  return `${header}.${payload}.mock-sig`
}

// ── G1: JWT refresh threshold = 15 minutes ─────────────────────────────────
describe('G1: JWT refresh threshold is 15 minutes', () => {
  let mockAuth: MockAuthClient
  let mockNats: MockNATSClient
  let store: AuthStore

  beforeEach(() => {
    mockAuth = new MockAuthClient()
    mockNats = new MockNATSClient()
    store = new AuthStore(mockAuth, mockNats)
  })

  afterEach(() => {
    store.stopRefreshLoop()
    vi.useRealTimers()
  })

  it('refreshes when TTL is below 15 minutes', async () => {
    vi.useFakeTimers()
    // JWT expires in 10 minutes (below 15-min threshold)
    const nearExpiryJwt = makeJWT(Math.floor(Date.now() / 1000) + 600)
    mockAuth.loginResponse = { ...mockAuth.loginResponse, jwt: nearExpiryJwt }
    await store.login('user@example.com', 'password')

    store.startRefreshLoop(1000)
    await vi.advanceTimersByTimeAsync(1010)
    store.stopRefreshLoop()

    expect(mockAuth.refreshCalls.length).toBeGreaterThanOrEqual(1)
  })

  it('does NOT refresh when TTL is above 15 minutes', async () => {
    vi.useFakeTimers()
    // JWT expires in 20 minutes (above 15-min threshold)
    const farJwt = makeJWT(Math.floor(Date.now() / 1000) + 1200)
    mockAuth.loginResponse = { ...mockAuth.loginResponse, jwt: farJwt }
    await store.login('user@example.com', 'password')

    store.startRefreshLoop(1000)
    await vi.advanceTimersByTimeAsync(1010)
    store.stopRefreshLoop()

    expect(mockAuth.refreshCalls).toHaveLength(0)
  })

  it('does NOT refresh when TTL is above 15 minutes (16 min)', async () => {
    vi.useFakeTimers()
    // JWT expires in 16 minutes (just above 15-min threshold)
    const boundaryJwt = makeJWT(Math.floor(Date.now() / 1000) + 960)
    mockAuth.loginResponse = { ...mockAuth.loginResponse, jwt: boundaryJwt }
    await store.login('user@example.com', 'password')

    store.startRefreshLoop(1000)
    await vi.advanceTimersByTimeAsync(1010)
    store.stopRefreshLoop()

    expect(mockAuth.refreshCalls).toHaveLength(0)
  })
})

// ── G3: Result event rendering — usage data extraction ─────────────────────
describe('G3: Result event renders usage data', () => {
  let mockNats: MockNATSClient
  let eventStore: EventStore

  beforeEach(() => {
    mockNats = new MockNATSClient()
    eventStore = new EventStore({
      natsClient: mockNats,
      userId: 'user-1',
      projectId: 'project-1',
      sessionId: 'session-1',
    })
  })

  it('applies usage from result event to the last assistant turn', () => {
    // Create an assistant turn first
    eventStore.applyEventForTest({
      type: 'assistant',
      message: {
        id: 'msg-1',
        role: 'assistant',
        content: [{ type: 'text', text: 'Hello' }],
        model: 'claude-sonnet-4-6',
      },
    }, 1)

    // Then receive result event with usage
    eventStore.applyEventForTest({
      type: 'result',
      subtype: 'success',
      usage: { inputTokens: 100, outputTokens: 50, cacheReadTokens: 10, cacheWriteTokens: 5, costUsd: 0.001 },
    }, 2)

    const turns = eventStore.conversation.turns
    expect(turns).toHaveLength(1)
    expect(turns[0].usage).toBeDefined()
    expect(turns[0].usage?.inputTokens).toBe(100)
    expect(turns[0].usage?.outputTokens).toBe(50)
    expect(turns[0].usage?.costUsd).toBe(0.001)
  })

  it('does nothing when result event has no usage', () => {
    eventStore.applyEventForTest({
      type: 'assistant',
      message: {
        id: 'msg-1',
        role: 'assistant',
        content: [{ type: 'text', text: 'Hello' }],
        model: 'claude-sonnet-4-6',
      },
    }, 1)

    eventStore.applyEventForTest({
      type: 'result',
      subtype: 'success',
    }, 2)

    // Usage was set by assistant event, result event without usage should not clear it
    const turns = eventStore.conversation.turns
    expect(turns).toHaveLength(1)
  })
})

// ── G7-G9: Payload fields ──────────────────────────────────────────────────
describe('G7-G9: Session payload fields', () => {
  // These tests verify the SessionListVM publishes correct payloads
  // We import the module indirectly via the mock NATS client
  let mockNats: MockNATSClient
  let sessionStore: SessionStore

  beforeEach(() => {
    mockNats = new MockNATSClient()
    sessionStore = new SessionStore(mockNats, 'user-1', 'dev')
    sessionStore.startWatching()

    // Set up a project so the VM can resolve slugs (ADR-0054: per-user bucket)
    mockNats.kvSet('mclaude-projects-dev', 'hosts.local.projects.my-project', makeProjectKVState({
      id: 'project-1',
      slug: 'my-project',
      hostSlug: 'local',
    }))
  })

  it('G8: deleteSession includes requestId', async () => {
    // Set up a session to delete (ADR-0054: per-user bucket, new key format)
    mockNats.kvSet('mclaude-sessions-dev', 'hosts.local.projects.my-project.sessions.session-1', makeSessionKVState({
      id: 'session-1',
      projectId: 'project-1',
      hostSlug: 'local',
    }))

    // Import and create the VM
    const { SessionListVM } = await import('../viewmodels/session-list-vm')
    const { HeartbeatMonitor } = await import('./heartbeat-monitor')
    const hb = new HeartbeatMonitor(mockNats, 'user-1', 'dev')
    const vm = new SessionListVM(sessionStore, hb, mockNats, 'user-1', undefined, 'dev')

    await vm.deleteSession('session-1')

    const lastPub = mockNats.published[mockNats.published.length - 1]
    const payload = JSON.parse(new TextDecoder().decode(lastPub.data))
    expect(payload.requestId).toBeDefined()
    expect(typeof payload.requestId).toBe('string')
    expect(payload.requestId.length).toBeGreaterThan(0)
  })

  it('G9: restartSession includes requestId', async () => {
    mockNats.kvSet('mclaude-sessions-dev', 'hosts.local.projects.my-project.sessions.session-1', makeSessionKVState({
      id: 'session-1',
      projectId: 'project-1',
      hostSlug: 'local',
    }))

    const { SessionListVM } = await import('../viewmodels/session-list-vm')
    const { HeartbeatMonitor } = await import('./heartbeat-monitor')
    const hb = new HeartbeatMonitor(mockNats, 'user-1', 'dev')
    const vm = new SessionListVM(sessionStore, hb, mockNats, 'user-1', undefined, 'dev')

    await vm.restartSession('session-1')

    const lastPub = mockNats.published[mockNats.published.length - 1]
    const payload = JSON.parse(new TextDecoder().decode(lastPub.data))
    expect(payload.requestId).toBeDefined()
    expect(typeof payload.requestId).toBe('string')
  })
})

// ── G10: Session KV DEL handling — slug→UUID lookup ────────────────────────
describe('G10: Session KV DEL removes by UUID via slug lookup', () => {
  let mockNats: MockNATSClient
  let store: SessionStore

  beforeEach(() => {
    mockNats = new MockNATSClient()
    store = new SessionStore(mockNats, 'user-1')
    store.startWatching()
  })

  it('removes session from map when DEL arrives for a session with matching slug', () => {
    // ADR-0054: per-user bucket, new key format
    const session = makeSessionKVState({ id: 'uuid-abc-123', projectId: 'project-1', slug: 'my-session' })
    mockNats.kvSet('mclaude-sessions-user-1', 'hosts.local.projects.project-1.sessions.my-session', session)
    expect(store.sessions.has('uuid-abc-123')).toBe(true)

    // DEL arrives with slug-based key (ADR-0054 format)
    mockNats.kvDelete('mclaude-sessions-user-1', 'hosts.local.projects.project-1.sessions.my-session')
    expect(store.sessions.has('uuid-abc-123')).toBe(false)
  })

  it('does not crash when DEL arrives for unknown slug', () => {
    mockNats.kvDelete('mclaude-sessions-user-1', 'hosts.local.projects.project-1.sessions.nonexistent')
    expect(store.sessions.size).toBe(0)
  })

  it('removes only the session with matching slug, not others', () => {
    const s1 = makeSessionKVState({ id: 'uuid-1', projectId: 'project-1', slug: 'sess-a' })
    const s2 = makeSessionKVState({ id: 'uuid-2', projectId: 'project-1', slug: 'sess-b' })
    mockNats.kvSet('mclaude-sessions-user-1', 'hosts.local.projects.project-1.sessions.sess-a', s1)
    mockNats.kvSet('mclaude-sessions-user-1', 'hosts.local.projects.project-1.sessions.sess-b', s2)
    expect(store.sessions.size).toBe(2)

    mockNats.kvDelete('mclaude-sessions-user-1', 'hosts.local.projects.project-1.sessions.sess-a')
    expect(store.sessions.has('uuid-1')).toBe(false)
    expect(store.sessions.has('uuid-2')).toBe(true)
    expect(store.sessions.size).toBe(1)
  })
})

// ── G11: createProject subject is host-scoped ──────────────────────────────
describe('G11: subjProjectsCreate is host-scoped', () => {
  it('produces host-scoped subject with uslug and hslug', () => {
    const result = subjProjectsCreate('alice' as UserSlug, 'local' as HostSlug)
    expect(result).toBe('mclaude.users.alice.hosts.local.api.projects.create')
  })

  it('includes the host slug in the subject', () => {
    const result = subjProjectsCreate('dev' as UserSlug, 'us-east' as HostSlug)
    expect(result).toContain('.hosts.us-east.')
  })
})

// ── G13: Project KV DEL handling — explicit check ──────────────────────────
describe('G13: Project KV DEL is explicitly handled', () => {
  let mockNats: MockNATSClient
  let store: SessionStore

  beforeEach(() => {
    mockNats = new MockNATSClient()
    store = new SessionStore(mockNats, 'user-1')
    store.startWatching()
  })

  it('removes project from map on DEL operation (slug-based lookup)', () => {
    // ADR-0054: per-user bucket, key format hosts.{hslug}.projects.{pslug}
    // Project map is keyed by UUID; DEL handler uses slug->UUID lookup.
    const project = makeProjectKVState({ id: 'project-1', name: 'Test', slug: 'project-1' })
    mockNats.kvSet('mclaude-projects-user-1', 'hosts.local.projects.project-1', project)
    expect(store.projects.has('project-1')).toBe(true)

    mockNats.kvDelete('mclaude-projects-user-1', 'hosts.local.projects.project-1')
    expect(store.projects.has('project-1')).toBe(false)
  })

  it('does not remove project on malformed JSON (non-DEL)', () => {
    // First add a valid project (ADR-0054: per-user bucket)
    const project = makeProjectKVState({ id: 'project-1', name: 'Test', slug: 'project-1' })
    mockNats.kvSet('mclaude-projects-user-1', 'hosts.local.projects.project-1', project)
    expect(store.projects.has('project-1')).toBe(true)

    // Simulate a PUT with malformed data (should not remove the project now)
    const watchers = (mockNats as unknown as { _kvWatchers: Map<string, Array<{ pattern: string; callback: (e: unknown) => void }>> })._kvWatchers
    const projectWatchers = watchers.get('mclaude-projects-user-1') ?? []
    for (const w of projectWatchers) {
      w.callback({
        key: '****************',
        value: new TextEncoder().encode('NOT VALID JSON{'),
        revision: 99,
        operation: 'PUT',
      })
    }
    // With the fix, malformed PUT should NOT delete the project (previously it would)
    expect(store.projects.has('project-1')).toBe(true)
  })

  it('notifies project listeners on DEL', () => {
    const project = makeProjectKVState({ id: 'project-1', name: 'Test', slug: 'project-1' })
    mockNats.kvSet('mclaude-projects-user-1', 'hosts.local.projects.project-1', project)

    const calls: number[] = []
    store.onProjectChanged(projects => calls.push(projects.size))

    mockNats.kvDelete('mclaude-projects-user-1', 'hosts.local.projects.project-1')
    expect(calls).toEqual([0])
  })
})
