/**
 * Tests for ConversationVM.sendMessageWithAttachment (ADR-0053).
 */
import { describe, it, expect, beforeEach } from 'vitest'
import { ConversationVM } from './conversation-vm'
import { EventStore } from '../stores/event-store'
import { SessionStore } from '../stores/session-store'
import { MockNATSClient } from '../testutil/mock-nats'
import { makeSessionKVState } from '../testutil/fixtures'
import type { AttachmentRef } from '@/transport/attachment-client'
import type { AttachmentRefBlock } from '@/types'

function parsePublished(data: Uint8Array): unknown {
  return JSON.parse(new TextDecoder().decode(data))
}

const TEST_ATTACHMENT: AttachmentRef = {
  id: 'att-001',
  filename: 'screenshot.png',
  mimeType: 'image/png',
  sizeBytes: 245000,
}

describe('ConversationVM.sendMessageWithAttachment (ADR-0053)', () => {
  let mockNats: MockNATSClient
  let sessionStore: SessionStore
  let eventStore: EventStore
  let vm: ConversationVM

  beforeEach(() => {
    mockNats = new MockNATSClient()
    const sessionState = makeSessionKVState()
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

  it('publishes a message with attachment_ref content block', () => {
    mockNats.clearRecorded()
    vm.sendMessageWithAttachment('check this', TEST_ATTACHMENT)

    expect(mockNats.published).toHaveLength(1)
    const msg = mockNats.published[0]
    expect(msg.subject).toBe(
      'mclaude.users.user-1.hosts.local.projects.project-1.sessions.session-1.input',
    )

    const payload = parsePublished(msg.data) as {
      type: string
      message: { role: string; content: Array<{ type: string; id?: string; filename?: string; mimeType?: string; sizeBytes?: number; text?: string }> }
      session_id: string
      uuid: string
    }
    expect(payload.type).toBe('user')
    expect(payload.message.role).toBe('user')
    expect(Array.isArray(payload.message.content)).toBe(true)

    const textBlock = payload.message.content.find(c => c.type === 'text')
    expect(textBlock?.text).toBe('check this')

    const attBlock = payload.message.content.find(c => c.type === 'attachment_ref')
    expect(attBlock?.id).toBe('att-001')
    expect(attBlock?.filename).toBe('screenshot.png')
    expect(attBlock?.mimeType).toBe('image/png')
    expect(attBlock?.sizeBytes).toBe(245000)
  })

  it('omits text block when message text is empty', () => {
    mockNats.clearRecorded()
    vm.sendMessageWithAttachment('', TEST_ATTACHMENT)

    const msg = mockNats.published[0]
    const payload = parsePublished(msg.data) as {
      message: { content: Array<{ type: string }> }
    }
    const textBlocks = payload.message.content.filter(c => c.type === 'text')
    expect(textBlocks).toHaveLength(0)
    const attBlocks = payload.message.content.filter(c => c.type === 'attachment_ref')
    expect(attBlocks).toHaveLength(1)
  })

  it('includes a uuid in the published payload', () => {
    mockNats.clearRecorded()
    vm.sendMessageWithAttachment('hi', TEST_ATTACHMENT)
    const payload = parsePublished(mockNats.published[0].data) as { uuid: string }
    expect(typeof payload.uuid).toBe('string')
    expect(payload.uuid).toMatch(/^[0-9a-f-]{36}$/)
  })

  it('adds an optimistic user turn with attachment_ref block', () => {
    vm.sendMessageWithAttachment('see file', TEST_ATTACHMENT)

    const { turns } = eventStore.conversation
    const userTurn = turns.find(t => t.type === 'user' && t.pendingUuid !== undefined)
    expect(userTurn).toBeDefined()

    const textBlock = userTurn?.blocks.find(b => b.type === 'text')
    expect(textBlock).toBeDefined()

    const attBlock = userTurn?.blocks.find(b => b.type === 'attachment_ref') as AttachmentRefBlock | undefined
    expect(attBlock).toBeDefined()
    expect(attBlock?.id).toBe('att-001')
    expect(attBlock?.filename).toBe('screenshot.png')
  })

  it('adds optimistic turn without text block when text is empty', () => {
    vm.sendMessageWithAttachment('', TEST_ATTACHMENT)

    const { turns } = eventStore.conversation
    const userTurn = turns.find(t => t.type === 'user' && t.pendingUuid !== undefined)
    expect(userTurn).toBeDefined()

    const textBlocks = userTurn?.blocks.filter(b => b.type === 'text')
    expect(textBlocks).toHaveLength(0)
    const attBlocks = userTurn?.blocks.filter(b => b.type === 'attachment_ref')
    expect(attBlocks).toHaveLength(1)
  })

  it('session_id is included in the NATS payload', () => {
    mockNats.clearRecorded()
    vm.sendMessageWithAttachment('hello', TEST_ATTACHMENT)
    const payload = parsePublished(mockNats.published[0].data) as { session_id: string }
    expect(payload.session_id).toBe('session-1')
  })
})
