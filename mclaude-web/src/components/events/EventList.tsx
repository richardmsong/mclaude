import type { Turn, Block, StreamingTextBlock } from '@/types'
import { UserMessage } from './UserMessage'
import { AssistantText } from './AssistantText'
import { ThinkingBlock } from './ThinkingBlock'
import { ToolCard } from './ToolCard'
import { AskUserQuestion } from './AskUserQuestion'
import { AgentGroup } from './AgentGroup'
import { SystemEvent } from './SystemEvent'

interface EventListProps {
  turns: Turn[]
  onApprove: (requestId: string) => void
  onDeny: (requestId: string) => void
}

function renderBlock(block: Block, turn: Turn, allTurns: Turn[], onApprove: (id: string) => void, onDeny: (id: string) => void): React.ReactNode {
  switch (block.type) {
    case 'text':
      return <AssistantText key={block.type + turn.id} text={block.text} />

    case 'streaming_text': {
      const sb = block as StreamingTextBlock
      return (
        <AssistantText
          key={'streaming' + turn.id}
          text={sb.chunks.join('')}
          streaming={!sb.complete}
        />
      )
    }

    case 'thinking':
      return <ThinkingBlock key={'think' + turn.id + block.text.slice(0, 8)} text={block.text} />

    case 'tool_use': {
      // Check if this is an Agent call with sub-turns
      if (block.name === 'Agent') {
        const subTurns = allTurns.filter(t => t.parentToolUseId === block.id)
        return (
          <AgentGroup
            key={block.id}
            block={block}
            subTurns={subTurns}
            onApprove={onApprove}
            onDeny={onDeny}
          />
        )
      }
      return <ToolCard key={block.id} block={block} />
    }

    case 'tool_result':
      // Standalone tool_result: monospace card with colored left border
      // (Paired results are rendered inline inside ToolCard)
      return (
        <div
          key={'result-' + block.toolUseId}
          style={{
            background: 'var(--surf2)',
            borderRadius: 8,
            borderLeft: `3px solid ${block.isError ? 'var(--red)' : 'var(--green)'}`,
            padding: '8px 12px',
            margin: '4px 0',
            fontFamily: "'Menlo','Courier New',monospace",
            fontSize: 12,
            color: block.isError ? 'var(--red)' : 'var(--text2)',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-all',
          }}
        >
          {block.content}
        </div>
      )

    case 'control_request':
      return (
        <AskUserQuestion
          key={block.requestId}
          block={block}
          onApprove={onApprove}
          onDeny={onDeny}
        />
      )

    case 'compaction':
      return <SystemEvent key={'compact' + turn.id} text="conversation compacted" variant="compaction" />

    default:
      return null
  }
}

export function EventList({ turns, onApprove, onDeny }: EventListProps) {
  // Only render top-level turns (no parentToolUseId)
  const topLevelTurns = turns.filter(t => !t.parentToolUseId)

  return (
    <div>
      {topLevelTurns.map(turn => {
        if (turn.type === 'user') {
          const textBlocks = turn.blocks.filter(b => b.type === 'text')
          return textBlocks.map((b, i) => (
            <UserMessage key={`${turn.id}-${i}`} text={(b as { text: string }).text} />
          ))
        }

        if (turn.type === 'assistant') {
          return (
            <div key={turn.id} style={{ margin: '4px 0' }}>
              {turn.blocks.map((block, i) => (
                <div key={`${turn.id}-block-${i}`}>
                  {renderBlock(block, turn, turns, onApprove, onDeny)}
                </div>
              ))}
              {turn.usage && (
                <div style={{ color: 'var(--text3)', fontSize: 11, marginTop: 4, textAlign: 'right' }}>
                  {turn.model} · {(turn.usage.inputTokens + turn.usage.outputTokens).toLocaleString()} tokens
                </div>
              )}
            </div>
          )
        }

        if (turn.type === 'system') {
          return turn.blocks.map((block, i) => (
            <div key={`${turn.id}-sys-${i}`}>
              {renderBlock(block, turn, turns, onApprove, onDeny)}
            </div>
          ))
        }

        return null
      })}
    </div>
  )
}
