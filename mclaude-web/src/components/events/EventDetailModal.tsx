import type { Block, Turn } from '@/types'
import { DiffView } from './DiffView'
import { highlightBash } from './ToolCard'

interface EventDetailModalProps {
  block: Block
  turn: Turn
  onClose: () => void
}

function toolIcon(name: string): string {
  const icons: Record<string, string> = {
    Bash: '💻', Edit: '✏️', Write: '📝', Read: '📄',
    Grep: '🔍', Glob: '🔍', Agent: '🤖',
  }
  return icons[name] ?? '🛠'
}

function formatTimestamp(): string {
  return new Date().toLocaleTimeString('en-US', { hour12: false })
}

const LABEL_STYLE: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 600,
  textTransform: 'uppercase',
  letterSpacing: '0.5px',
  color: 'var(--text2)',
  marginBottom: 6,
}

const JSON_PRE_STYLE: React.CSSProperties = {
  background: 'var(--surf2)',
  borderRadius: 8,
  padding: '10px 12px',
  fontFamily: "'Menlo','Courier New',monospace",
  fontSize: 12,
  color: 'var(--text2)',
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-all',
  marginBottom: 14,
  overflow: 'auto',
  maxHeight: 300,
}

const MONO_PRE_STYLE: React.CSSProperties = {
  background: 'var(--surf2)',
  borderRadius: 8,
  padding: '10px 12px',
  fontFamily: "'Menlo','Courier New',monospace",
  fontSize: 12,
  color: 'var(--text2)',
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-all',
  marginBottom: 14,
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return <div style={LABEL_STYLE}>{children}</div>
}

