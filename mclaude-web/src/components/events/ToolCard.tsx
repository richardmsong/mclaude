import { useState } from 'react'
import type { ToolUseBlock } from '@/types'
import { DiffView } from './DiffView'

const TOOL_ICONS: Record<string, string> = {
  Bash: '💻',
  Edit: '✏️',
  Write: '📝',
  Read: '📄',
  Grep: '🔍',
  Glob: '🔍',
  Agent: '🤖',
  WebFetch: '🌐',
  WebSearch: '🌐',
}

function toolIcon(name: string): string {
  return TOOL_ICONS[name] ?? '🛠'
}

// Minimal bash syntax highlighting
function highlightBash(cmd: string): React.ReactNode {
  // Highlight first word as command, flags as cyan, strings as green
  const parts = cmd.split(/(\s+|"[^"]*"|'[^']*'|--?\w+|\$\w+)/g)
  return (
    <>
      {parts.map((part, i) => {
        if (i === 0) return <span key={i} style={{ color: 'var(--blue)' }}>{part}</span>
        if (part.startsWith('"') || part.startsWith("'")) return <span key={i} style={{ color: 'var(--green)' }}>{part}</span>
        if (part.startsWith('--') || part.startsWith('-')) return <span key={i} style={{ color: '#57c0ff' }}>{part}</span>
        if (part.startsWith('$')) return <span key={i} style={{ color: 'var(--orange)' }}>{part}</span>
        return <span key={i}>{part}</span>
      })}
    </>
  )
}

function formatInput(toolName: string, input: unknown): { summary: string; isDiff: boolean; diff?: string } {
  if (!input || typeof input !== 'object') {
    return { summary: String(input ?? ''), isDiff: false }
  }
  const inp = input as Record<string, unknown>
  if (toolName === 'Bash' || toolName === '!') {
    return { summary: String(inp['command'] ?? ''), isDiff: false }
  }
  if (toolName === 'Edit' || toolName === 'Write') {
    const file = String(inp['file_path'] ?? inp['path'] ?? '')
    const parts = file.split('/')
    const shortPath = parts.slice(-2).join('/')
    const oldStr = String(inp['old_string'] ?? '')
    const newStr = String(inp['new_string'] ?? '')
    if (toolName === 'Edit' && oldStr && newStr) {
      const diff = oldStr.split('\n').map(l => `-${l}`).join('\n') + '\n' +
                   newStr.split('\n').map(l => `+${l}`).join('\n')
      return { summary: shortPath, isDiff: true, diff }
    }
    return { summary: shortPath, isDiff: false }
  }
  if (toolName === 'Read') {
    const file = String(inp['file_path'] ?? '')
    const parts = file.split('/')
    const start = inp['offset'] ? `L${inp['offset']}` : ''
    const end = inp['limit'] ? `-${inp['limit']}` : ''
    return { summary: parts.slice(-2).join('/') + (start ? ` ${start}${end}` : ''), isDiff: false }
  }
  if (toolName === 'Grep' || toolName === 'Glob') {
    const pattern = String(inp['pattern'] ?? inp['path'] ?? '')
    const path = String(inp['path'] ?? '').split('/').slice(-2).join('/')
    return { summary: `${pattern} ${path}`.trim(), isDiff: false }
  }
  return { summary: JSON.stringify(input).slice(0, 120), isDiff: false }
}

interface ToolCardProps {
  block: ToolUseBlock
}

export function ToolCard({ block }: ToolCardProps) {
  const [showDetail, setShowDetail] = useState(false)
  const { summary, isDiff, diff } = formatInput(block.name, block.fullInput)
  const isBash = block.name === 'Bash' || block.name === '!'
  const isError = block.result?.isError

  return (
    <div style={{
      background: 'var(--surf)',
      border: '1px solid var(--border)',
      borderRadius: 12,
      overflow: 'hidden',
      margin: '4px 0',
    }}>
      {/* Tool header + body */}
      <div
        onClick={() => setShowDetail(s => !s)}
        style={{
          padding: '8px 12px',
          cursor: 'pointer',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
          <span>{toolIcon(block.name)}</span>
          <span style={{ color: 'var(--text)', fontWeight: 500, fontSize: 13 }}>{block.name}</span>
          {block.elapsed && (
            <span style={{ color: 'var(--text3)', fontSize: 11, marginLeft: 'auto' }}>
              {(block.elapsed / 1000).toFixed(1)}s
            </span>
          )}
          {isError && <span style={{ color: 'var(--red)', fontSize: 11 }}>error</span>}
        </div>
        <div style={{
          fontFamily: "'Menlo','Courier New',monospace",
          fontSize: 12,
          color: 'var(--text2)',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-all',
        }}>
          {isBash ? highlightBash(summary) : summary}
        </div>
      </div>

      {/* Result body */}
      {block.result && (
        <div style={{
          borderTop: `1px solid var(--border)`,
          padding: '8px 12px',
          background: 'var(--surf2)',
        }}>
          {isDiff && diff ? (
            <DiffView diff={diff} />
          ) : (
            <pre style={{
              fontFamily: "'Menlo','Courier New',monospace",
              fontSize: 12,
              color: isError ? 'var(--red)' : 'var(--text2)',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              maxHeight: showDetail ? undefined : 200,
              overflow: showDetail ? undefined : 'hidden',
            }}>
              {block.result.content}
            </pre>
          )}
          {!isDiff && block.result.content.length > 300 && (
            <button
              onClick={(e) => { e.stopPropagation(); setShowDetail(s => !s) }}
              style={{ color: 'var(--blue)', fontSize: 12, marginTop: 4 }}
            >
              {showDetail ? 'show less' : 'show more'}
            </button>
          )}
        </div>
      )}

      {/* No result yet — still running */}
      {!block.result && (
        <div style={{
          borderTop: '1px solid var(--border)',
          padding: '6px 12px',
          background: 'var(--surf2)',
          color: 'var(--text3)',
          fontSize: 12,
          fontStyle: 'italic',
        }}>
          running…
        </div>
      )}
    </div>
  )
}
