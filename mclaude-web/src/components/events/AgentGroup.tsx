import { useState } from 'react'
import type { ToolUseBlock, Turn } from '@/types'
import { EventList } from './EventList'

interface AgentGroupProps {
  block: ToolUseBlock
  subTurns: Turn[]
  onApprove: (requestId: string) => void
  onDeny: (requestId: string) => void
}

export function AgentGroup({ block, subTurns, onApprove, onDeny }: AgentGroupProps) {
  const [expanded, setExpanded] = useState(false)
  const inp = block.fullInput as Record<string, unknown> | null
  const agentType = String(inp?.['subagent_type'] ?? inp?.['type'] ?? 'agent')
  const description = String(inp?.['description'] ?? inp?.['prompt'] ?? '').slice(0, 80)
  const eventCount = subTurns.reduce((n, t) => n + t.blocks.length, 0)

  return (
    <div style={{
      borderLeft: '3px solid var(--orange)',
      borderRadius: '0 12px 12px 0',
      background: 'var(--surf)',
      overflow: 'hidden',
      margin: '4px 0',
    }}>
      <button
        onClick={() => setExpanded(e => !e)}
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          width: '100%',
          padding: '10px 14px',
          textAlign: 'left',
        }}
      >
        <span>🤖</span>
        <span style={{ color: 'var(--text)', fontWeight: 500 }}>Agent</span>
        <span style={{
          background: 'var(--orange)',
          color: '#000',
          fontSize: 11,
          fontWeight: 600,
          borderRadius: 6,
          padding: '1px 7px',
        }}>
          {agentType}
        </span>
        {description && (
          <span style={{ color: 'var(--text2)', fontSize: 12, flex: 1 }}>
            {description}
          </span>
        )}
        <span style={{ color: 'var(--text3)', fontSize: 12 }}>{eventCount}</span>
        <span style={{ color: 'var(--text3)' }}>{expanded ? '▼' : '▶'}</span>
      </button>
      {expanded && (
        <div style={{ padding: '0 14px 12px 14px' }}>
          <EventList
            turns={subTurns}
            onApprove={onApprove}
            onDeny={onDeny}
          />
        </div>
      )}
    </div>
  )
}
