import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { execSync } from 'child_process'

// ─── Mock logger — capture structured log lines ──────────────────────────────

type LogLine = Record<string, unknown>
const mockLogLines: LogLine[] = []

// Pino API: logger.info(mergeObject, message) — two args
// or logger.info(message) — one string arg
function capture(level: string, objOrMsg: LogLine | string, msg?: string): void {
  if (typeof objOrMsg === 'string') {
    mockLogLines.push({ level, msg: objOrMsg })
  } else {
    mockLogLines.push({ level, msg, ...objOrMsg })
  }
}

vi.mock('../logger', () => ({
  logger: {
    info: vi.fn((o: LogLine | string, m?: string) => capture('info', o, m)),
    error: vi.fn((o: LogLine | string, m?: string) => capture('error', o, m)),
    debug: vi.fn((o: LogLine | string, m?: string) => capture('debug', o, m)),
    warn: vi.fn((o: LogLine | string, m?: string) => capture('warn', o, m)),
  },
}))

// ─── Imports after mock ───────────────────────────────────────────────────────

import { ConversationVM } from '../viewmodels/conversation-vm'
import { EventStore } from '../stores/event-store'
import { SessionStore } from '../stores/session-store'
import { AuthStore } from '../stores/auth-store'
import { MockNATSClient } from '../testutil/mock-nats'
import { MockAuthClient } from '../testutil/mock-auth'
import { makeSessionKVState } from '../testutil/fixtures'

// ─── Helpers ─────────────────────────────────────────────────────────────────

function makeJWT(expSeconds: number): string {
  const header = btoa(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).replace(/=/g, '')
  const payload = btoa(JSON.stringify({ sub: 'user-1', exp: expSeconds })).replace(/=/g, '')
  return `${header}.${payload}.sig`
}

function logsForComponent(component: string): LogLine[] {
  return mockLogLines.filter(l => l['component'] === component)
}

// ─── Tests ───────────────────────────────────────────────────────────────────

