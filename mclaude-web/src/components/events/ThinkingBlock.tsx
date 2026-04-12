import { useState } from 'react'

interface ThinkingBlockProps {
  text: string
}

export function ThinkingBlock({ text }: ThinkingBlockProps) {
  const [expanded, setExpanded] = useState(false)
  return (
    <div style={{ margin: '4px 0' }}>
      <button
        onClick={() => setExpanded(e => !e)}
        style={{
          color: 'var(--purple)',
          display: 'flex',
          alignItems: 'center',
          gap: 6,
          background: 'none',
          padding: '2px 0',
          fontSize: 13,
        }}
      >
        <span>{expanded ? '▼' : '▶'}</span>
        <span>Claude's thinking…</span>
      </button>
      {expanded && (
        <div style={{
          background: 'var(--surf2)',
          borderRadius: 8,
          padding: '8px 12px',
          marginTop: 6,
          fontFamily: "'Menlo','Courier New',monospace",
          fontSize: 12,
          color: 'var(--text2)',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
        }}>
          {text}
        </div>
      )}
    </div>
  )
}
