import { describe, it, expect, beforeEach } from 'vitest'
import { SkillsPickerVM } from './skills-picker-vm'
import { ConversationVM } from './conversation-vm'
import { EventStore } from '../stores/event-store'
import { SessionStore } from '../stores/session-store'
import { MockNATSClient } from '../testutil/mock-nats'
import { makeSessionKVState } from '../testutil/fixtures'

describe('SkillsPickerVM', () => {
  let mockNats: MockNATSClient
  let sessionStore: SessionStore
  let conversationVM: ConversationVM
  let vm: SkillsPickerVM

  beforeEach(() => {
    mockNats = new MockNATSClient()
    // ADR-0054: per-user bucket, new key format
    mockNats.kvSet('mclaude-sessions-user-1', 'hosts.local.projects.project-1.sessions.session-1', makeSessionKVState({
      id: 'session-1',
      capabilities: { skills: ['commit', 'review-pr', 'deploy'], tools: ['Bash'], agents: [] },
    }))

    sessionStore = new SessionStore(mockNats, 'user-1')
    sessionStore.startWatching()

    const eventStore = new EventStore({
      natsClient: mockNats,
      userId: 'user-1',
      projectId: 'project-1',
      sessionId: 'session-1',
    })

    conversationVM = new ConversationVM(
      eventStore,
      sessionStore,
      mockNats,
      'user-1',
      'project-1',
      'session-1',
    )

    vm = new SkillsPickerVM(sessionStore, conversationVM, 'session-1')
  })

  describe('skills getter', () => {
    it('returns skills from session capabilities', () => {
      const skills = vm.skills
      expect(skills).toContain('commit')
      expect(skills).toContain('review-pr')
      expect(skills).toContain('deploy')
      expect(skills).toHaveLength(3)
    })

    it('returns empty array when session not found', () => {
      const vmNoSession = new SkillsPickerVM(sessionStore, conversationVM, 'nonexistent-session')
      expect(vmNoSession.skills).toEqual([])
    })

    it('returns empty array when capabilities has no skills', () => {
      mockNats.kvSet('mclaude-sessions-user-1', 'hosts.local.projects.project-1.sessions.session-2', makeSessionKVState({
        id: 'session-2',
        capabilities: { skills: [], tools: [], agents: [] },
      }))
      const vm2 = new SkillsPickerVM(sessionStore, conversationVM, 'session-2')
      expect(vm2.skills).toEqual([])
    })
  })

  describe('invoke', () => {
    it('sends skill invocation message via conversationVM', () => {
      mockNats.clearRecorded()
      vm.invoke('commit', '-m "Fix bug"')
      expect(mockNats.published).toHaveLength(1)
      const payload = JSON.parse(new TextDecoder().decode(mockNats.published[0]!.data)) as {
        type: string
        message: { content: string }
      }
      expect(payload.type).toBe('user')
      expect(payload.message.content).toBe('/commit -m "Fix bug"')
    })

    it('sends skill name without args when args not provided', () => {
      mockNats.clearRecorded()
      vm.invoke('review-pr')
      expect(mockNats.published).toHaveLength(1)
      const payload = JSON.parse(new TextDecoder().decode(mockNats.published[0]!.data)) as {
        type: string
        message: { content: string }
      }
      expect(payload.message.content).toBe('/review-pr')
    })
  })

  describe('refresh', () => {
    it('sends reload_plugins control request via conversationVM', () => {
      mockNats.clearRecorded()
      vm.refresh()
      expect(mockNats.published).toHaveLength(1)
      const payload = JSON.parse(new TextDecoder().decode(mockNats.published[0]!.data)) as {
        type: string
        request: { subtype: string }
      }
      expect(payload.type).toBe('control_request')
      expect(payload.request.subtype).toBe('reload_plugins')
    })
  })
})
