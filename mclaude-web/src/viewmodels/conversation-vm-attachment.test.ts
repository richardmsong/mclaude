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

  it('publishes type:message format per spec-nats-payload-schema.md', () => {
    mockNats.clearRecorded()
    vm.sendMessageWithAttachment('check this', TEST_ATTACHMENT)

    expect(mockNats.published).toHaveLength(1)
    const msg = mockNats.published[0]
    expect(msg.subject).toBe(
      'mclaude.users.user-1.hosts.local.projects.project-1.sessions.session-1.input',
    )

    const payload = parsePublished(msg.data) as {
      id: string
      ts: number
      type: string
      text: string
      attachments: Array<{ id: string; filename: string; mimeType: string; sizeBytes: number }>
    }
    // Must use type:"message" not type:"user" — session-agent only resolves S3 attachments for type:"message"
    expect(payload.type).toBe('message')
    expect(payload.text).toBe('check this')
    expect(Array.isArray(payload.attachments)).toBe(true)
    expect(payload.attachments).toHaveLength(1)

    const att = payload.attachments[0]
    expect(att.id).toBe('att-001')
    expect(att.filename).toBe('screenshot.png')
    expect(att.mimeType).toBe('image/png')
    expect(att.sizeBytes).toBe(245000)
  })

  it('publishes empty text and single attachment when text is empty', () => {
    mockNats.clearRecorded()
    vm.sendMessageWithAttachment('', TEST_ATTACHMENT)

    const msg = mockNats.published[0]
    const payload = parsePublished(msg.data) as {
      type: string
      text: string
      attachments: Array<{ id: string }>
    }
    expect(payload.type).toBe('message')
    expect(payload.text).toBe('')
    expect(payload.attachments).toHaveLength(1)
    expect(payload.attachments[0].id).toBe('att-001')
  })

  it('includes envelope id (UUID) and ts (unix millis) in the published payload', () => {
    const before = Date.now()
    mockNats.clearRecorded()
    vm.sendMessageWithAttachment('hi', TEST_ATTACHMENT)
    const after = Date.now()
    const payload = parsePublished(mockNats.published[0].data) as { id: string; ts: number }
    expect(typeof payload.id).toBe('string')
    expect(payload.id).toMatch(/^[0-9a-f-]{36}$/)
    expect(payload.ts).toBeGreaterThanOrEqual(before)
    expect(payload.ts).toBeLessThanOrEqual(after)
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

  it('session slug is encoded in the NATS subject (not the payload)', () => {
    // Per spec-nats-payload-schema.md: the session is identified by the subject, not a payload field.
    // The subject is mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.input
    mockNats.clearRecorded()
    vm.sendMessageWithAttachment('hello', TEST_ATTACHMENT)
    const msg = mockNats.published[0]
    expect(msg.subject).toContain('sessions.session-1.input')
    // Payload must NOT contain legacy session_id field
    const payload = parsePublished(msg.data) as Record<string, unknown>
    expect(payload).not.toHaveProperty('session_id')
  })
})