describe('monitoring — structured pino logs', () => {
  let mockNats: MockNATSClient
  let eventStore: EventStore
  let sessionStore: SessionStore
  let vm: ConversationVM

  beforeEach(() => {
    mockLogLines.length = 0
    vi.clearAllMocks()

    mockNats = new MockNATSClient()
    eventStore = new EventStore({
      natsClient: mockNats,
      userId: 'user-1',
      projectId: 'project-1',
      sessionId: 'session-1',
    })
    sessionStore = new SessionStore(mockNats, 'user-1')

    // Pre-populate session store so vm.state.skills works (ADR-0054: per-user bucket)
    mockNats.kvSet('mclaude-sessions-user-1', 'hosts.local.projects.project-1.sessions.session-1', makeSessionKVState())
    sessionStore.startWatching()

    vm = new ConversationVM(
      eventStore,
      sessionStore,
      mockNats,
      'user-1',
      'project-1',
      'session-1',
    )
  })

  afterEach(() => {
    vm.destroy()
    sessionStore.stopWatching()
  })

  describe('ConversationVM.sendMessage', () => {
    it('logs info with component=conversation-vm and sessionId', () => {
      vm.sendMessage('hello')
      const lines = logsForComponent('conversation-vm')
      expect(lines.length).toBeGreaterThanOrEqual(1)
      const line = lines.find(l => l['msg'] === 'sendMessage')
      expect(line).toBeDefined()
      expect(line?.['sessionId']).toBe('session-1')
      expect(line?.['level']).toBe('info')
    })

    it('logs userId on sendMessage', () => {
      vm.sendMessage('test')
      const line = mockLogLines.find(l => l['msg'] === 'sendMessage')
      expect(line?.['userId']).toBe('user-1')
    })
  })

  describe('ConversationVM.approvePermission', () => {
    it('logs info with component=conversation-vm and requestId', () => {
      // Add a pending control_request block first so the block can be found
      eventStore.applyEventForTest({
        type: 'control_request',
        subtype: 'can_use_tool',
        request_id: 'req-1',
        tool_name: 'Bash',
        input: { command: 'ls' },
      })

      mockLogLines.length = 0
      vm.approvePermission('req-1')

      const line = mockLogLines.find(l => l['msg'] === 'approvePermission')
      expect(line).toBeDefined()
      expect(line?.['component']).toBe('conversation-vm')
      expect(line?.['requestId']).toBe('req-1')
      expect(line?.['level']).toBe('info')
      expect(line?.['sessionId']).toBe('session-1')
    })
  })

  describe('SessionStore KV updates', () => {
    it('logs debug with component=session-store and sessionId on KV update', () => {
      // Trigger a KV update after the store is already watching
      mockLogLines.length = 0
      mockNats.kvSet('mclaude-sessions-user-1', 'hosts.local.projects.project-1.sessions.session-2', makeSessionKVState({ id: 'session-2' }))

      const lines = logsForComponent('session-store')
      expect(lines.length).toBeGreaterThanOrEqual(1)
      const line = lines.find(l => l['sessionId'] === 'session-2')
      expect(line).toBeDefined()
      expect(line?.['level']).toBe('debug')
      expect(line?.['userId']).toBe('user-1')
    })
  })

  describe('AuthStore JWT refresh', () => {
    let authStore: AuthStore
    let mockAuth: MockAuthClient

    beforeEach(() => {
      mockAuth = new MockAuthClient()
      authStore = new AuthStore(mockAuth, mockNats)
    })

    afterEach(() => {
      authStore.stopRefreshLoop()
      vi.useRealTimers()
    })

    it('logs info on successful JWT refresh', async () => {
      vi.useFakeTimers()

      const nearExpiryJwt = makeJWT(Math.floor(Date.now() / 1000) + 120)
      mockAuth.loginResponse = { ...mockAuth.loginResponse, jwt: nearExpiryJwt }
      await authStore.login('user@example.com', 'password')

      mockLogLines.length = 0

      const checkIntervalMs = 1000
      authStore.startRefreshLoop(checkIntervalMs)
      await vi.advanceTimersByTimeAsync(checkIntervalMs + 10)
      authStore.stopRefreshLoop()

      const line = mockLogLines.find(l => l['msg'] === 'JWT refreshed')
      expect(line).toBeDefined()
      expect(line?.['component']).toBe('auth-store')
      expect(line?.['level']).toBe('info')
    })

    it('logs error on JWT refresh failure', async () => {
      vi.useFakeTimers()

      mockAuth.shouldFailRefresh = true
      const nearExpiryJwt = makeJWT(Math.floor(Date.now() / 1000) + 120)
      mockAuth.loginResponse = { ...mockAuth.loginResponse, jwt: nearExpiryJwt }
      await authStore.login('user@example.com', 'password')

      mockLogLines.length = 0

      const checkIntervalMs = 1000
      authStore.startRefreshLoop(checkIntervalMs)
      await vi.advanceTimersByTimeAsync(checkIntervalMs + 10)
      authStore.stopRefreshLoop()

      const line = mockLogLines.find(l => l['msg'] === 'JWT refresh failed')
      expect(line).toBeDefined()
      expect(line?.['component']).toBe('auth-store')
      expect(line?.['level']).toBe('error')
    })
  })

  describe('code quality', () => {
    it('no bare console.log in production source files', () => {
      const result = execSync(
        'grep -r "console\\.log" ~/mclaude/worktrees/spa/mclaude-web/src' +
        ' --include="*.ts" --include="*.tsx"' +
        ' --exclude-dir=testutil --exclude="*.test.ts" --exclude="*.test.tsx" || true',
        { encoding: 'utf8' },
      )
      expect(result.trim()).toBe('')
    })
  })

  describe('EventStore logging', () => {
    it('logs start/subscribe at debug level', () => {
      const mockNats = new MockNATSClient()
      const store = new EventStore({ natsClient: mockNats, userId: 'user-1', projectId: 'project-1', sessionId: 'session-1' })
      store.start()
      // Find a debug log line from event-store with sessionId
      const debugLogs = mockLogLines.filter(l =>
        l['level'] === 'debug' && l['component'] === 'event-store'
      )
      expect(debugLogs.length).toBeGreaterThan(0)
      store.stop()
    })

    it('logs warn for malformed event', () => {
      const mockNats = new MockNATSClient()
      const store = new EventStore({ natsClient: mockNats, userId: 'user-1', projectId: 'project-1', sessionId: 'session-1' })
      store.start()
      mockLogLines.length = 0
      // Simulate malformed JSON — use ADR-0024 subject format (typed slugs)
      // EventStore subscribes to mclaude.users.{uslug}.projects.{pslug}.events.{sslug}
      // When no slug opts provided, falls back to userId/projectId/sessionId as slug values
      mockNats.publish('mclaude.users.user-1.hosts.local.projects.project-1.events.session-1', new TextEncoder().encode('not-json'))
      const warnLogs = mockLogLines.filter(l => l['level'] === 'warn' && l['component'] === 'event-store')
      expect(warnLogs.length).toBeGreaterThan(0)
      store.stop()
    })
  })
})
