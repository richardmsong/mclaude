/**
 * Tests for ADR-0053 attachment_ref block handling in EventStore.
 */
import { describe, it, expect, beforeEach } from 'vitest'
import { EventStore } from './event-store'
import { MockNATSClient } from '../testutil/mock-nats'
import type { StreamJsonEvent, AttachmentRefBlock } from '@/types'

describe('EventStore — attachment_ref blocks (ADR-0053)', () => {
  let mockNats: MockNATSClient
  let store: EventStore

  beforeEach(() => {
    mockNats = new MockNATSClient()
    store = new EventStore({
      natsClient: mockNats,
      userId: 'user-1',
      projectId: 'project-1',
      sessionId: 'session-1',
    })
  })

  it('parses attachment_ref content block in a user event', () => {
    const userEvent: StreamJsonEvent = {
      type: 'user',
      message: {
        role: 'user',
        content: [
          { type: 'text', text: 'check this file' },
          {
            type: 'attachment_ref',
            id: 'att-001',
            filename: 'screenshot.png',
            mimeType: 'image/png',
            sizeBytes: 245000,
          } as unknown as { type: 'text'; text: string },
        ],
      },
    }
    store.applyEventForTest(userEvent)

    const { turns } = store.conversation
    const userTurn = turns.find(t => t.type === 'user')
    expect(userTurn).toBeDefined()
    const textBlock = userTurn?.blocks.find(b => b.type === 'text')
    expect(textBlock).toBeDefined()
    const attBlock = userTurn?.blocks.find(b => b.type === 'attachment_ref') as AttachmentRefBlock | undefined
    expect(attBlock).toBeDefined()
    expect(attBlock?.id).toBe('att-001')
    expect(attBlock?.filename).toBe('screenshot.png')
    expect(attBlock?.mimeType).toBe('image/png')
    expect(attBlock?.sizeBytes).toBe(245000)
  })

  it('handles attachment_ref-only user event (no text)', () => {
    const userEvent: StreamJsonEvent = {
      type: 'user',
      message: {
        role: 'user',
        content: [
          {
            type: 'attachment_ref',
            id: 'att-002',
            filename: 'report.pdf',
            mimeType: 'application/pdf',
            sizeBytes: 102400,
          } as unknown as { type: 'text'; text: string },
        ],
      },
    }
    store.applyEventForTest(userEvent)

    const { turns } = store.conversation
    const userTurn = turns.find(t => t.type === 'user')
    expect(userTurn).toBeDefined()
    expect(userTurn?.blocks).toHaveLength(1)
    const attBlock = userTurn?.blocks[0] as AttachmentRefBlock
    expect(attBlock.type).toBe('attachment_ref')
    expect(attBlock.id).toBe('att-002')
  })

  it('applies default values for missing attachment_ref fields', () => {
    const userEvent: StreamJsonEvent = {
      type: 'user',
      message: {
        role: 'user',
        content: [
          {
            type: 'attachment_ref',
            id: 'att-003',
            // no filename, mimeType, sizeBytes
          } as unknown as { type: 'text'; text: string },
        ],
      },
    }
    store.applyEventForTest(userEvent)

    const { turns } = store.conversation
    const userTurn = turns.find(t => t.type === 'user')
    const attBlock = userTurn?.blocks[0] as AttachmentRefBlock | undefined
    expect(attBlock?.type).toBe('attachment_ref')
    expect(attBlock?.filename).toBe('file')
    expect(attBlock?.mimeType).toBe('application/octet-stream')
    expect(attBlock?.sizeBytes).toBe(0)
  })

  it('ignores attachment_ref blocks with missing id', () => {
    const userEvent: StreamJsonEvent = {
      type: 'user',
      message: {
        role: 'user',
        content: [
          {
            type: 'attachment_ref',
            // no id
          } as unknown as { type: 'text'; text: string },
        ],
      },
    }
    store.applyEventForTest(userEvent)

    const { turns } = store.conversation
    // Turn should not be created if there are no valid blocks
    // (if no valid blocks, turn.blocks.length === 0 → not pushed)
    const userTurn = turns.find(t => t.type === 'user')
    if (userTurn) {
      const attBlock = userTurn.blocks.find(b => b.type === 'attachment_ref')
      expect(attBlock).toBeUndefined()
    }
  })

  it('attachment_ref appears in pending message turn when addPendingMessage is called', () => {
    const pendingContent = [
      { type: 'text', text: 'see attached' },
      { type: 'attachment_ref', id: 'att-005', filename: 'data.csv', mimeType: 'text/csv', sizeBytes: 500 },
    ] as Array<{ type: string; text?: string; source?: { type: string; media_type: string; data: string } }>

    store.addPendingMessage('uuid-1', pendingContent)

    const { turns } = store.conversation
    const pendingTurn = turns.find(t => t.pendingUuid === 'uuid-1')
    expect(pendingTurn).toBeDefined()
    const textBlock = pendingTurn?.blocks.find(b => b.type === 'text')
    expect(textBlock).toBeDefined()
    const attBlock = pendingTurn?.blocks.find(b => b.type === 'attachment_ref') as AttachmentRefBlock | undefined
    expect(attBlock).toBeDefined()
    expect(attBlock?.id).toBe('att-005')
    expect(attBlock?.filename).toBe('data.csv')
    expect(attBlock?.mimeType).toBe('text/csv')
    expect(attBlock?.sizeBytes).toBe(500)
  })
})
