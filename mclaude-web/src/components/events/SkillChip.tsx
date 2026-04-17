import { useState } from 'react'
import type { SkillInvocationBlock } from '@/types'

interface SkillChipProps {
  block: SkillInvocationBlock
}

export function SkillChip({ block }: SkillChipProps) {
  const [expanded, setExpanded] = useState(false)

  return (
    <div
      className="ev-skill"
      style={{
        borderLeft: '3px solid var(--blue)',
        background: 'var(--surf)',
        borderRadius: '0 12px 12px 0',
        overflow: 'hidden',
        margin: '4px 0',
      }}
    >
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
        <span>🔧</span>
        <span style={{ color: 'var(--text)', fontWeight: 700 }}>{block.skillName}</span>
        <span style={{
          background: 'var(--blue)',
          color: '#000',
          fontSize: 11,
          fontWeight: 600,
          borderRadius: 6,
          padding: '1px 7px',
        }}>
          Skill
        </span>
        <span style={{ flex: 1 }} />
        <span style={{ color: 'var(--text3)' }}>{expanded ? '▼' : '▶'}</span>
      </button>
      {expanded && (
        <div style={{ padding: '0 14px 12px 14px' }}>
          {block.args && (
            <div style={{
              color: 'var(--text2)',
              fontSize: 13,
              marginBottom: 10,
              whiteSpace: 'pre-wrap',
            }}>
              {block.args}
            </div>
          )}
          <div style={{
            background: 'var(--surf2)',
            borderRadius: 8,
            padding: '8px 12px',
            fontFamily: "'Menlo','Courier New',monospace",
            fontSize: 12,
            color: 'var(--text2)',
            whiteSpace: 'pre-wrap',
            overflowX: 'auto',
          }}>
            {block.rawContent}
          </div>
        </div>
      )}
    </div>
  )
}
