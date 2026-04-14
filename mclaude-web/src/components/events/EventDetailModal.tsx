import type { Block, Turn } from '@/types'

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
              {block.fullInput !== undefined && (
                <>
                  <div style={{ fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.5px', color: 'var(--text2)', marginBottom: 6 }}>
                    Input
                  </div>
                  <pre style={{
                    background: 'var(--surf2)',
                    borderRadius: 8,
                    padding: '10px 12px',
                    fontFamily: "'Menlo','Courier New',monospace",
                    fontSize: 12,
                    color: 'var(--text2)',
                    whiteSpace: 'pre-wrap',
                    wordBreak: 'break-all',
                    marginBottom: 14,
                  }}>
                    {JSON.stringify(block.fullInput, null, 2)}
                  </pre>
                </>
              )}
              {block.result && (
                <>
                  <div style={{ fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.5px', color: 'var(--text2)', marginBottom: 6 }}>
                    {block.result.isError ? '⚠ Error' : 'Result'}
                  </div>
                  <pre style={{
                    background: 'var(--surf2)',
                    borderRadius: 8,
                    padding: '10px 12px',
                    fontFamily: "'Menlo','Courier New',monospace",
                    fontSize: 12,
                    color: block.result.isError ? 'var(--red)' : 'var(--text2)',
                    whiteSpace: 'pre-wrap',
                    wordBreak: 'break-all',
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
            <pre style={{
              background: 'var(--surf2)',
              borderRadius: 8,
              padding: '10px 12px',
              fontFamily: "'Menlo','Courier New',monospace",
              fontSize: 12,
              color: 'var(--text2)',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
            }}>
              {block.type === 'text' ? block.text : block.chunks.join('')}
            </pre>
          )}
          {block.type === 'thinking' && (
            <pre style={{
              background: 'var(--surf2)',
              borderRadius: 8,
              padding: '10px 12px',
              fontFamily: "'Menlo','Courier New',monospace",
              fontSize: 12,
              color: 'var(--purple)',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
            }}>
              {block.text}
            </pre>
          )}
          {block.type === 'control_request' && (
            <pre style={{
              background: 'var(--surf2)',
              borderRadius: 8,
              padding: '10px 12px',
              fontFamily: "'Menlo','Courier New',monospace",
              fontSize: 12,
              color: 'var(--text2)',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
            }}>
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
