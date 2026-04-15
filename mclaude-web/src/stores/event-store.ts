import type {
  INATSClient,
  NATSMessage,
  StreamJsonEvent,
  ConversationModel,
  Turn,
  StreamingTextBlock,
  ToolUseBlock,
  ToolResultBlock,
  TextBlock,
  ThinkingBlock,
  ControlRequestBlock,
  SystemMessageBlock,
  SessionState,
  SystemInitEvent,
  SystemStateChangedEvent,
  PendingMessage,
} from '@/types'
import { logger } from '@/logger'

export interface EventStoreOptions {
  natsClient: INATSClient
  userId: string
  projectId: string
  sessionId: string
}

export type EventStoreListener = (model: ConversationModel) => void

export class EventStore {
  private _conversation: ConversationModel = { turns: [] }
  private _lastSequence = 0
  private _replayFromSeq = 0
  private _listeners: EventStoreListener[] = []
  private _unsubscribe: (() => void) | null = null
  private _sessionState: SessionState = 'idle'
  private _capabilities = { skills: [] as string[], tools: [] as string[], agents: [] as string[] }
  private _model = ''
  private _turnCounter = 0
  private _pendingMessages: PendingMessage[] = []

  constructor(private readonly opts: EventStoreOptions) {}

  get conversation(): ConversationModel {
    return this._conversation
  }

  get lastSequence(): number {
    return this._lastSequence
  }

  get replayFromSeq(): number {
    return this._replayFromSeq
  }

  get sessionState(): SessionState {
    return this._sessionState
  }

  get model(): string {
    return this._model
  }

  get capabilities() {
    return this._capabilities
  }

  start(replayFromSeq?: number): void {
    const subject = `mclaude.${this.opts.userId}.${this.opts.projectId}.events.${this.opts.sessionId}`
    const startSeq = replayFromSeq ?? 0
    this._replayFromSeq = startSeq

    logger.debug(
      {
        component: 'event-store',
        sessionId: this.opts.sessionId,
        userId: this.opts.userId,
        projectId: this.opts.projectId,
        subject,
        startSeq,
      },
      'subscribing to event stream via JetStream ordered consumer',
    )

    const onMsg = (msg: NATSMessage) => {
      // Deduplication: skip events at or before lastSequence
      if (msg.seq !== undefined && msg.seq <= this._lastSequence) return

      if (msg.seq !== undefined) {
        this._lastSequence = msg.seq
      }

      try {
        const event = JSON.parse(new TextDecoder().decode(msg.data)) as StreamJsonEvent
        this._applyEvent(event)
        this._notify()
      } catch (err) {
        logger.warn(
          {
            component: 'event-store',
            sessionId: this.opts.sessionId,
            userId: this.opts.userId,
            projectId: this.opts.projectId,
            err: err instanceof Error ? err.message : String(err),
          },
          'malformed event: failed to parse',
        )
      }
    }

    // Use JetStream ordered consumer for replay and deduplication.
    // jsSubscribe is async; we wrap the callback with a stopped guard so that
    // stop() immediately ceases event processing even before the consumer resolves.
    let stopped = false
    const guardedOnMsg = (msg: NATSMessage) => {
      if (stopped) return
      onMsg(msg)
    }

    let innerUnsub: (() => void) | null = null
    this.opts.natsClient.jsSubscribe('MCLAUDE_EVENTS', subject, startSeq, guardedOnMsg).then((unsub) => {
      if (stopped) {
        // stop() was called before the consumer was ready
        unsub()
      } else {
        innerUnsub = unsub
      }
    }).catch((err) => {
      logger.warn(
        {
          component: 'event-store',
          sessionId: this.opts.sessionId,
          userId: this.opts.userId,
          projectId: this.opts.projectId,
          err: err instanceof Error ? err.message : String(err),
        },
        'failed to create JetStream ordered consumer; events will not flow',
      )
    })

    this._unsubscribe = () => {
      stopped = true
      if (innerUnsub) innerUnsub()
    }
  }

  stop(): void {
    if (this._unsubscribe) {
      this._unsubscribe()
      this._unsubscribe = null
    }
  }

  onConversationChanged(listener: EventStoreListener): () => void {
    this._listeners.push(listener)
    return () => {
      this._listeners = this._listeners.filter(l => l !== listener)
    }
  }

  // Exposed for testing
  applyEventForTest(event: StreamJsonEvent, seq?: number): void {
    if (seq !== undefined) {
      if (seq <= this._lastSequence) return
      this._lastSequence = seq
    }
    this._applyEvent(event)
    this._notify()
  }

  addPendingMessage(uuid: string, content: string | Array<{ type: string; text?: string }>): void {
    const pending: PendingMessage = { uuid, content, sentAt: Date.now() }
    this._pendingMessages.push(pending)
    this._notify()
  }

