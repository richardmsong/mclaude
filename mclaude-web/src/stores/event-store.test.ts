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
        // addPendingMessage already inserted 1 optimistic turn; event with non-matching uuid inserts a second
        const userTurns = store.conversation.turns.filter(t => t.type === 'user')
        expect(userTurns).toHaveLength(2)
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

  // ─── Skill invocation chip ───────────────────────────────────────────────────

  describe('skill invocation parsing', () => {
    it('creates a user turn with SkillInvocationBlock when content starts with "Base directory for this skill:"', () => {
      const event: StreamJsonEvent = {
        type: 'user',
        message: {
          role: 'user',
          content: 'Base directory for this skill: /data/worktrees/main/.claude/skills/feature-change\n\nARGUMENTS:\nFix two event-store bugs in the SPA',
        },
      }
      store.applyEventForTest(event, 1)

      const userTurns = store.conversation.turns.filter(t => t.type === 'user')
      expect(userTurns).toHaveLength(1)
      expect(userTurns[0].blocks).toHaveLength(1)
      const block = userTurns[0].blocks[0]
      expect(block.type).toBe('skill_invocation')
      if (block.type === 'skill_invocation') {
        expect(block.skillName).toBe('feature-change')
        expect(block.args).toBe('Fix two event-store bugs in the SPA')
        expect(block.rawContent).toContain('Base directory for this skill:')
      }
    })

    it('extracts args from after the ARGUMENTS: line', () => {
      const event: StreamJsonEvent = {
        type: 'user',
        message: {
          role: 'user',
          content: 'Base directory for this skill: /path/to/skills/my-skill\n\nSome preamble\nARGUMENTS:\nline1\nline2',
        },
      }
      store.applyEventForTest(event, 1)

      const block = store.conversation.turns[0].blocks[0]
      if (block.type === 'skill_invocation') {
        expect(block.skillName).toBe('my-skill')
        expect(block.args).toBe('line1\nline2')
      }
    })

    it('sets args to empty string when no ARGUMENTS: line present', () => {
      const event: StreamJsonEvent = {
        type: 'user',
        message: {
          role: 'user',
          content: 'Base directory for this skill: /path/to/skills/no-args-skill\n\nNo arguments section here.',
        },
      }
      store.applyEventForTest(event, 1)

      const block = store.conversation.turns[0].blocks[0]
      if (block.type === 'skill_invocation') {
        expect(block.skillName).toBe('no-args-skill')
        expect(block.args).toBe('')
      }
    })
  })

  // ─── System notification filter ───────────────────────────────────────────────

  describe('system notification filter', () => {
    it('does NOT create a turn when content starts with "[SYSTEM NOTIFICATION"', () => {
      const event: StreamJsonEvent = {
        type: 'user',
        message: {
          role: 'user',
          content: '[SYSTEM NOTIFICATION] harness check-in: task is still running',
        },
      }
      store.applyEventForTest(event, 1)

      expect(store.conversation.turns).toHaveLength(0)
    })

    it('discards system notification with additional content after the prefix', () => {
      const event: StreamJsonEvent = {
        type: 'user',
        message: {
          role: 'user',
          content: '[SYSTEM NOTIFICATION — 12:34:56] You have been idle for 5 minutes.',
        },
      }
      store.applyEventForTest(event, 1)

      expect(store.conversation.turns).toHaveLength(0)
    })
  })

  // ─── Normal user text regression ─────────────────────────────────────────────

  describe('normal user text (regression)', () => {
    it('creates a TextBlock turn for ordinary user messages', () => {
      const event: StreamJsonEvent = {
        type: 'user',
        message: { role: 'user', content: 'Hello, Claude!' },
      }
      store.applyEventForTest(event, 1)

      const userTurns = store.conversation.turns.filter(t => t.type === 'user')
      expect(userTurns).toHaveLength(1)
      expect(userTurns[0].blocks).toHaveLength(1)
      const block = userTurns[0].blocks[0]
      expect(block.type).toBe('text')
      if (block.type === 'text') {
        expect(block.text).toBe('Hello, Claude!')
      }
    })

    it('does NOT create a SkillInvocationBlock for non-skill messages', () => {
      const event: StreamJsonEvent = {
        type: 'user',
        message: { role: 'user', content: 'fix the bug please' },
      }
      store.applyEventForTest(event, 1)

      const block = store.conversation.turns[0]?.blocks[0]
      expect(block?.type).toBe('text')
    })
  })

  // ─── Bug 1: user turn ordering ───────────────────────────────────────────────

  describe('Bug 1 — user turn ordering', () => {
    it('addPendingMessage immediately inserts an optimistic user turn into turns[]', () => {
      store.addPendingMessage('uuid-opt', 'Do something')
      const turns = store.conversation.turns
      const userTurns = turns.filter(t => t.type === 'user')
      expect(userTurns).toHaveLength(1)
      expect(userTurns[0].blocks[0].type === 'text' && userTurns[0].blocks[0].text).toBe('Do something')
    })

    it('optimistic user turn appears BEFORE subsequent assistant turns', () => {
      store.addPendingMessage('uuid-opt', 'Do something')

      // Assistant starts streaming
      const streamEvt: StreamJsonEvent = {
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'Sure!' }, index: 0 },
      }
      store.applyEventForTest(streamEvt)

      const turns = store.conversation.turns
      const userIdx = turns.findIndex(t => t.type === 'user')
      const assistantIdx = turns.findIndex(t => t.type === 'assistant')
      expect(userIdx).toBeGreaterThanOrEqual(0)
      expect(assistantIdx).toBeGreaterThanOrEqual(0)
      expect(userIdx).toBeLessThan(assistantIdx)
    })

    it('server echo with matching uuid does NOT create a duplicate user turn', () => {
      store.addPendingMessage('uuid-opt', 'Do something')

      // Assistant streams and finalizes
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'Sure!' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'assistant',
        message: { id: 'msg-x', role: 'assistant', content: [{ type: 'text', text: 'Sure!' }], model: 'claude-sonnet-4-6' },
      })

      // Server echoes the user message back
      store.applyEventForTest({
        type: 'user',
        message: { role: 'user', content: 'Do something' },
        uuid: 'uuid-opt',
        isReplay: true,
      })

      const userTurns = store.conversation.turns.filter(t => t.type === 'user')
      // Still exactly one user turn — the optimistic turn was confirmed, not duplicated
      expect(userTurns).toHaveLength(1)
    })

    it('confirmed user turn (after echo) has no pendingUuid', () => {
      store.addPendingMessage('uuid-opt', 'Do something')

      store.applyEventForTest({
        type: 'user',
        message: { role: 'user', content: 'Do something' },
        uuid: 'uuid-opt',
        isReplay: true,
      })

      const userTurns = store.conversation.turns.filter(t => t.type === 'user')
      expect(userTurns).toHaveLength(1)
      expect(userTurns[0].pendingUuid).toBeUndefined()
    })

    it('optimistic turn remains in turns[] before echo, with pendingUuid set', () => {
      store.addPendingMessage('uuid-opt', 'In flight')
      const userTurns = store.conversation.turns.filter(t => t.type === 'user')
      expect(userTurns).toHaveLength(1)
      expect(userTurns[0].pendingUuid).toBe('uuid-opt')
    })

    it('user turn appears before assistant turns in turns[] index after full cycle', () => {
      store.addPendingMessage('uuid-opt', 'Do something')
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'Sure!' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'assistant',
        message: { id: 'msg-x', role: 'assistant', content: [{ type: 'text', text: 'Sure!' }], model: 'claude-sonnet-4-6' },
      })
      store.applyEventForTest({
        type: 'user',
        message: { role: 'user', content: 'Do something' },
        uuid: 'uuid-opt',
        isReplay: true,
      })

      const turns = store.conversation.turns
      const userIdx = turns.findIndex(t => t.type === 'user')
      const assistantIdx = turns.findIndex(t => t.type === 'assistant')
      expect(userIdx).toBeLessThan(assistantIdx)
    })
  })

  // ─── Bug 2: sub-agent turn scoping ───────────────────────────────────────────

  describe('Bug 2 — sub-agent turn scoping', () => {
    it('stream_event with parent_tool_use_id creates assistant turn with that parentToolUseId', () => {
      const event: StreamJsonEvent = {
        type: 'stream_event',
        parent_tool_use_id: 'toolu_agent_abc',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'sub output' }, index: 0 },
      }
      store.applyEventForTest(event)
      const turns = store.conversation.turns
      const subTurn = turns.find(t => t.type === 'assistant' && t.parentToolUseId === 'toolu_agent_abc')
      expect(subTurn).toBeDefined()
      expect(subTurn?.parentToolUseId).toBe('toolu_agent_abc')
    })

    it('stream_event with parent_tool_use_id does NOT append to a top-level assistant turn', () => {
      // First create a top-level assistant turn
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'top level' }, index: 0 },
      })
      // Now sub-agent event with parent_tool_use_id
      store.applyEventForTest({
        type: 'stream_event',
        parent_tool_use_id: 'toolu_agent_abc',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'sub' }, index: 0 },
      })
      const topLevelTurns = store.conversation.turns.filter(t => t.type === 'assistant' && !t.parentToolUseId)
      const subTurns = store.conversation.turns.filter(t => t.type === 'assistant' && t.parentToolUseId === 'toolu_agent_abc')
      // There must be exactly 1 top-level assistant turn and 1 sub-agent turn
      expect(topLevelTurns).toHaveLength(1)
      expect(subTurns).toHaveLength(1)
      // The top-level turn must NOT contain sub-agent text
      const topText = topLevelTurns[0].blocks.find(b => b.type === 'streaming_text')
      expect(topText?.type === 'streaming_text' && topText.chunks.join('')).toBe('top level')
    })

    it('assistant event with parent_tool_use_id creates turn with that parentToolUseId', () => {
      const event: StreamJsonEvent = {
        type: 'assistant',
        parent_tool_use_id: 'toolu_agent_xyz',
        message: {
          id: 'msg-sub',
          role: 'assistant',
          content: [{ type: 'tool_use', id: 'bash-1', name: 'Bash', input: { command: 'ls' } }],
          model: 'claude-sonnet-4-6',
        },
      }
      store.applyEventForTest(event)
      const subTurn = store.conversation.turns.find(t => t.type === 'assistant' && t.parentToolUseId === 'toolu_agent_xyz')
      expect(subTurn).toBeDefined()
      const toolBlock = subTurn?.blocks.find(b => b.type === 'tool_use')
      expect(toolBlock?.type === 'tool_use' && toolBlock.name).toBe('Bash')
    })

    it('assistant event with parent_tool_use_id does NOT append to top-level turn', () => {
      // Top-level assistant turn (no parent)
      store.applyEventForTest({
        type: 'assistant',
        message: {
          id: 'msg-top',
          role: 'assistant',
          content: [{ type: 'tool_use', id: 'agent-tool', name: 'Agent', input: {} }],
          model: 'claude-sonnet-4-6',
        },
      })
      // Sub-agent assistant event
      store.applyEventForTest({
        type: 'assistant',
        parent_tool_use_id: 'agent-tool',
        message: {
          id: 'msg-sub',
          role: 'assistant',
          content: [{ type: 'tool_use', id: 'bash-sub', name: 'Bash', input: { command: 'pwd' } }],
          model: 'claude-sonnet-4-6',
        },
      })

      const topLevelTurns = store.conversation.turns.filter(t => t.type === 'assistant' && !t.parentToolUseId)
      const subTurns = store.conversation.turns.filter(t => t.type === 'assistant' && t.parentToolUseId === 'agent-tool')
      expect(topLevelTurns).toHaveLength(1)
      expect(subTurns).toHaveLength(1)
      // Top-level should only have the Agent tool block
      expect(topLevelTurns[0].blocks).toHaveLength(1)
      expect(topLevelTurns[0].blocks[0].type === 'tool_use' && topLevelTurns[0].blocks[0].name).toBe('Agent')
      // Sub turn should have the Bash tool block
      expect(subTurns[0].blocks[0].type === 'tool_use' && subTurns[0].blocks[0].name).toBe('Bash')
    })

    it('consecutive sub-agent stream_events with the same parent_tool_use_id append to the same sub turn', () => {
      store.applyEventForTest({
        type: 'stream_event',
        parent_tool_use_id: 'toolu_agent_abc',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'chunk1' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'stream_event',
        parent_tool_use_id: 'toolu_agent_abc',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'chunk2' }, index: 0 },
      })
      const subTurns = store.conversation.turns.filter(t => t.type === 'assistant' && t.parentToolUseId === 'toolu_agent_abc')
      expect(subTurns).toHaveLength(1)
      const block = subTurns[0].blocks.find(b => b.type === 'streaming_text')
      expect(block?.type === 'streaming_text' && block.chunks.join('')).toBe('chunk1chunk2')
    })

    it('two different sub-agents create separate turns', () => {
      store.applyEventForTest({
        type: 'stream_event',
        parent_tool_use_id: 'toolu_agent_1',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'agent1' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'stream_event',
        parent_tool_use_id: 'toolu_agent_2',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'agent2' }, index: 0 },
      })
      const turns1 = store.conversation.turns.filter(t => t.parentToolUseId === 'toolu_agent_1')
      const turns2 = store.conversation.turns.filter(t => t.parentToolUseId === 'toolu_agent_2')
      expect(turns1).toHaveLength(1)
      expect(turns2).toHaveLength(1)
    })
  })

  describe('JetStream replay ordering: stream_events arrive before user echo', () => {
    // With --replay-user-messages, Claude publishes stream_events BEFORE the
    // user echo in the JetStream sequence:
    //   stream_event(47) → stream_event(48) → user-echo(49) → assistant(50)
    //
    // During replay the EventStore must still produce: [user] → [assistant]
    // not: [assistant] → [user].

    it('user echo inserted before the streaming assistant turn it caused', () => {
      // Apply stream_events first (as they arrive in JetStream)
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'hello ' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'world' }, index: 0 },
      })

      // Then user echo arrives (--replay-user-messages)
      store.applyEventForTest({
        type: 'user',
        uuid: 'replay-uuid-1',
        isReplay: true,
        message: { role: 'user', content: 'say hello' },
      })

      // Must be: [user-turn, asst-turn] — NOT [asst-turn, user-turn]
      expect(store.conversation.turns).toHaveLength(2)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[1].type).toBe('assistant')

      const userText = store.conversation.turns[0].blocks.find(b => b.type === 'text')
      expect(userText?.type === 'text' && userText.text).toBe('say hello')

      const streaming = store.conversation.turns[1].blocks.find(b => b.type === 'streaming_text')
      expect(streaming?.type === 'streaming_text' && streaming.chunks.join('')).toBe('hello world')
    })

    it('full replay sequence: two exchanges produce correct order', () => {
      // Simulate replaying two user/assistant exchanges, each with
      // stream_events arriving before the user echo.

      // --- Exchange 1 ---
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'response1' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-1',
        isReplay: true,
        message: { role: 'user', content: 'message1' },
      })
      store.applyEventForTest({
        type: 'assistant',
        message: { id: 'msg-1', role: 'assistant', content: [{ type: 'text', text: 'response1' }], model: 'claude-test' },
      })

      // --- Exchange 2 ---
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'response2' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-2',
        isReplay: true,
        message: { role: 'user', content: 'message2' },
      })
      store.applyEventForTest({
        type: 'assistant',
        message: { id: 'msg-2', role: 'assistant', content: [{ type: 'text', text: 'response2' }], model: 'claude-test' },
      })

      // Correct order: user1, asst1, user2, asst2
      expect(store.conversation.turns).toHaveLength(4)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[1].type).toBe('assistant')
      expect(store.conversation.turns[2].type).toBe('user')
      expect(store.conversation.turns[3].type).toBe('assistant')

      const user1Text = store.conversation.turns[0].blocks.find(b => b.type === 'text')
      expect(user1Text?.type === 'text' && user1Text.text).toBe('message1')
      const user2Text = store.conversation.turns[2].blocks.find(b => b.type === 'text')
      expect(user2Text?.type === 'text' && user2Text.text).toBe('message2')
    })
  })

  describe('new message in existing session (regression: response must not append to prior turn)', () => {
    // Reproduce the bug: user sends a message in an existing session.
    // The prior assistant turn is finalized. A new stream_event for the NEW
    // response must create a fresh assistant turn AFTER the user's message —
    // not append into the old finalized assistant turn.
    it('new stream_event after a finalized assistant turn creates a new assistant turn', () => {
      // Simulate a completed prior exchange
      store.applyEventForTest({
        type: 'user',
        message: { role: 'user', content: 'first message' },
      })
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'first ' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'response' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'assistant',
        message: {
          id: 'msg-1',
          role: 'assistant',
          content: [{ type: 'text', text: 'first response' }],
          model: 'claude-test',
        },
      })

      // Confirm prior exchange: [user, asst(finalized)]
      expect(store.conversation.turns).toHaveLength(2)
      const priorAsst = store.conversation.turns[1]
      expect(priorAsst.type).toBe('assistant')
      const streamBlock = priorAsst.blocks.find(b => b.type === 'streaming_text')
      expect(streamBlock?.type === 'streaming_text' && streamBlock.complete).toBe(true)

      // User sends second message (optimistic turn added first)
      store.addPendingMessage('uuid-2nd', 'second message')
      expect(store.conversation.turns).toHaveLength(3)
      expect(store.conversation.turns[2].pendingUuid).toBe('uuid-2nd')

      // New response starts streaming BEFORE the echo arrives
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'second ' }, index: 0 },
      })

      // Must have created a NEW assistant turn, NOT appended to the prior one
      expect(store.conversation.turns).toHaveLength(4)
      const newAsst = store.conversation.turns[3]
      expect(newAsst.type).toBe('assistant')

      // Prior assistant turn must be unchanged (no new streaming appended)
      const priorStreamBlock = priorAsst.blocks.find(b => b.type === 'streaming_text')
      expect(priorStreamBlock?.type === 'streaming_text' && priorStreamBlock.chunks.join('')).toBe('first response')

      // Order: [user-0, asst-1(old), user-2(pending), asst-3(new)]
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[1].type).toBe('assistant')
      expect(store.conversation.turns[2].type).toBe('user')
      expect(store.conversation.turns[3].type).toBe('assistant')

      // Echo arrives and confirms the optimistic turn
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-2nd',
        isReplay: true,
        message: { role: 'user', content: 'second message' },
      })
      // Still 4 turns — optimistic turn confirmed, no duplicate
      expect(store.conversation.turns).toHaveLength(4)
      expect(store.conversation.turns[2].pendingUuid).toBeUndefined()
      expect(store.conversation.turns[2].type).toBe('user')
      expect(store.conversation.turns[3].type).toBe('assistant')
    })
  })
  // ─── Pending message inline repositioning (spec: plan-replay-user-messages) ──

  describe('pending message inline repositioning on echo', () => {
    it('idle-state send: echo with no streaming assistant turn appends user turn at end', () => {
      // No assistant turn in progress — echo should land at the end (same as before)
      store.addPendingMessage('uuid-idle', 'Hello Claude')
      expect(store.conversation.turns).toHaveLength(1)
      expect(store.conversation.turns[0].pendingUuid).toBe('uuid-idle')

      // Echo arrives — no streaming assistant turn exists
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-idle',
        isReplay: true,
        message: { role: 'user', content: 'Hello Claude' },
      })

      // User turn confirmed at the bottom (no assistant to reorder around)
      expect(store.conversation.turns).toHaveLength(1)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[0].pendingUuid).toBeUndefined()
      const textBlock = store.conversation.turns[0].blocks[0]
      expect(textBlock.type === 'text' && textBlock.text).toBe('Hello Claude')
    })

    it('mid-turn send: echo moves user turn BEFORE the streaming assistant turn', () => {
      // Pre-populate a streaming assistant turn (Claude is mid-response)
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'thinking...' }, index: 0 },
      })
      expect(store.conversation.turns).toHaveLength(1)
      expect(store.conversation.turns[0].type).toBe('assistant')

      // User sends a message mid-turn: dim turn appears AFTER the streaming asst
      store.addPendingMessage('uuid-mid', 'do it like this')
      expect(store.conversation.turns).toHaveLength(2)
      expect(store.conversation.turns[0].type).toBe('assistant')
      expect(store.conversation.turns[1].type).toBe('user')
      expect(store.conversation.turns[1].pendingUuid).toBe('uuid-mid')

      // Echo arrives — user turn must move BEFORE the streaming assistant turn
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-mid',
        isReplay: true,
        message: { role: 'user', content: 'do it like this' },
      })

      expect(store.conversation.turns).toHaveLength(2)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[0].pendingUuid).toBeUndefined()
      expect(store.conversation.turns[1].type).toBe('assistant')
      const userText = store.conversation.turns[0].blocks.find(b => b.type === 'text')
      expect(userText?.type === 'text' && userText.text).toBe('do it like this')
    })

    it('batched 3-message send: each echo promotes its pending turn inline individually', () => {
      // Scenario: user-A is sent and a streaming response starts. Then user-B
      // and user-C are queued as pending WHILE the response to A is streaming.
      // This matches the spec flow where pending messages appear at the bottom
      // of the chat below active assistant content.

      // Message A sent, pending turn appears
      store.addPendingMessage('uuid-a', 'message A')
      // turns: [user-A(pending)]
      expect(store.conversation.turns).toHaveLength(1)

      // Streaming response to A starts
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'responding to A' }, index: 0 },
      })
      // turns: [user-A(pending), asst(streaming)]
      expect(store.conversation.turns).toHaveLength(2)

      // Echo for A arrives — A must move BEFORE the streaming assistant turn
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-a',
        isReplay: true,
        message: { role: 'user', content: 'message A' },
      })
      // turns: [user-A(confirmed), asst(streaming)]
      expect(store.conversation.turns).toHaveLength(2)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[0].pendingUuid).toBeUndefined()
      const blockA = store.conversation.turns[0].blocks.find(b => b.type === 'text')
      expect(blockA?.type === 'text' && blockA.text).toBe('message A')
      expect(store.conversation.turns[1].type).toBe('assistant')

      // While A's response is still streaming, user queues B and C
      store.addPendingMessage('uuid-b', 'message B')
      store.addPendingMessage('uuid-c', 'message C')
      // turns: [user-A(confirmed), asst(streaming), user-B(pending), user-C(pending)]
      expect(store.conversation.turns).toHaveLength(4)
      expect(store.conversation.turns[2].pendingUuid).toBe('uuid-b')
      expect(store.conversation.turns[3].pendingUuid).toBe('uuid-c')

      // Finalize A's response and start streaming B's response
      store.applyEventForTest({
        type: 'assistant',
        message: {
          id: 'msg-a',
          role: 'assistant',
          content: [{ type: 'text', text: 'responding to A' }],
          model: 'claude-test',
        },
      })
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'responding to B' }, index: 0 },
      })
      // turns: [user-A, asst-A(finalized), user-B(pending), user-C(pending), asst-B(streaming)]
      expect(store.conversation.turns).toHaveLength(5)
      expect(store.conversation.turns[4].type).toBe('assistant')

      // Echo for B arrives — B moves BEFORE the new streaming asst turn
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-b',
        isReplay: true,
        message: { role: 'user', content: 'message B' },
      })

      // Expected: [user-A, asst-A(finalized), user-B(confirmed), asst-B(streaming), user-C(pending)]
      expect(store.conversation.turns).toHaveLength(5)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[0].pendingUuid).toBeUndefined()
      expect(store.conversation.turns[1].type).toBe('assistant')
      expect(store.conversation.turns[2].type).toBe('user')
      expect(store.conversation.turns[2].pendingUuid).toBeUndefined()
      const blockB = store.conversation.turns[2].blocks.find(b => b.type === 'text')
      expect(blockB?.type === 'text' && blockB.text).toBe('message B')
      expect(store.conversation.turns[3].type).toBe('assistant')
      expect(store.conversation.turns[4].type).toBe('user')
      expect(store.conversation.turns[4].pendingUuid).toBe('uuid-c')
    })

    it('parentToolUseId match: echo lands before streaming asst turn under the same parent', () => {
      const parentId = 'toolu-parent-1'

      // A streaming assistant turn under parentId
      store.applyEventForTest({
        type: 'stream_event',
        parent_tool_use_id: parentId,
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'sub-agent output' }, index: 0 },
      })
      expect(store.conversation.turns).toHaveLength(1)
      expect(store.conversation.turns[0].type).toBe('assistant')
      expect(store.conversation.turns[0].parentToolUseId).toBe(parentId)

      // User sends a message tagged with the same parentToolUseId
      // (addPendingMessage doesn't take parentToolUseId, but the echo event carries it)
      store.addPendingMessage('uuid-sub', 'redirect sub-agent')
      // turns: [asst(streaming, parent=parentId), user(pending, no parent yet)]
      expect(store.conversation.turns).toHaveLength(2)

      // Echo arrives with parentToolUseId = parentId
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-sub',
        isReplay: true,
        parent_tool_use_id: parentId,
        message: { role: 'user', content: 'redirect sub-agent' },
      })

      // The user turn should be inserted before the streaming asst turn
      // (they share the same parentToolUseId)
      expect(store.conversation.turns).toHaveLength(2)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[0].pendingUuid).toBeUndefined()
      expect(store.conversation.turns[1].type).toBe('assistant')
      expect(store.conversation.turns[1].parentToolUseId).toBe(parentId)
    })
  })

  // ─── Regression: protocol order stream_events → assistant → user echo ─────────
  // With --replay-user-messages the actual JetStream sequence is:
  //   stream_event(N) → ... → assistant(N+k) → user-echo(N+k+1)
  // The user echo arrives AFTER the assistant event has already finalized the
  // streaming turn. The old code only repositioned when blocks were ALL
  // streaming_text (i.e. before finalization), so it failed here.

  describe('regression: protocol order stream_events → assistant event → user echo', () => {
    it('single exchange: user echo arrives AFTER assistant event, user still appears before assistant', () => {
      // Exact pattern from NATS dump seqs 23992-23996:
      //   stream_event → stream_event → stream_event → assistant → user
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'water' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'melon' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: '.' }, index: 0 },
      })

      // Assistant event arrives and finalizes the streaming turn
      store.applyEventForTest({
        type: 'assistant',
        message: {
          id: 'msg-wm',
          role: 'assistant',
          content: [{ type: 'text', text: 'watermelon.' }],
          model: 'claude-sonnet-4-6',
          usage: { input_tokens: 9, output_tokens: 1 },
        },
      })

      // At this point: one finalized assistant turn
      expect(store.conversation.turns).toHaveLength(1)
      expect(store.conversation.turns[0].type).toBe('assistant')

      // User echo arrives last (--replay-user-messages protocol)
      store.applyEventForTest({
        type: 'user',
        uuid: 'replay-wm',
        isReplay: true,
        message: { role: 'user', content: 'say the word watermelon and nothing else' },
      })

      // Must be: [user, assistant] — NOT [assistant, user]
      expect(store.conversation.turns).toHaveLength(2)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[1].type).toBe('assistant')

      const userText = store.conversation.turns[0].blocks.find(b => b.type === 'text')
      expect(userText?.type === 'text' && userText.text).toBe('say the word watermelon and nothing else')

      const asstStreaming = store.conversation.turns[1].blocks.find(b => b.type === 'streaming_text')
      expect(asstStreaming?.type === 'streaming_text' && asstStreaming.chunks.join('')).toBe('watermelon.')
    })

    it('full session replay with multiple exchanges all in reverse order: user turns appear before their responses', () => {
      // Simulates replaying a session where EVERY exchange follows the
      // stream_events → assistant → user-echo protocol order.
      // This is the exact bug scenario from the live session:
      //   "Test", "say pineapple", "say watermelon" — all user turns at bottom.

      // ─── Exchange 1: "Test" ───────────────────────────────────────────────
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: "Got it — everything's working" }, index: 0 },
      })
      store.applyEventForTest({
        type: 'assistant',
        message: {
          id: 'msg-1',
          role: 'assistant',
          content: [{ type: 'text', text: "Got it — everything's working" }],
          model: 'claude-sonnet-4-6',
        },
      })
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-test',
        isReplay: true,
        message: { role: 'user', content: 'Test' },
      })

      // After exchange 1: [user("Test"), asst("Got it")]
      expect(store.conversation.turns).toHaveLength(2)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[1].type).toBe('assistant')

      // ─── Exchange 2: "say pineapple" ──────────────────────────────────────
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'pineapple' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'assistant',
        message: {
          id: 'msg-2',
          role: 'assistant',
          content: [{ type: 'text', text: 'pineapple' }],
          model: 'claude-sonnet-4-6',
        },
      })
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-pineapple',
        isReplay: true,
        message: { role: 'user', content: 'say the word "pineapple" and nothing else' },
      })

      // After exchange 2: [user1, asst1, user2, asst2]
      expect(store.conversation.turns).toHaveLength(4)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[1].type).toBe('assistant')
      expect(store.conversation.turns[2].type).toBe('user')
      expect(store.conversation.turns[3].type).toBe('assistant')

      // ─── Exchange 3: "say watermelon" ─────────────────────────────────────
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'water' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'melon' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: '.' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'assistant',
        message: {
          id: 'msg-3',
          role: 'assistant',
          content: [{ type: 'text', text: 'watermelon.' }],
          model: 'claude-sonnet-4-6',
          usage: { input_tokens: 9, output_tokens: 1 },
        },
      })
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-watermelon',
        isReplay: true,
        message: { role: 'user', content: 'say the word watermelon and nothing else' },
      })

      // After exchange 3: [user1, asst1, user2, asst2, user3, asst3]
      // NOT: [asst1, asst2, asst3, user1, user2, user3]
      expect(store.conversation.turns).toHaveLength(6)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[1].type).toBe('assistant')
      expect(store.conversation.turns[2].type).toBe('user')
      expect(store.conversation.turns[3].type).toBe('assistant')
      expect(store.conversation.turns[4].type).toBe('user')
      expect(store.conversation.turns[5].type).toBe('assistant')

      // Verify content ordering
      const user1Text = store.conversation.turns[0].blocks.find(b => b.type === 'text')
      expect(user1Text?.type === 'text' && user1Text.text).toBe('Test')

      const user2Text = store.conversation.turns[2].blocks.find(b => b.type === 'text')
      expect(user2Text?.type === 'text' && user2Text.text).toBe('say the word "pineapple" and nothing else')

      const user3Text = store.conversation.turns[4].blocks.find(b => b.type === 'text')
      expect(user3Text?.type === 'text' && user3Text.text).toBe('say the word watermelon and nothing else')
    })

    it('three consecutive live sends: after full cycle each user appears before its response', () => {
      // Simulates the "batched" scenario described in the spec:
      // three separate user sends, each with its own assistant response,
      // all processed in reverse order (assistant arrives before echo).
      // After ALL events are replayed, each user turn must appear BEFORE
      // (not after) its corresponding assistant response.

      // ─── Send 1 (live, with pending turn) ────────────────────────────────
      store.addPendingMessage('uuid-s1', 'send one')
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'response one' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'assistant',
        message: {
          id: 'msg-s1',
          role: 'assistant',
          content: [{ type: 'text', text: 'response one' }],
          model: 'claude-test',
        },
      })
      // Echo: dedup path → positions user before finalized assistant
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-s1',
        isReplay: true,
        message: { role: 'user', content: 'send one' },
      })

      expect(store.conversation.turns).toHaveLength(2)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[1].type).toBe('assistant')

      // ─── Send 2 (live, with pending turn) ────────────────────────────────
      store.addPendingMessage('uuid-s2', 'send two')
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'response two' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'assistant',
        message: {
          id: 'msg-s2',
          role: 'assistant',
          content: [{ type: 'text', text: 'response two' }],
          model: 'claude-test',
        },
      })
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-s2',
        isReplay: true,
        message: { role: 'user', content: 'send two' },
      })

      expect(store.conversation.turns).toHaveLength(4)
      expect(store.conversation.turns[2].type).toBe('user')
      expect(store.conversation.turns[3].type).toBe('assistant')

      // ─── Send 3 (live, with pending turn) ────────────────────────────────
      store.addPendingMessage('uuid-s3', 'send three')
      store.applyEventForTest({
        type: 'stream_event',
        stream_event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'response three' }, index: 0 },
      })
      store.applyEventForTest({
        type: 'assistant',
        message: {
          id: 'msg-s3',
          role: 'assistant',
          content: [{ type: 'text', text: 'response three' }],
          model: 'claude-test',
        },
      })
      store.applyEventForTest({
        type: 'user',
        uuid: 'uuid-s3',
        isReplay: true,
        message: { role: 'user', content: 'send three' },
      })

      // Final state: [user1, asst1, user2, asst2, user3, asst3]
      expect(store.conversation.turns).toHaveLength(6)
      expect(store.conversation.turns[0].type).toBe('user')
      expect(store.conversation.turns[1].type).toBe('assistant')
      expect(store.conversation.turns[2].type).toBe('user')
      expect(store.conversation.turns[3].type).toBe('assistant')
      expect(store.conversation.turns[4].type).toBe('user')
      expect(store.conversation.turns[5].type).toBe('assistant')

      // Verify text content
      const s1Text = store.conversation.turns[0].blocks.find(b => b.type === 'text')
      expect(s1Text?.type === 'text' && s1Text.text).toBe('send one')
      const s2Text = store.conversation.turns[2].blocks.find(b => b.type === 'text')
      expect(s2Text?.type === 'text' && s2Text.text).toBe('send two')
      const s3Text = store.conversation.turns[4].blocks.find(b => b.type === 'text')
      expect(s3Text?.type === 'text' && s3Text.text).toBe('send three')
    })
  })

})
