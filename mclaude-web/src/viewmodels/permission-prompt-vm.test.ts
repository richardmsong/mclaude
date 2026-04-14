import { describe, it, expect, beforeEach, vi } from 'vitest'
import { PermissionPromptVM } from './permission-prompt-vm'
import { ConversationVM } from './conversation-vm'
import { EventStore } from '../stores/event-store'
import { SessionStore } from '../stores/session-store'
import { MockNATSClient } from '../testutil/mock-nats'
import { makeSessionKVState, transcripts } from '../testutil/fixtures'

describe('PermissionPromptVM', () => {
  let mockNats: MockNATSClient
  let eventStore: EventStore
  let conversationVM: ConversationVM
  let vm: PermissionPromptVM

  beforeEach(() => {
    mockNats = new MockNATSClient()
    mockNats.kvSet('mclaude-sessions', 'user-1/project-1/session-1', makeSessionKVState())

    const sessionStore = new SessionStore(mockNats, 'user-1')
    sessionStore.startWatching()

    eventStore = new EventStore({
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

    vm = new PermissionPromptVM(conversationVM)
  })

  describe('no pending permissions', () => {
    it('returns null when no control_request events', () => {
      expect(vm.pending).toBeNull()
    })

    it('allPending returns empty array', () => {
      expect(vm.allPending).toHaveLength(0)
    })
  })

  describe('single pending permission', () => {
    beforeEach(() => {
      for (const event of transcripts.permissionRequest) {
        eventStore.applyEventForTest(event)
      }
    })

    it('vm.pending returns the request', () => {
      const pending = vm.pending
      expect(pending).not.toBeNull()
      expect(pending?.requestId).toBe('req-1')
      expect(pending?.toolName).toBe('Bash')
    })

    it('allPending has one entry', () => {
      expect(vm.allPending).toHaveLength(1)
      expect(vm.allPending[0].requestId).toBe('req-1')
    })
  })

  describe('parallelPermissions transcript', () => {
    beforeEach(() => {
      for (const event of transcripts.parallelPermissions) {
        eventStore.applyEventForTest(event)
      }
    })

    it('allPending returns both pending requests', () => {
      expect(vm.allPending).toHaveLength(2)
      const ids = vm.allPending.map(p => p.requestId)
      expect(ids).toContain('req-1')
      expect(ids).toContain('req-2')
    })
  })

  describe('after approvePermission', () => {
    beforeEach(() => {
      for (const event of transcripts.parallelPermissions) {
        eventStore.applyEventForTest(event)
      }
    })

    it('approved request is no longer in allPending', () => {
      expect(vm.allPending).toHaveLength(2)
      conversationVM.approvePermission('req-1')
      const remaining = vm.allPending
      expect(remaining).toHaveLength(1)
      expect(remaining[0].requestId).toBe('req-2')
    })
  })

  describe('approve() action', () => {
    it('calls conversationVM.approvePermission with the first pending request requestId', () => {
      for (const event of transcripts.permissionRequest) {
        eventStore.applyEventForTest(event)
      }
      mockNats.clearRecorded()
      vm.approve()
      // Should have published a control_response
      expect(mockNats.published).toHaveLength(1)
      const payload = JSON.parse(new TextDecoder().decode(mockNats.published[0].data)) as {
        type: string
        response: { request_id: string; response: { behavior: string } }
      }
      expect(payload.type).toBe('control_response')
      expect(payload.response.request_id).toBe('req-1')
      expect(payload.response.response.behavior).toBe('allow')
    })

    it('approve() is a no-op when no pending', () => {
      mockNats.clearRecorded()
      vm.approve() // should not throw or publish
      expect(mockNats.published).toHaveLength(0)
    })
  })

  describe('deny() action', () => {
    it('calls conversationVM.denyPermission with the first pending request requestId', () => {
      for (const event of transcripts.permissionRequest) {
        eventStore.applyEventForTest(event)
      }
      mockNats.clearRecorded()
      vm.deny()
      expect(mockNats.published).toHaveLength(1)
      const payload = JSON.parse(new TextDecoder().decode(mockNats.published[0].data)) as {
        type: string
        response: { request_id: string; response: { behavior: string } }
      }
      expect(payload.type).toBe('control_response')
      expect(payload.response.request_id).toBe('req-1')
      expect(payload.response.response.behavior).toBe('deny')
    })
  })

  describe('R2: desktop notification', () => {
    it('requests notification permission when first permission arrives (permission=default)', () => {
      // Mock Notification API — node test env has no document so we only check
      // that requestPermission is called; visibilityState mock is skipped in node.
      const requestPermissionMock = vi.fn().mockResolvedValue('denied')
      const originalNotification = (global as Record<string, unknown>)['Notification']
      ;(global as Record<string, unknown>)['Notification'] = {
        permission: 'default' as NotificationPermission,
        requestPermission: requestPermissionMock,
      }
      // In node env document.visibilityState is unavailable; the guard
      // in PermissionPromptVM uses optional chaining so it won't crash.

      for (const event of transcripts.permissionRequest) {
        eventStore.applyEventForTest(event)
      }

      // requestPermission is called when Notification.permission === 'default'
      expect(requestPermissionMock).toHaveBeenCalled()

      // Restore
      ;(global as Record<string, unknown>)['Notification'] = originalNotification
    })
  })

  describe('onPendingChanged listener', () => {
    it('fires when a control_request event arrives', () => {
      let callCount = 0
      vm.onPendingChanged(() => { callCount++ })
      for (const event of transcripts.permissionRequest) {
        eventStore.applyEventForTest(event)
      }
      expect(callCount).toBeGreaterThan(0)
    })

    it('fires with the pending request when one arrives', () => {
      const pendings: Array<unknown> = []
      vm.onPendingChanged((p) => pendings.push(p))
      for (const event of transcripts.permissionRequest) {
        eventStore.applyEventForTest(event)
      }
      // Last notification should have a pending item
      const last = pendings[pendings.length - 1] as { requestId: string } | null
      expect(last).not.toBeNull()
      expect(last?.requestId).toBe('req-1')
    })

    it('fires with null after permission is approved', () => {
      for (const event of transcripts.permissionRequest) {
        eventStore.applyEventForTest(event)
      }
      const pendings: Array<unknown> = []
      vm.onPendingChanged((p) => pendings.push(p))
      vm.approve()
      const last = pendings[pendings.length - 1]
      expect(last).toBeNull()
    })

    it('unsubscribe stops listener', () => {
      let callCount = 0
      const unsub = vm.onPendingChanged(() => { callCount++ })
      for (const event of transcripts.permissionRequest) {
        eventStore.applyEventForTest(event)
      }
      const countAfterSub = callCount
      unsub()
      vm.approve()
      expect(callCount).toBe(countAfterSub) // no new calls after unsubscribe
    })
  })
})
