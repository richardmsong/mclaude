import { describe, it, expect, beforeEach } from 'vitest'
import { EventStore } from './event-store'
import { MockNATSClient } from '../testutil/mock-nats'
import { transcripts } from '../testutil/fixtures'
import type { StreamJsonEvent } from '@/types'

describe('EventStore', () => {
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

  describe('simpleMessage transcript', () => {
    it('has user turn + assistant streaming turn during streaming', () => {
      // Apply up through stream_events (before assistant event)
      const events = transcripts.simpleMessage
      // system init, user, stream_event x2
      for (let i = 0; i < 4; i++) {
        store.applyEventForTest(events[i])
      }
      const { turns } = store.conversation
      const userTurns = turns.filter(t => t.type === 'user')
      const assistantTurns = turns.filter(t => t.type === 'assistant')
      expect(userTurns).toHaveLength(1)
      expect(assistantTurns).toHaveLength(1)
      const streamingBlock = assistantTurns[0].blocks.find(b => b.type === 'streaming_text')
      expect(streamingBlock).toBeDefined()
      expect(streamingBlock?.type).toBe('streaming_text')
      if (streamingBlock?.type === 'streaming_text') {
        expect(streamingBlock.complete).toBe(false)
        expect(streamingBlock.chunks.join('')).toBe('Hello, world!')
      }
    })

    it('finalizes streaming block after assistant event', () => {
      // Apply all events
      for (const event of transcripts.simpleMessage) {
        store.applyEventForTest(event)
      }
      const { turns } = store.conversation
      const assistantTurn = turns.find(t => t.type === 'assistant')
      expect(assistantTurn).toBeDefined()
      const streamingBlock = assistantTurn?.blocks.find(b => b.type === 'streaming_text')
      expect(streamingBlock).toBeDefined()
      if (streamingBlock?.type === 'streaming_text') {
        expect(streamingBlock.complete).toBe(true)
        expect(streamingBlock.chunks.join('')).toBe('Hello, world!')
      }
    })

    it('sets model and usage on assistant turn', () => {
      for (const event of transcripts.simpleMessage) {
        store.applyEventForTest(event)
      }
      const assistantTurn = store.conversation.turns.find(t => t.type === 'assistant')
      expect(assistantTurn?.model).toBe('claude-sonnet-4-6')
      expect(assistantTurn?.usage?.inputTokens).toBe(10)
      expect(assistantTurn?.usage?.outputTokens).toBe(5)
    })
  })

  describe('toolUse transcript', () => {
    it('has ToolUseBlock after assistant event', () => {
      const events = transcripts.toolUse
      // user + assistant
      store.applyEventForTest(events[0])
      store.applyEventForTest(events[1])
      const assistantTurn = store.conversation.turns.find(t => t.type === 'assistant')
      expect(assistantTurn).toBeDefined()
      const toolUseBlock = assistantTurn?.blocks.find(b => b.type === 'tool_use')
      expect(toolUseBlock).toBeDefined()
      if (toolUseBlock?.type === 'tool_use') {
        expect(toolUseBlock.id).toBe('tool-1')
        expect(toolUseBlock.name).toBe('Bash')
      }
    })

    it('sets elapsed after tool_progress event', () => {
      store.applyEventForTest(transcripts.toolUse[0])
      store.applyEventForTest(transcripts.toolUse[1])
      store.applyEventForTest(transcripts.toolUse[2]) // tool_progress
      const assistantTurn = store.conversation.turns.find(t => t.type === 'assistant')
      const toolUseBlock = assistantTurn?.blocks.find(b => b.type === 'tool_use')
      if (toolUseBlock?.type === 'tool_use') {
        expect(toolUseBlock.elapsed).toBe(500)
      }
    })

    it('attaches tool result to ToolUseBlock after user tool_result', () => {
      for (const event of transcripts.toolUse) {
        store.applyEventForTest(event)
      }
      const assistantTurn = store.conversation.turns.find(t => t.type === 'assistant')
      const toolUseBlock = assistantTurn?.blocks.find(b => b.type === 'tool_use')
      if (toolUseBlock?.type === 'tool_use') {
        expect(toolUseBlock.result).toBeDefined()
        expect(toolUseBlock.result?.content).toBe('file1.txt\nfile2.txt')
        expect(toolUseBlock.result?.isError).toBe(false)
      }
    })

    it('does not create a user turn for tool_result', () => {
      for (const event of transcripts.toolUse) {
        store.applyEventForTest(event)
      }
      // Should only have 1 user turn (the initial "Run ls" message)
      const userTurns = store.conversation.turns.filter(t => t.type === 'user')
      expect(userTurns).toHaveLength(1)
    })
  })

  describe('permissionRequest transcript', () => {
    it('creates ControlRequestBlock with status=pending', () => {
      for (const event of transcripts.permissionRequest) {
        store.applyEventForTest(event)
      }
      const turns = store.conversation.turns
      let found = false
      for (const turn of turns) {
        for (const block of turn.blocks) {
          if (block.type === 'control_request') {
            expect(block.status).toBe('pending')
            expect(block.requestId).toBe('req-1')
            expect(block.toolName).toBe('Bash')
            found = true
          }
        }
      }
      expect(found).toBe(true)
    })
  })

  describe('compaction transcript', () => {
    it('resets conversation to a single CompactionBlock after compact_boundary', () => {
      // First add some events
      for (const event of transcripts.simpleMessage) {
        store.applyEventForTest(event)
      }
      // Now apply compaction
      store.applyEventForTest(transcripts.compaction[0])
      const { turns } = store.conversation
      expect(turns).toHaveLength(1)
      expect(turns[0].type).toBe('system')
      expect(turns[0].blocks).toHaveLength(1)
      expect(turns[0].blocks[0].type).toBe('compaction')
      if (turns[0].blocks[0].type === 'compaction') {
        expect(turns[0].blocks[0].summary).toContain('Context compacted')
      }
    })

    it('allows subsequent turns after compaction', () => {
      for (const event of transcripts.compaction) {
        store.applyEventForTest(event)
      }
      // After compaction + user message
      const { turns } = store.conversation
      expect(turns.length).toBeGreaterThanOrEqual(2)
      const userTurn = turns.find(t => t.type === 'user')
      expect(userTurn).toBeDefined()
    })
  })

  describe('clear event', () => {
    it('resets conversation to empty turns', () => {
      for (const event of transcripts.simpleMessage) {
        store.applyEventForTest(event)
      }
      expect(store.conversation.turns.length).toBeGreaterThan(0)

      const clearEvent: StreamJsonEvent = { type: 'clear' }
      store.applyEventForTest(clearEvent)
      expect(store.conversation.turns).toHaveLength(0)
    })

    it('clears _pendingMessages on clear event', () => {
      store.addPendingMessage('uuid-1', 'Hello')
      store.addPendingMessage('uuid-2', 'World')
      expect(store.pendingMessages).toHaveLength(2)

      const clearEvent: StreamJsonEvent = { type: 'clear' }
      store.applyEventForTest(clearEvent)
      expect(store.pendingMessages).toHaveLength(0)
    })
  })

  describe('compact_boundary clears pendingMessages', () => {
    it('clears _pendingMessages on compact_boundary', () => {
      store.addPendingMessage('uuid-1', 'In-flight message')
      expect(store.pendingMessages).toHaveLength(1)

      store.applyEventForTest(transcripts.compaction[0])
      expect(store.pendingMessages).toHaveLength(0)
    })
  })

  describe('deduplication', () => {
    it('feeding the same event twice (same sequence number) does NOT create duplicate turns', () => {
      const event: StreamJsonEvent = { type: 'user', message: { role: 'user', content: 'Hello' } }
      store.applyEventForTest(event, 1)
      store.applyEventForTest(event, 1) // same seq
      const userTurns = store.conversation.turns.filter(t => t.type === 'user')
      expect(userTurns).toHaveLength(1)
    })

    it('events with increasing sequence numbers are both applied', () => {
      const event1: StreamJsonEvent = { type: 'user', message: { role: 'user', content: 'Hello' } }
      const event2: StreamJsonEvent = { type: 'user', message: { role: 'user', content: 'World' } }
      store.applyEventForTest(event1, 1)
      store.applyEventForTest(event2, 2)
      const userTurns = store.conversation.turns.filter(t => t.type === 'user')
      expect(userTurns).toHaveLength(2)
    })

    describe('uuid-based pending message matching', () => {
      it('removes matching pending message when user event with same uuid arrives', () => {
        store.addPendingMessage('uuid-1', 'Hello')
        expect(store.pendingMessages).toHaveLength(1)

        const event: StreamJsonEvent = { type: 'user', message: { role: 'user', content: 'Hello' }, uuid: 'uuid-1', isReplay: true }
        store.applyEventForTest(event, 2)

        // Pending message removed
        expect(store.pendingMessages).toHaveLength(0)
        // Inline user turn created
        const userTurns = store.conversation.turns.filter(t => t.type === 'user')
        expect(userTurns).toHaveLength(1)
        expect(userTurns[0].blocks[0].type === 'text' && userTurns[0].blocks[0].text).toBe('Hello')
      })

      it('does NOT remove pending when uuid does not match', () => {
        store.addPendingMessage('uuid-1', 'Hello')

        const event: StreamJsonEvent = { type: 'user', message: { role: 'user', content: 'Hello' }, uuid: 'uuid-2', isReplay: true }
        store.applyEventForTest(event, 2)

        // Pending message still present (uuid didn't match)
        expect(store.pendingMessages).toHaveLength(1)
        // But inline user turn still created
        const userTurns = store.conversation.turns.filter(t => t.type === 'user')
        expect(userTurns).toHaveLength(1)
      })

      it('creates inline user turn when no uuid present', () => {
        const event: StreamJsonEvent = { type: 'user', message: { role: 'user', content: 'Hello' } }
        store.applyEventForTest(event, 1)

        const userTurns = store.conversation.turns.filter(t => t.type === 'user')
        expect(userTurns).toHaveLength(1)
      })

      it('creates system turn for synthetic replay', () => {
        const event: StreamJsonEvent = {
          type: 'user',
          message: { role: 'user', content: 'Background task completed' },
          uuid: 'uuid-1',
          isReplay: true,
          isSynthetic: true,
        }
        store.applyEventForTest(event, 1)

        const systemTurns = store.conversation.turns.filter(t => t.type === 'system')
        expect(systemTurns).toHaveLength(1)
        expect(systemTurns[0].blocks[0].type).toBe('system_message')
        if (systemTurns[0].blocks[0].type === 'system_message') {
          expect(systemTurns[0].blocks[0].text).toBe('Background task completed')
        }
        // No user turn for synthetic
        expect(store.conversation.turns.filter(t => t.type === 'user')).toHaveLength(0)
      })
    })
  })

  describe('system.init event', () => {
    it('sets model and capabilities on the EventStore', () => {
      const initEvent = transcripts.simpleMessage[0] // system init
      store.applyEventForTest(initEvent)
      expect(store.model).toBe('claude-sonnet-4-6')
      expect(store.capabilities.skills).toContain('commit')
      expect(store.capabilities.tools).toContain('Bash')
    })
  })

  describe('system.session_state_changed event', () => {
    it('updates sessionState', () => {
      expect(store.sessionState).toBe('idle')
      const stateChangedEvent: StreamJsonEvent = {
        type: 'system',
        subtype: 'session_state_changed',
        state: 'running',
      }
      store.applyEventForTest(stateChangedEvent)
      expect(store.sessionState).toBe('running')
    })

    it('can transition to requires_action', () => {
      const event: StreamJsonEvent = {
        type: 'system',
        subtype: 'session_state_changed',
        state: 'requires_action',
      }
      store.applyEventForTest(event)
      expect(store.sessionState).toBe('requires_action')
    })
  })

  describe('parent_tool_use_id', () => {
    it('event with parent_tool_use_id creates turn with parentToolUseId', () => {
      const event: StreamJsonEvent = {
        type: 'stream_event',
        parent_tool_use_id: 'parent-tool-1',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'sub-result' }, index: 0 },
      }
      store.applyEventForTest(event)
      const assistantTurn = store.conversation.turns.find(t => t.type === 'assistant')
      expect(assistantTurn?.parentToolUseId).toBe('parent-tool-1')
    })
  })

  describe('listener notifications', () => {
    it('fires onConversationChanged after applyEventForTest', () => {
      let callCount = 0
      store.onConversationChanged(() => { callCount++ })
      store.applyEventForTest({ type: 'user', message: { role: 'user', content: 'Hi' } })
      expect(callCount).toBe(1)
    })

    it('unsubscribe stops notifications', () => {
      let callCount = 0
      const unsub = store.onConversationChanged(() => { callCount++ })
      store.applyEventForTest({ type: 'user', message: { role: 'user', content: 'Hi' } })
      unsub()
      store.applyEventForTest({ type: 'user', message: { role: 'user', content: 'Hi 2' } })
      expect(callCount).toBe(1)
    })
  })
})
