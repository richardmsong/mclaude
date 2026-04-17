import { useState } from 'react'
import type { Turn, Block, StreamingTextBlock, SkillInvocationBlock, PendingMessage, UserImageBlock } from '@/types'
import { UserMessage } from './UserMessage'
import { AssistantText } from './AssistantText'
import { ThinkingBlock } from './ThinkingBlock'
import { ToolCard } from './ToolCard'
import { AskUserQuestion } from './AskUserQuestion'
import { AgentGroup } from './AgentGroup'
import { SystemEvent } from './SystemEvent'
import { SkillChip } from './SkillChip'
import { TurnUsageBadge } from './TurnUsageBadge'

interface EventListProps {
  turns: Turn[]
  pendingMessages?: PendingMessage[]
  onApprove: (requestId: string) => void
  onDeny: (requestId: string) => void
}

function UserImageThumbnail({ dataUrl, pending }: { dataUrl: string; pending: boolean }) {
  const [lightboxOpen, setLightboxOpen] = useState(false)
  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'flex-end', margin: '4px 0' }}>
        <img
          src={dataUrl}
          alt="attached image"
          style={{
            maxWidth: 240,
            borderRadius: 8,
            display: 'block',
            cursor: 'pointer',
            opacity: pending ? 0.5 : 1,
          }}
          onClick={() => setLightboxOpen(true)}
        />
      </div>
      {lightboxOpen && (
        <div
          onClick={() => setLightboxOpen(false)}
          style={{
            position: 'fixed',
            inset: 0,
            background: 'rgba(0,0,0,0.8)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 1000,
            cursor: 'zoom-out',
          }}
        >
          <img
            src={dataUrl}
            alt="full size"
            style={{ maxWidth: '90vw', maxHeight: '90vh', borderRadius: 8 }}
            onClick={(e) => e.stopPropagation()}
          />
        </div>
      )}
    </>
  )
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
      return <ToolCard key={block.id} block={block} turn={turn} />
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

    case 'system_message':
      return <SystemEvent key={'sysmsg' + turn.id + block.text.slice(0, 8)} text={block.text} variant="compaction" />

    case 'skill_invocation':
      return <SkillChip key={'skill' + turn.id} block={block as SkillInvocationBlock} />

    default:
      return null
  }
}

export function EventList({ turns, pendingMessages = [], onApprove, onDeny }: EventListProps) {
  // Only render top-level turns (no parentToolUseId)
  const topLevelTurns = turns.filter(t => !t.parentToolUseId)

  // Collect the set of uuids that already have an optimistic turn in turns[].
  // pendingMessages whose uuid appears here are already rendered as a user turn,
  // so we must not render them again in the pending section.
  const pendingUuidsInTurns = new Set(
    turns.map(t => t.pendingUuid).filter((u): u is string => u !== undefined),
  )
  const unrenderedPending = pendingMessages.filter(pm => !pendingUuidsInTurns.has(pm.uuid))

  return (
    <div>
      {topLevelTurns.map(turn => {
        if (turn.type === 'user') {
          return turn.blocks.map((block, i) => {
            if (block.type === 'skill_invocation') {
              return (
                <div key={`${turn.id}-${i}`}>
                  {renderBlock(block, turn, turns, onApprove, onDeny)}
                </div>
              )
            }
            if (block.type === 'text') {
              return (
                <UserMessage
                  key={`${turn.id}-${i}`}
                  text={block.text}
                  pending={turn.pendingUuid !== undefined}
                />
              )
            }
            if (block.type === 'user_image') {
              return (
                <UserImageThumbnail
                  key={`${turn.id}-${i}`}
                  dataUrl={(block as UserImageBlock).dataUrl}
                  pending={turn.pendingUuid !== undefined}
                />
              )
            }
            return null
          })
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
                <TurnUsageBadge usage={turn.usage} model={turn.model} />
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
      {unrenderedPending.map(pm => {
        const textContent = typeof pm.content === 'string'
          ? pm.content
          : pm.content.filter(c => c.type === 'text').map(c => c.text ?? '').join('')
        const imageItems = typeof pm.content === 'string'
          ? []
          : pm.content.filter(c => c.type === 'image' && c.source?.type === 'base64')
        return (
          <div key={pm.uuid}>
            {textContent && (
              <div
                style={{
                  opacity: 0.5,
                  padding: '6px 12px',
                  margin: '4px 0',
                  background: 'var(--surf)',
                  borderRadius: 12,
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 2,
                }}
              >
                <div style={{ color: 'var(--text)', fontSize: 15 }}>{textContent}</div>
                <div style={{ color: 'var(--text3)', fontSize: 11 }}>sending...</div>
              </div>
            )}
            {imageItems.map((c, i) => (
              <UserImageThumbnail
                key={`${pm.uuid}-img-${i}`}
                dataUrl={`data:${c.source!.media_type};base64,${c.source!.data}`}
                pending={true}
              />
            ))}
          </div>
        )
      })}
    </div>
  )
}
