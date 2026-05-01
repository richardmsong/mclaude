import { describe, it, expect, beforeEach } from 'vitest'
import { ConversationVM } from './conversation-vm'
import { EventStore } from '../stores/event-store'
import { SessionStore } from '../stores/session-store'
import { MockNATSClient } from '../testutil/mock-nats'
import { makeSessionKVState, transcripts } from '../testutil/fixtures'

function parsePublished(data: Uint8Array): unknown {
  return JSON.parse(new TextDecoder().decode(data))
}

describe('ConversationVM', () => {
  let mockNats: MockNATSClient
  let sessionStore: SessionStore
  let eventStore: EventStore
  let vm: ConversationVM

  beforeEach(() => {
    mockNats = new MockNATSClient()
    const sessionState = makeSessionKVState()
    // ADR-0054: per-user bucket, new key format
  mockNats.kvSet('mclaude-sessions-user-1', 'hosts.local.projects.project-1.sessions.session-1', sessionState)

    sessionStore = new SessionStore(mockNats, 'user-1')
    sessionStore.startWatching()

    eventStore = new EventStore({
      natsClient: mockNats,
      userId: 'user-1',
      projectId: 'project-1',
      sessionId: 'session-1',
    })

    vm = new ConversationVM(
      eventStore,
      sessionStore,
      mockNats,
      'user-1',
      'project-1',
      'session-1',
    )
  })

  describe('sendMessage', () => {
    it('publishes to the correct subject with correct payload including uuid', () => {
      mockNats.clearRecorded()
      vm.sendMessage('hello')
      expect(mockNats.published).toHaveLength(1)
      const msg = mockNats.published[0]
      expect(msg.subject).toBe('mclaude.users.user-1.hosts.local.projects.project-1.sessions.session-1.input')
      const payload = parsePublished(msg.data) as { type: string; message: { role: string; content: string }; session_id: string; uuid: string }
      expect(payload.type).toBe('user')
      expect(payload.message.role).toBe('user')
      expect(payload.message.content).toBe('hello')
      expect(payload.session_id).toBe('session-1')
      expect(typeof payload.uuid).toBe('string')
      expect(payload.uuid).toMatch(/^[0-9a-f-]{36}$/)
    })

    it('adds a pending message to the event store and an optimistic user turn', () => {
      vm.sendMessage('hello world')
      // addPendingMessage immediately inserts an optimistic user turn
      expect(eventStore.conversation.turns).toHaveLength(1)
      expect(eventStore.conversation.turns[0].type).toBe('user')
      expect(eventStore.conversation.turns[0].pendingUuid).toBeDefined()
      // Pending message also tracked in pendingMessages
      expect(eventStore.pendingMessages).toHaveLength(1)
      expect(eventStore.pendingMessages[0].content).toBe('hello world')
      expect(typeof eventStore.pendingMessages[0].uuid).toBe('string')
    })

    it('pending message is removed when user event with matching uuid arrives', () => {
      vm.sendMessage('hello world')
      const uuid = eventStore.pendingMessages[0].uuid
      // Simulate Claude echoing back the message
      eventStore.applyEventForTest({
        type: 'user',
        message: { role: 'user', content: 'hello world' },
        uuid,
        isReplay: true,
      })
      expect(eventStore.pendingMessages).toHaveLength(0)
      expect(eventStore.conversation.turns.filter(t => t.type === 'user')).toHaveLength(1)
    })

    it('vm.state includes pendingMessages', () => {
      vm.sendMessage('pending text')
      const state = vm.state
      expect(state.pendingMessages).toHaveLength(1)
      expect(state.pendingMessages[0].content).toBe('pending text')
    })
  })

  describe('approvePermission', () => {
    it('publishes control_response with allow behavior and updates block status', () => {
      // First set up a pending permission via event
      for (const event of transcripts.permissionRequest) {
        eventStore.applyEventForTest(event)
      }

      mockNats.clearRecorded()
      vm.approvePermission('req-1')

      expect(mockNats.published).toHaveLength(1)
      const msg = mockNats.published[0]
      expect(msg.subject).toBe('mclaude.users.user-1.hosts.local.projects.project-1.sessions.session-1.control')
      const payload = parsePublished(msg.data) as {
        type: string
        response: { subtype: string; request_id: string; response: { behavior: string } }
      }
      expect(payload.type).toBe('control_response')
      expect(payload.response.subtype).toBe('success')
      expect(payload.response.request_id).toBe('req-1')
      expect(payload.response.response.behavior).toBe('allow')

      // Verify block status updated
      let found = false
      for (const turn of eventStore.conversation.turns) {
        for (const block of turn.blocks) {
          if (block.type === 'control_request' && block.requestId === 'req-1') {
            expect(block.status).toBe('approved')
            found = true
          }
        }
      }
      expect(found).toBe(true)
    })
  })

  describe('denyPermission', () => {
    it('publishes control_response with deny behavior and updates block status', () => {
      for (const event of transcripts.permissionRequest) {
        eventStore.applyEventForTest(event)
      }

      mockNats.clearRecorded()
      vm.denyPermission('req-1')

      const msg = mockNats.published[0]
      expect(msg.subject).toBe('mclaude.users.user-1.hosts.local.projects.project-1.sessions.session-1.control')
      const payload = parsePublished(msg.data) as {
        type: string
        response: { subtype: string; request_id: string; response: { behavior: string } }
      }
      expect(payload.type).toBe('control_response')
      expect(payload.response.response.behavior).toBe('deny')

      let found = false
      for (const turn of eventStore.conversation.turns) {
        for (const block of turn.blocks) {
          if (block.type === 'control_request' && block.requestId === 'req-1') {
            expect(block.status).toBe('denied')
            found = true
          }
        }
      }
      expect(found).toBe(true)
    })
  })

  describe('interrupt', () => {
    it('publishes interrupt control request to sessions.control', () => {
      mockNats.clearRecorded()
      vm.interrupt()
      expect(mockNats.published).toHaveLength(1)
      const msg = mockNats.published[0]
      expect(msg.subject).toBe('mclaude.users.user-1.hosts.local.projects.project-1.sessions.session-1.control')
      const payload = parsePublished(msg.data) as { type: string; request: { subtype: string } }
      expect(payload.type).toBe('control_request')
      expect(payload.request.subtype).toBe('interrupt')
    })
  })

  describe('switchModel', () => {
    it('publishes set_model control request with the model name', () => {
      mockNats.clearRecorded()
      vm.switchModel('claude-opus-4-6')
      expect(mockNats.published).toHaveLength(1)
      const msg = mockNats.published[0]
      expect(msg.subject).toBe('mclaude.users.user-1.hosts.local.projects.project-1.sessions.session-1.control')
      const payload = parsePublished(msg.data) as { type: string; request: { subtype: string; model: string } }
      expect(payload.type).toBe('control_request')
      expect(payload.request.subtype).toBe('set_model')
      expect(payload.request.model).toBe('claude-opus-4-6')
    })
  })

  describe('invokeSkill', () => {
    it('sends message with slash-prefixed skill and args', () => {
      mockNats.clearRecorded()
      vm.invokeSkill('commit', '-m "Fix bug"')
      expect(mockNats.published).toHaveLength(1)
      const msg = mockNats.published[0]
      const payload = parsePublished(msg.data) as { type: string; message: { content: string } }
      expect(payload.message.content).toBe('/commit -m "Fix bug"')
    })

    it('sends message with just skill name when no args', () => {
      mockNats.clearRecorded()
      vm.invokeSkill('review-pr')
      const msg = mockNats.published[0]
      const payload = parsePublished(msg.data) as { type: string; message: { content: string } }
      expect(payload.message.content).toBe('/review-pr')
    })
  })

  describe('reloadPlugins', () => {
    it('publishes reload_plugins control request', () => {
      mockNats.clearRecorded()
      vm.reloadPlugins()
      expect(mockNats.published).toHaveLength(1)
      const msg = mockNats.published[0]
      expect(msg.subject).toBe('mclaude.users.user-1.hosts.local.projects.project-1.sessions.session-1.control')
      const payload = parsePublished(msg.data) as { type: string; request: { subtype: string } }
      expect(payload.type).toBe('control_request')
      expect(payload.request.subtype).toBe('reload_plugins')
    })
  })

  describe('setMaxThinkingTokens', () => {
    it('publishes set_max_thinking_tokens control request with budget', () => {
      mockNats.clearRecorded()
      vm.setMaxThinkingTokens(8000)
      expect(mockNats.published).toHaveLength(1)
      const msg = mockNats.published[0]
      expect(msg.subject).toBe('mclaude.users.user-1.hosts.local.projects.project-1.sessions.session-1.control')
      const payload = parsePublished(msg.data) as {
        type: string
        request: { subtype: string; budget: number }
      }
      expect(payload.type).toBe('control_request')
      expect(payload.request.subtype).toBe('set_max_thinking_tokens')
      expect(payload.request.budget).toBe(8000)
    })

    it('publishes budget=0 to disable thinking', () => {
      mockNats.clearRecorded()
      vm.setMaxThinkingTokens(0)
      const msg = mockNats.published[0]
      const payload = parsePublished(msg.data) as {
        type: string
        request: { subtype: string; budget: number }
      }
      expect(payload.request.budget).toBe(0)
    })
  })
})
