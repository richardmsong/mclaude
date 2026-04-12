interface DiffViewProps {
  diff: string
  filename?: string
}

interface DiffLine {
  type: 'add' | 'remove' | 'context' | 'header'
  content: string
}

function parseDiff(raw: string): DiffLine[] {
  return raw.split('\n').map(line => {
    if (line.startsWith('+')) return { type: 'add' as const, content: line.slice(1) }
    if (line.startsWith('-')) return { type: 'remove' as const, content: line.slice(1) }
    if (line.startsWith('@@') || line.startsWith('diff ') || line.startsWith('index ') || line.startsWith('---') || line.startsWith('+++')) {
      return { type: 'header' as const, content: line }
    }
    return { type: 'context' as const, content: line.slice(1) }
  })
}

const LINE_STYLES: Record<DiffLine['type'], React.CSSProperties> = {
  add: { background: 'rgba(48,209,88,0.12)', color: 'var(--text)' },
  remove: { background: 'rgba(255,69,58,0.12)', color: 'var(--text)' },
  context: { background: 'transparent', color: 'var(--text3)' },
  header: { background: 'var(--surf3)', color: 'var(--text2)', fontStyle: 'italic' },
}

const GUTTER: Record<DiffLine['type'], string> = {
  add: '+',
  remove: '−',
  context: ' ',
  header: ' ',
}

export function DiffView({ diff, filename }: DiffViewProps) {
  const lines = parseDiff(diff)
  return (
    <div style={{
      background: 'var(--surf2)',
      borderRadius: 8,
      overflow: 'hidden',
      fontSize: 12,
      fontFamily: "'Menlo','Courier New',monospace",
    }}>
      {filename && (
        <div style={{
          padding: '6px 12px',
          background: 'var(--surf3)',
          color: 'var(--text2)',
          borderBottom: '1px solid var(--border)',
          display: 'flex',
          alignItems: 'center',
          gap: 6,
        }}>
          <span>📄</span>
          <span>{filename}</span>
        </div>
      )}
      <div style={{ overflowX: 'auto' }}>
        {lines.map((line, i) => (
          <div key={i} style={{ display: 'flex', ...LINE_STYLES[line.type] }}>
            <span style={{
              width: 20,
              textAlign: 'center',
              flexShrink: 0,
              color: line.type === 'add' ? 'var(--green)' : line.type === 'remove' ? 'var(--red)' : 'var(--text3)',
              userSelect: 'none',
            }}>
              {GUTTER[line.type]}
            </span>
            <span style={{ whiteSpace: 'pre', padding: '1px 8px 1px 4px' }}>
              {line.content}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}