  get pendingMessages(): PendingMessage[] {
    return this._pendingMessages
  }

  private _nextTurnId(): string {
    return `turn-${++this._turnCounter}`
  }

  private _currentAssistantTurn(): Turn | null {
    for (let i = this._conversation.turns.length - 1; i >= 0; i--) {
      const t = this._conversation.turns[i]
      if (t.type === 'assistant') return t
    }
    return null
  }

  private _findToolUseBlock(toolUseId: string): ToolUseBlock | null {
    for (const turn of this._conversation.turns) {
      for (const block of turn.blocks) {
        if (block.type === 'tool_use' && block.id === toolUseId) {
          return block
        }
      }
    }
    return null
  }

  private _applyEvent(event: StreamJsonEvent): void {
    switch (event.type) {
      case 'clear': {
        logger.debug(
          {
            component: 'event-store',
            sessionId: this.opts.sessionId,
            userId: this.opts.userId,
            projectId: this.opts.projectId,
            eventType: 'clear',
          },
          'processing clear event',
        )
        this._conversation = { turns: [] }
        this._pendingMessages = []
        // Update replayFromSeq so reconnects skip events before this clear
        if (this._lastSequence > 0) {
          this._replayFromSeq = this._lastSequence
        }
        break
      }

      case 'compact_boundary': {
        logger.debug(
          {
            component: 'event-store',
            sessionId: this.opts.sessionId,
            userId: this.opts.userId,
            projectId: this.opts.projectId,
            eventType: 'compact_boundary',
          },
          'processing compact_boundary event',
        )
        this._conversation = {
          turns: [{
            id: this._nextTurnId(),
            type: 'system',
            blocks: [{ type: 'compaction', summary: event.summary }],
          }],
        }
        this._pendingMessages = []
        // Update replayFromSeq — events before compaction are no longer relevant
        if (this._lastSequence > 0) {
          this._replayFromSeq = this._lastSequence
        }
        break
      }

      case 'system': {
        if (event.subtype === 'init') {
          const e = event as SystemInitEvent
          logger.debug(
            {
              component: 'event-store',
              sessionId: this.opts.sessionId,
              userId: this.opts.userId,
              projectId: this.opts.projectId,
              eventType: 'system.init',
              model: e.model,
            },
            'processing system.init event',
          )
          this._model = e.model
          this._capabilities = e.capabilities
        } else if (event.subtype === 'session_state_changed') {
          const e = event as SystemStateChangedEvent
          this._sessionState = e.state
        }
        break
      }

      case 'stream_event': {
        if (event.stream_event.delta?.type === 'text_delta') {
          const text = event.stream_event.delta.text ?? ''
          let current = this._currentAssistantTurn()
          if (!current) {
            current = {
              id: this._nextTurnId(),
              type: 'assistant',
              blocks: [],
              parentToolUseId: event.parent_tool_use_id ?? undefined,
            }
            this._conversation.turns.push(current)
          }
          const last = current.blocks[current.blocks.length - 1]
          if (last && last.type === 'streaming_text') {
            last.chunks.push(text)
          } else {
            const block: StreamingTextBlock = { type: 'streaming_text', chunks: [text], complete: false }
            current.blocks.push(block)
          }
        }
        break
      }

      case 'assistant': {
        // Finalize any streaming text block
        let turn = this._currentAssistantTurn()
        if (!turn) {
          turn = {
            id: this._nextTurnId(),
            type: 'assistant',
            blocks: [],
            parentToolUseId: event.parent_tool_use_id ?? undefined,
          }
          this._conversation.turns.push(turn)
        }

        // Finalize streaming block if present
        for (const block of turn.blocks) {
          if (block.type === 'streaming_text') {
            block.complete = true
          }
        }

        // Add content blocks from the assistant message
        for (const contentBlock of event.message.content) {
          if (contentBlock.type === 'text' && contentBlock.text) {
            // Replace or supplement streaming block
            const hasStreaming = turn.blocks.some(b => b.type === 'streaming_text')
            if (!hasStreaming) {
              const block: TextBlock = { type: 'text', text: contentBlock.text }
              turn.blocks.push(block)
            }
          } else if (contentBlock.type === 'thinking' && contentBlock.text) {
            const block: ThinkingBlock = { type: 'thinking', text: contentBlock.text }
            turn.blocks.push(block)
          } else if (contentBlock.type === 'tool_use' && contentBlock.id && contentBlock.name) {
            const block: ToolUseBlock = {
              type: 'tool_use',
              id: contentBlock.id,
              name: contentBlock.name,
              inputSummary: JSON.stringify(contentBlock.input).slice(0, 100),
              fullInput: contentBlock.input,
            }
            turn.blocks.push(block)
          }
        }

        // Set model + usage on turn
        if (event.message.model) turn.model = event.message.model
        if (event.message.usage) {
          turn.usage = {
            inputTokens: event.message.usage.input_tokens,
            outputTokens: event.message.usage.output_tokens,
            cacheReadTokens: event.message.usage.cache_read_input_tokens ?? 0,
            cacheWriteTokens: event.message.usage.cache_creation_input_tokens ?? 0,
            costUsd: 0,
          }
        }
        break
      }

      case 'user': {
        // Step 1: If content is tool_result array → attach to ToolUseBlock and return early
        if (Array.isArray(event.message.content)) {
          const hasToolResult = event.message.content.some(c => c.type === 'tool_result')
          if (hasToolResult) {
            for (const c of event.message.content) {
              if (c.type === 'tool_result' && c.tool_use_id) {
                const toolUseBlock = this._findToolUseBlock(c.tool_use_id)
                const resultBlock: ToolResultBlock = {
                  type: 'tool_result',
                  toolUseId: c.tool_use_id,
                  content: typeof c.content === 'string' ? c.content : JSON.stringify(c.content),
                  isError: c.is_error ?? false,
                }
                if (toolUseBlock) {
                  toolUseBlock.result = resultBlock
                } else {
                  // Orphaned tool_result — no matching tool_use found
                  const orphanTurn: Turn = {
                    id: this._nextTurnId(),
                    type: 'user',
                    blocks: [resultBlock],
                    parentToolUseId: event.parent_tool_use_id ?? undefined,
                  }
                  this._conversation.turns.push(orphanTurn)
                }
              }
            }
            return // Don't add user turn for tool_result
          }
        }

        // Step 2: If event has uuid and matching pending message → remove from _pendingMessages
        if (event.uuid) {
          const idx = this._pendingMessages.findIndex(p => p.uuid === event.uuid)
          if (idx !== -1) {
            this._pendingMessages.splice(idx, 1)
          }
        }

        // Step 3: If isSynthetic → create system turn with SystemMessageBlock
        if (event.isSynthetic) {
          const text = typeof event.message.content === 'string'
            ? event.message.content
            : event.message.content
                .filter(c => c.type === 'text' && c.text)
                .map(c => c.text ?? '')
                .join('')
          const block: SystemMessageBlock = { type: 'system_message', text }
          const systemTurn: Turn = {
            id: this._nextTurnId(),
            type: 'system',
            blocks: [block],
            parentToolUseId: event.parent_tool_use_id ?? undefined,
          }
          this._conversation.turns.push(systemTurn)
          break
        }

        // Step 4: Otherwise → create a normal user turn inline at current position
        const turn: Turn = {
          id: this._nextTurnId(),
          type: 'user',
          blocks: [],
          parentToolUseId: event.parent_tool_use_id ?? undefined,
        }

        if (typeof event.message.content === 'string') {
          if (event.message.content) {
            turn.blocks.push({ type: 'text', text: event.message.content })
          }
        } else if (Array.isArray(event.message.content)) {
          for (const c of event.message.content) {
            if (c.type === 'text' && c.text) {
              turn.blocks.push({ type: 'text', text: c.text })
            }
          }
        }

        if (turn.blocks.length > 0) {
          this._conversation.turns.push(turn)
        }
        break
      }

      case 'control_request': {
        if (event.subtype === 'can_use_tool') {
          logger.debug(
            {
              component: 'event-store',
              sessionId: this.opts.sessionId,
              userId: this.opts.userId,
              projectId: this.opts.projectId,
              eventType: 'control_request',
              toolName: event.tool_name,
              requestId: event.request_id,
            },
            'processing control_request event',
          )
          // Add to current assistant turn or create system turn
          let turn = this._currentAssistantTurn()
          if (!turn) {
            turn = {
              id: this._nextTurnId(),
              type: 'assistant',
              blocks: [],
            }
            this._conversation.turns.push(turn)
          }
          const block: ControlRequestBlock = {
            type: 'control_request',
            requestId: event.request_id,
            toolName: event.tool_name,
            input: event.input,
            status: 'pending',
          }
          turn.blocks.push(block)
        }
        break
      }

      case 'tool_progress': {
        const toolUseBlock = this._findToolUseBlock(event.tool_use_id)
        if (toolUseBlock) {
          toolUseBlock.elapsed = event.elapsed_ms
        }
        break
      }

      case 'result':
      case 'keep_alive':
        // No UI action needed
        break
    }
  }

  private _notify(): void {
    for (const l of this._listeners) l(this._conversation)
  }
}
