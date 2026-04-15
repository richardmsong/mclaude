import type { INATSClient, ConversationModel, SessionState, PendingMessage } from '@/types'
import type { EventStore } from '@/stores/event-store'
import type { SessionStore } from '@/stores/session-store'
import { logger } from '@/logger'

export interface ConversationVMState {
  turns: ConversationModel['turns']
  pendingMessages: PendingMessage[]
  state: SessionState
  model: string
  skills: string[]
  isStreaming: boolean
}

export type ConversationVMListener = (state: ConversationVMState) => void

export class ConversationVM {
  private _listeners: ConversationVMListener[] = []
  private _unsubscribers: Array<() => void> = []

  constructor(
    private readonly eventStore: EventStore,
    private readonly sessionStore: SessionStore,
    private readonly natsClient: INATSClient,
    private readonly userId: string,
    private readonly projectId: string,
    private readonly sessionId: string,
  ) {
    const unsub = this.eventStore.onConversationChanged(() => this._notify())
    this._unsubscribers.push(unsub)
  }

  get state(): ConversationVMState {
    const conversation = this.eventStore.conversation
    const session = this.sessionStore.sessions.get(this.sessionId)
    const isStreaming = conversation.turns.some(t =>
      t.type === 'assistant' && t.blocks.some(b => b.type === 'streaming_text' && !b.complete)
    )
    return {
      turns: conversation.turns,
      pendingMessages: this.eventStore.pendingMessages,
      state: this.eventStore.sessionState,
      model: this.eventStore.model,
      skills: session?.capabilities.skills ?? [],
      isStreaming,
    }
  }

  sendMessage(text: string): void {
    const uuid = crypto.randomUUID()
    this.eventStore.addPendingMessage(uuid, text)
    const subject = `mclaude.${this.userId}.${this.projectId}.api.sessions.input`
    const payload = {
      type: 'user',
      message: { role: 'user', content: text },
      session_id: this.sessionId,
      uuid,
      parent_tool_use_id: null,
    }
    logger.info({ component: 'conversation-vm', sessionId: this.sessionId, userId: this.userId }, 'sendMessage')
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify(payload)))
  }

  sendMessageWithImage(text: string, imageBase64: string, mimeType: string): void {
    const uuid = crypto.randomUUID()
    const content = [
      { type: 'text', text },
      { type: 'image', source: { type: 'base64', media_type: mimeType, data: imageBase64 } },
    ]
    this.eventStore.addPendingMessage(uuid, content)
    const subject = `mclaude.${this.userId}.${this.projectId}.api.sessions.input`
    const payload = {
      type: 'user',
      message: {
        role: 'user',
        content,
      },
      session_id: this.sessionId,
      uuid,
      parent_tool_use_id: null,
    }
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify(payload)))
  }

  approvePermission(requestId: string): void {
    const subject = `mclaude.${this.userId}.${this.projectId}.api.sessions.control`
    const payload = {
      type: 'control_response',
      response: {
        subtype: 'success',
        request_id: requestId,
        response: { behavior: 'allow' },
      },
    }
    logger.info({ component: 'conversation-vm', sessionId: this.sessionId, requestId }, 'approvePermission')
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify(payload)))

    // Update local block status
    for (const turn of this.eventStore.conversation.turns) {
      for (const block of turn.blocks) {
        if (block.type === 'control_request' && block.requestId === requestId) {
          block.status = 'approved'
        }
      }
    }
    this._notify()
  }

  denyPermission(requestId: string): void {
    const subject = `mclaude.${this.userId}.${this.projectId}.api.sessions.control`
    const payload = {
      type: 'control_response',
      response: {
        subtype: 'success',
        request_id: requestId,
        response: { behavior: 'deny' },
      },
    }
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify(payload)))

    for (const turn of this.eventStore.conversation.turns) {
      for (const block of turn.blocks) {
        if (block.type === 'control_request' && block.requestId === requestId) {
          block.status = 'denied'
        }
      }
    }
    this._notify()
  }

  interrupt(): void {
    const subject = `mclaude.${this.userId}.${this.projectId}.api.sessions.control`
    const payload = { type: 'control_request', request: { subtype: 'interrupt' } }
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify(payload)))
  }

  switchModel(model: string): void {
    const subject = `mclaude.${this.userId}.${this.projectId}.api.sessions.control`
    const payload = { type: 'control_request', request: { subtype: 'set_model', model } }
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify(payload)))
  }

  invokeSkill(skillName: string, args?: string): void {
    const text = args ? `/${skillName} ${args}` : `/${skillName}`
    this.sendMessage(text)
  }

  setMaxThinkingTokens(budget: number): void {
    const subject = `mclaude.${this.userId}.${this.projectId}.api.sessions.control`
    const payload = { type: 'control_request', request: { subtype: 'set_max_thinking_tokens', budget } }
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify(payload)))
  }

  reloadPlugins(): void {
    const subject = `mclaude.${this.userId}.${this.projectId}.api.sessions.control`
    const payload = { type: 'control_request', request: { subtype: 'reload_plugins' } }
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify(payload)))
  }

  onStateChanged(listener: ConversationVMListener): () => void {
    this._listeners.push(listener)
    return () => { this._listeners = this._listeners.filter(l => l !== listener) }
  }

  destroy(): void {
    for (const u of this._unsubscribers) u()
    this._unsubscribers = []
  }

  private _notify(): void {
    for (const l of this._listeners) l(this.state)
  }
}
