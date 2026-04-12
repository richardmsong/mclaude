import type {
  INATSClient,
  StreamJsonEvent,
  ConversationModel,
  Turn,
  StreamingTextBlock,
  ToolUseBlock,
  ToolResultBlock,
  TextBlock,
  ThinkingBlock,
  ControlRequestBlock,
  SessionState,
  SystemInitEvent,
  SystemStateChangedEvent,
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
  private _listeners: EventStoreListener[] = []
  private _unsubscribe: (() => void) | null = null
  private _sessionState: SessionState = 'idle'
  private _capabilities = { skills: [] as string[], tools: [] as string[], agents: [] as string[] }
  private _model = ''
  private _turnCounter = 0

  constructor(private readonly opts: EventStoreOptions) {}

  get conversation(): ConversationModel {
    return this._conversation
  }

  get lastSequence(): number {
    return this._lastSequence
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

    logger.debug(
      {
        component: 'event-store',
        sessionId: this.opts.sessionId,
        userId: this.opts.userId,
        projectId: this.opts.projectId,
        subject,
      },
      'subscribing to event stream',
    )

    this._unsubscribe = this.opts.natsClient.subscribe(subject, (msg) => {
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
    })

    // Signal to the NATS client the desired start sequence (clients that support it)
    void startSeq
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
        const turn: Turn = {
          id: this._nextTurnId(),
          type: 'user',
          blocks: [],
          parentToolUseId: event.parent_tool_use_id ?? undefined,
        }

        if (typeof event.message.content === 'string') {
          turn.blocks.push({ type: 'text', text: event.message.content })
        } else if (Array.isArray(event.message.content)) {
          for (const c of event.message.content) {
            if (c.type === 'text' && c.text) {
              turn.blocks.push({ type: 'text', text: c.text })
            } else if (c.type === 'tool_result' && c.tool_use_id) {
              // Attach tool result to matching tool use block
              const toolUseBlock = this._findToolUseBlock(c.tool_use_id)
              if (toolUseBlock) {
                const resultBlock: ToolResultBlock = {
                  type: 'tool_result',
                  toolUseId: c.tool_use_id,
                  content: typeof c.content === 'string' ? c.content : JSON.stringify(c.content),
                  isError: c.is_error ?? false,
                }
                toolUseBlock.result = resultBlock
              }
              return // Don't add user turn for tool_result
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