function highlightJson(value: unknown): React.ReactNode {
  const text = JSON.stringify(value, null, 2)
  // Split into tokens: keys (string followed by colon), strings, numbers, booleans/null, punctuation, whitespace
  const tokens = text.split(/("(?:[^"\\]|\\.)*"(?:\s*:)?|true|false|null|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|[{}[\],:])/g)
  return tokens.map((t, i) => {
    if (t === '') return null
    if (/^"(?:[^"\\]|\\.)*":\s*$/.test(t) || /^"(?:[^"\\]|\\.)*":$/.test(t)) return <span key={i} style={{ color: 'var(--blue)' }}>{t}</span>
    if (/^"/.test(t)) return <span key={i} style={{ color: 'var(--green)' }}>{t}</span>
    if (/^-?\d/.test(t)) return <span key={i} style={{ color: 'var(--orange)' }}>{t}</span>
    if (t === 'true' || t === 'false' || t === 'null') return <span key={i} style={{ color: 'var(--purple)' }}>{t}</span>
    if (/^[{}[\],:]$/.test(t)) return <span key={i} style={{ color: 'var(--text3)' }}>{t}</span>
    return <span key={i}>{t}</span>
  })
}

function JsonHighlight({ input }: { input: unknown }) {
  return (
    <pre style={JSON_PRE_STYLE}>
      {highlightJson(input)}
    </pre>
  )
}

function ToolBody({ name, input }: { name: string; input: unknown }) {
  if (!input || typeof input !== 'object') {
    return <JsonHighlight input={input} />
  }
  const inp = input as Record<string, unknown>

  if (name === 'Bash' || name === '!') {
    const command = String(inp['command'] ?? '')
    return (
      <>
        <SectionLabel>Command</SectionLabel>
        <pre style={MONO_PRE_STYLE}>
          {highlightBash(command)}
        </pre>
      </>
    )
  }

  if (name === 'Edit') {
    const filePath = String(inp['file_path'] ?? inp['path'] ?? '')
    const oldStr = String(inp['old_string'] ?? '')
    const newStr = String(inp['new_string'] ?? '')
    const patch = String(inp['patch'] ?? '')
    const diff = oldStr && newStr
      ? oldStr.split('\n').map(l => `-${l}`).join('\n') + '\n' +
        newStr.split('\n').map(l => `+${l}`).join('\n')
      : patch || null
    return (
      <>
        <SectionLabel>File</SectionLabel>
        <pre style={MONO_PRE_STYLE}>{filePath}</pre>
        {diff && (
          <>
            <SectionLabel>Diff</SectionLabel>
            <div style={{ marginBottom: 14 }}>
              <DiffView diff={diff} />
            </div>
          </>
        )}
      </>
    )
  }

  if (name === 'Write') {
    const filePath = String(inp['file_path'] ?? inp['path'] ?? '')
    const content = String(inp['content'] ?? '')
    return (
      <>
        <SectionLabel>File</SectionLabel>
        <pre style={MONO_PRE_STYLE}>{filePath}</pre>
        <SectionLabel>Content</SectionLabel>
        <pre style={MONO_PRE_STYLE}>{content.slice(0, 2000)}</pre>
      </>
    )
  }

  if (name === 'Read') {
    const filePath = String(inp['file_path'] ?? '')
    const offset = inp['offset'] != null ? `L${inp['offset']}` : ''
    const limit = inp['limit'] != null ? `-${inp['limit']}` : ''
    const range = offset ? ` ${offset}${limit}` : ''
    return (
      <>
        <SectionLabel>File</SectionLabel>
        <pre style={MONO_PRE_STYLE}>{filePath}{range}</pre>
      </>
    )
  }

  if (name === 'Grep' || name === 'Glob') {
    const pattern = String(inp['pattern'] ?? '')
    const path = String(inp['path'] ?? '')
    return (
      <>
        {pattern && (
          <>
            <SectionLabel>Pattern</SectionLabel>
            <pre style={MONO_PRE_STYLE}>{pattern}</pre>
          </>
        )}
        {path && (
          <>
            <SectionLabel>Path</SectionLabel>
            <pre style={MONO_PRE_STYLE}>{path}</pre>
          </>
        )}
      </>
    )
  }

  // All other tools: syntax-highlighted JSON
  return <JsonHighlight input={input} />
}

export function EventDetailModal({ block, turn, onClose }: EventDetailModalProps) {
  const title = block.type === 'tool_use'
    ? `${toolIcon(block.name)} ${block.name}`
    : block.type === 'text' ? '📝 Text'
    : block.type === 'thinking' ? '🧠 Thinking'
    : block.type === 'control_request' ? '🔐 Permission'
    : block.type

  return (
    <>
      {/* Scrim */}
      <div
        onClick={onClose}
        style={{
          position: 'fixed',
          inset: 0,
          background: 'rgba(0,0,0,0.5)',
          zIndex: 400,
        }}
      />

      {/* Bottom sheet */}
      <div style={{
        position: 'fixed',
        bottom: 0,
        left: 0,
        right: 0,
        background: 'var(--surf)',
        borderRadius: '16px 16px 0 0',
        zIndex: 401,
        maxHeight: '80vh',
        display: 'flex',
        flexDirection: 'column',
        boxShadow: '0 -4px 24px rgba(0,0,0,0.5)',
      }}>
        {/* Header */}
        <div style={{
          display: 'flex',
          alignItems: 'center',
          padding: '14px 16px',
          borderBottom: '1px solid var(--border)',
          flexShrink: 0,
        }}>
          <span style={{ flex: 1, fontWeight: 600, fontSize: 15 }}>{title}</span>
          <span style={{ color: 'var(--text3)', fontSize: 12, marginRight: 16 }}>
            {formatTimestamp()}
          </span>
          <button
            onClick={onClose}
            style={{ color: 'var(--text2)', fontSize: 18, padding: '0 4px' }}
          >
            ✕
          </button>
        </div>

        {/* Content */}
        <div style={{ flex: 1, overflowY: 'auto', padding: 16 }}>
          {block.type === 'tool_use' && (
            <>
              <ToolBody name={block.name} input={block.fullInput} />
              {block.result && (
                <>
                  <SectionLabel>{block.result.isError ? '⚠ Error' : 'Result'}</SectionLabel>
                  <pre style={{
                    ...MONO_PRE_STYLE,
                    color: block.result.isError ? 'var(--red)' : 'var(--text2)',
                  }}>
                    {block.result.content}
                  </pre>
                </>
              )}
              {block.elapsed !== undefined && (
                <div style={{ color: 'var(--text3)', fontSize: 12, marginTop: 8 }}>
                  Elapsed: {(block.elapsed / 1000).toFixed(2)}s
                </div>
              )}
            </>
          )}
          {(block.type === 'text' || block.type === 'streaming_text') && (
            <pre style={MONO_PRE_STYLE}>
              {block.type === 'text' ? block.text : block.chunks.join('')}
            </pre>
          )}
          {block.type === 'thinking' && (
            <pre style={{ ...MONO_PRE_STYLE, color: 'var(--purple)' }}>
              {block.text}
            </pre>
          )}
          {block.type === 'control_request' && (
            <pre style={MONO_PRE_STYLE}>
              {JSON.stringify({ toolName: block.toolName, input: block.input }, null, 2)}
            </pre>
          )}
          {/* Turn metadata */}
          {turn.usage && (
            <div style={{ color: 'var(--text3)', fontSize: 11, marginTop: 12 }}>
              {turn.model} · {(turn.usage.inputTokens + turn.usage.outputTokens).toLocaleString()} tokens
            </div>
          )}
        </div>
      </div>
    </>
  )
}
