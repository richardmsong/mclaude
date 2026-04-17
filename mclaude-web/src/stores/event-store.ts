import type {
  INATSClient,
  NATSMessage,
  StreamJsonEvent,
  ConversationModel,
  Turn,
  Block,
  StreamingTextBlock,
  ToolUseBlock,
  ToolResultBlock,
  TextBlock,
  ThinkingBlock,
  ControlRequestBlock,
  SystemMessageBlock,
  SkillInvocationBlock,
  UserImageBlock,
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

  addPendingMessage(uuid: string, content: string | Array<{ type: string; text?: string; source?: { type: string; media_type: string; data: string } }>): void {
    const pending: PendingMessage = { uuid, content, sentAt: Date.now() }
    this._pendingMessages.push(pending)

    // Immediately add an optimistic user turn so the message appears in the
    // correct position (before any subsequent assistant turns).
    const blocks: Block[] = []
    if (typeof content === 'string') {
      if (content) blocks.push({ type: 'text', text: content })
    } else {
      for (const c of content) {
        if (c.type === 'text' && c.text) {
          blocks.push({ type: 'text', text: c.text })
        } else if (c.type === 'image' && c.source?.type === 'base64') {
          const imgBlock: UserImageBlock = {
            type: 'user_image',
            dataUrl: `data:${c.source.media_type};base64,${c.source.data}`,
            mimeType: c.source.media_type,
          }
          blocks.push(imgBlock)
        }
      }
    }

    // Only create the turn if there's something to show
    if (blocks.length > 0) {
      const turn: Turn = {
        id: this._nextTurnId(),
        type: 'user',
        blocks,
        // Tag with uuid so the server-echo dedup can find and replace it
        pendingUuid: uuid,
      }
      this._conversation.turns.push(turn)
    }

    this._notify()
  }

  get pendingMessages(): PendingMessage[] {
    return this._pendingMessages
  }

  private _nextTurnId(): string {
    return `turn-${++this._turnCounter}`
  }

  /**
   * Return the most recent assistant turn whose parentToolUseId matches.
   *
   * If `messageId` is provided, only returns the turn when its own messageId
   * matches (same Anthropic message) OR the turn has no messageId yet (it was
   * created by a stream_event and has not yet been claimed by an assistant event).
   * A turn whose messageId is set to a DIFFERENT value is a distinct exchange
   * boundary — return null so the caller creates a fresh turn.
   */
  private _currentAssistantTurn(parentToolUseId?: string, messageId?: string): Turn | null {
    for (let i = this._conversation.turns.length - 1; i >= 0; i--) {
      const t = this._conversation.turns[i]
      if (t.type === 'assistant' && t.parentToolUseId === parentToolUseId) {
        if (messageId !== undefined) {
          // If this turn already belongs to a different message, it is a
          // finalized boundary — treat as no match so a new turn is created.
          if (t.messageId !== undefined && t.messageId !== messageId) return null
        }
        return t
      }
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

  /**
   * Insert a user turn at the correct position in _conversation.turns.
   *
   * With --replay-user-messages, Claude publishes stream_events (and in some
   * protocol orderings even the full assistant event) BEFORE the user echo in the
   * JetStream sequence. So when the user echo arrives (live or replay), the
   * associated assistant response turn may already be at or near the END of turns[].
   *
   * We insert the user turn BEFORE the last matching assistant turn under
   * wantedParent, UNLESS that assistant turn is already "claimed" — i.e. a
   * confirmed (non-pending) user turn immediately precedes it, meaning it was
   * already correctly paired with an earlier user message.
   *
   * "Matching" means the turn's parentToolUseId equals wantedParent (both may be
   * undefined for top-level turns).
   *
   * If no eligible assistant turn is found, append to the end (correct for
   * idle-state sends where no response has started yet).
   */
  private _insertUserTurn(turn: Turn, wantedParent: string | undefined): void {
    // Goal: insert a confirmed user turn BEFORE the most-recent "unclaimed"
    // assistant turn under wantedParent, keeping any remaining pending user
    // turns at the very end of the conversation (below all active content).
    //
    // Algorithm:
    //  1. Find the last assistant turn under wantedParent.
    //  2. Check whether that turn is "claimed": scan backward from its index,
    //     skipping system turns, to find the nearest non-system predecessor.
    //     If that predecessor is a CONFIRMED (non-pending) user turn, the
    //     assistant turn is already paired — fall through to append.
    //  3. Otherwise (predecessor is an assistant turn, a pending user turn,
    //     or nothing), the turn is unclaimed — eligible for insertion before.
    //  4. Collect all pending user turns from the array (they may be anywhere
    //     due to batched sends).
    //  5. Remove the unclaimed asst turn from the array.
    //  6. Push the confirmed turn, then re-push the asst turn.
    //  7. Re-append all collected pending turns at the very end.

    // Step 1: find the last assistant turn under wantedParent.
    let asstIdx = -1
    for (let i = this._conversation.turns.length - 1; i >= 0; i--) {
      const t = this._conversation.turns[i]
      if (t.type === 'assistant' && t.parentToolUseId === wantedParent) {
        asstIdx = i
        break
      }
    }

    if (asstIdx === -1) {
      // No assistant turn at all — just append.
      this._conversation.turns.push(turn)
      return
    }

    // If a confirmed user turn exists ANYWHERE after asstIdx, this assistant turn
    // is already paired with that later user message — just append.
    const hasConfirmedUserAfter = this._conversation.turns
      .slice(asstIdx + 1)
      .some(t => t.type === 'user' && t.pendingUuid === undefined)
    if (hasConfirmedUserAfter) {
      this._conversation.turns.push(turn)
      return
    }

    // Step 2: check whether the found assistant turn is already "claimed" by
    // a confirmed user turn immediately preceding it (skipping system turns).
    let predecessorIdx = asstIdx - 1
    while (predecessorIdx >= 0 && this._conversation.turns[predecessorIdx].type === 'system') {
      predecessorIdx--
    }
    const predecessor = predecessorIdx >= 0 ? this._conversation.turns[predecessorIdx] : null
    const isClaimedByConfirmedUser =
      predecessor !== null &&
      predecessor.type === 'user' &&
      predecessor.pendingUuid === undefined // confirmed (not pending)

    if (isClaimedByConfirmedUser) {
      // The assistant turn is already paired with a confirmed user turn — just append.
      this._conversation.turns.push(turn)
      return
    }

    // Steps 3-7: insert before the unclaimed assistant turn.
    const asstTurn = this._conversation.turns[asstIdx]

    // Step 4: collect all pending user turns from the array.
    // We scan the entire array because pending turns can appear before or
    // after the assistant turn in a batched-send scenario.
    const pendingTurns: Turn[] = []
    for (let i = this._conversation.turns.length - 1; i >= 0; i--) {
      const t = this._conversation.turns[i]
      if (t.type === 'user' && t.pendingUuid !== undefined) {
        pendingTurns.unshift(this._conversation.turns.splice(i, 1)[0])
        // Adjust asstIdx if we removed an element before it.
        if (i < asstIdx) {
          asstIdx--
        }
      }
    }

    // Step 5: remove the assistant turn.
    this._conversation.turns.splice(asstIdx, 1)

    // Step 6: push confirmed turn then assistant turn.
    this._conversation.turns.push(turn)
    this._conversation.turns.push(asstTurn)

    // Step 7: re-append pending turns at the very end.
    for (const p of pendingTurns) {
      this._conversation.turns.push(p)
    }
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
          const wantedParent = event.parent_tool_use_id ?? undefined
          let current = this._currentAssistantTurn(wantedParent)

          // Only reuse a turn that has an active (not-yet-finalized) streaming block.
          // If the existing turn is finalized (all streaming_text blocks complete),
          // treat it as a new response and push a fresh assistant turn at the end.
          // This prevents new responses from being appended into a prior session's
          // last assistant turn when the user sends a message from an existing session.
          if (current) {
            const hasActiveStreaming = current.blocks.some(
              b => b.type === 'streaming_text' && !(b as StreamingTextBlock).complete,
            )
            if (!hasActiveStreaming) current = null
          }

          if (!current) {
            current = {
              id: this._nextTurnId(),
              type: 'assistant',
              blocks: [],
              parentToolUseId: wantedParent,
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
        const wantedParentA = event.parent_tool_use_id ?? undefined
        const incomingMessageId = event.message.id
        // Pass incomingMessageId so _currentAssistantTurn returns null when the
        // most recent assistant turn belongs to a DIFFERENT Anthropic message —
        // i.e., it has already been finalized and stamped with a different
        // message ID. This prevents new responses from being merged into the
        // prior exchange's turn.
        let turn = this._currentAssistantTurn(wantedParentA, incomingMessageId)
        if (!turn) {
          turn = {
            id: this._nextTurnId(),
            type: 'assistant',
            blocks: [],
            parentToolUseId: wantedParentA,
          }
          this._conversation.turns.push(turn)
        }

        // Stamp the turn with this message's ID so future calls can detect a
        // turn boundary (different messageId → different exchange).
        turn.messageId = incomingMessageId

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

        // Step 2: Dedup the optimistic turn that addPendingMessage already inserted.
        // Primary: match by uuid (present on --replay-user-messages echoes).
        // Fallback: match by text content (normal Claude Code echoes omit uuid).
        {
          const incomingText = typeof event.message.content === 'string'
            ? event.message.content
            : Array.isArray(event.message.content)
              ? event.message.content.filter(c => c.type === 'text' && c.text).map(c => c.text ?? '').join('')
              : ''

          // Find a matching pending message: match by uuid when present, fall back to
          // text content only when uuid is absent (normal Claude echoes omit uuid).
          let pendingIdx = event.uuid
            ? this._pendingMessages.findIndex(p => p.uuid === event.uuid)
            : -1
          if (pendingIdx === -1 && !event.uuid && incomingText) {
            pendingIdx = this._pendingMessages.findIndex(p => {
              const pendingText = typeof p.content === 'string'
                ? p.content
                : p.content.filter(c => c.type === 'text').map(c => c.text ?? '').join('')
              return pendingText === incomingText
            })
          }

          if (pendingIdx !== -1) {
            const matched = this._pendingMessages[pendingIdx]
            this._pendingMessages.splice(pendingIdx, 1)
            // Find the optimistic turn in turns[] and reposition it to the correct
            // inline position. The optimistic turn was pushed to the end by
            // addPendingMessage, but Claude may have started streaming a response
            // before we received the echo — so the optimistic turn may now be
            // sitting AFTER the streaming assistant turn instead of BEFORE it.
            const optimisticIdx = this._conversation.turns.findIndex(
              t => t.type === 'user' && t.pendingUuid === matched.uuid,
            )
            if (optimisticIdx !== -1) {
              // Splice the optimistic turn out of its current position.
              const [optimisticTurn] = this._conversation.turns.splice(optimisticIdx, 1)
              // Clear the pending flag.
              delete optimisticTurn.pendingUuid
              // Reinsert at the correct position (before any fresh streaming
              // assistant turn under the same parentToolUseId).
              this._insertUserTurn(optimisticTurn, event.parent_tool_use_id ?? undefined)
              break
            }
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

        // Step 4: Inspect raw text content for special prefixes before creating a turn
        const rawText = typeof event.message.content === 'string'
          ? event.message.content
          : Array.isArray(event.message.content)
            ? event.message.content
                .filter(c => c.type === 'text' && c.text)
                .map(c => c.text ?? '')
                .join('')
            : ''

        // Step 4a: System notifications → discard entirely
        if (rawText.startsWith('[SYSTEM NOTIFICATION')) {
          break
        }

        // Step 4b: Skill invocation expansion → SkillInvocationBlock
        if (rawText.startsWith('Base directory for this skill:')) {
          const lines = rawText.split('\n')
          // Extract skill name from the path segment after the last /skills/
          const firstLine = lines[0]
          const skillsIdx = firstLine.lastIndexOf('/skills/')
          const skillName = skillsIdx !== -1
            ? firstLine.slice(skillsIdx + '/skills/'.length).trim()
            : firstLine.replace('Base directory for this skill:', '').trim()

          // Extract args: everything after the line containing "ARGUMENTS:"
          let args = ''
          const argsLineIdx = lines.findIndex(l => l.includes('ARGUMENTS:'))
          if (argsLineIdx !== -1) {
            args = lines.slice(argsLineIdx + 1).join('\n').trim()
          }

          const block: SkillInvocationBlock = {
            type: 'skill_invocation',
            skillName,
            args,
            rawContent: rawText,
          }
          const skillTurn: Turn = {
            id: this._nextTurnId(),
            type: 'user',
            blocks: [block],
            parentToolUseId: event.parent_tool_use_id ?? undefined,
          }
          this._conversation.turns.push(skillTurn)
          break
        }

        // Step 4c: Otherwise → create a normal user turn.
        // Use _insertUserTurn which handles the ordering fix: with
        // --replay-user-messages, Claude publishes stream_events BEFORE the user
        // echo in the JetStream sequence. So when we receive the user echo during
        // replay (or live, if streaming started before the echo arrived), the
        // associated response turn is already at the END of turns[].
        //
        // _insertUserTurn inserts before a fresh streaming-only assistant turn if
        // one exists under the same parentToolUseId, otherwise appends to the end.
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
            const raw = c as { type: string; text?: string; source?: { type: string; media_type: string; data: string } }
            if (raw.type === 'text' && raw.text) {
              turn.blocks.push({ type: 'text', text: raw.text })
            } else if (raw.type === 'image') {
              const src = raw.source
              if (src?.type === 'base64') {
                const imgBlock: UserImageBlock = {
                  type: 'user_image',
                  dataUrl: `data:${src.media_type};base64,${src.data}`,
                  mimeType: src.media_type,
                }
                turn.blocks.push(imgBlock)
              }
            }
          }
        }

        if (turn.blocks.length > 0) {
          this._insertUserTurn(turn, event.parent_tool_use_id ?? undefined)
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
