import React from 'react'

interface AssistantTextProps {
  text: string
  streaming?: boolean
}

// Minimal markdown renderer — handles the most common constructs
function renderMarkdown(text: string): React.ReactNode {
  const lines = text.split('\n')
  const elements: React.ReactNode[] = []
  let i = 0

  while (i < lines.length) {
    const line = lines[i]

    // Code block
    if (line.trimStart().startsWith('```')) {
      const lang = line.trim().slice(3).trim()
      const codeLines: string[] = []
      i++
      while (i < lines.length && !lines[i].trimStart().startsWith('```')) {
        codeLines.push(lines[i])
        i++
      }
      elements.push(
        <pre key={i} style={{
          background: 'var(--surf2)',
          borderRadius: 8,
          padding: '10px 12px',
          overflowX: 'auto',
          fontFamily: "'Menlo','Courier New',monospace",
          fontSize: 12,
          margin: '6px 0',
          color: 'var(--text)',
        }}>
          {lang && <div style={{ color: 'var(--text2)', fontSize: 11, marginBottom: 4 }}>{lang}</div>}
          <code>{codeLines.join('\n')}</code>
        </pre>
      )
      i++ // skip closing ```
      continue
    }

    // Headings
    const headingMatch = /^(#{1,4})\s+(.+)$/.exec(line)
    if (headingMatch) {
      const level = headingMatch[1].length
      const sizes = ['1.4em', '1.2em', '1.1em', '1em']
      elements.push(
        <div key={i} style={{
          fontSize: sizes[level - 1],
          fontWeight: 700,
          margin: '8px 0 4px',
          color: 'var(--text)',
        }}>
          {renderInline(headingMatch[2])}
        </div>
      )
      i++
      continue
    }

    // Unordered list item
    if (/^(\s*[-*+]\s)/.test(line)) {
      const items: string[] = []
      while (i < lines.length && /^(\s*[-*+]\s)/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*[-*+]\s/, ''))
        i++
      }
      elements.push(
        <ul key={i} style={{ margin: '4px 0', paddingLeft: 20 }}>
          {items.map((item, j) => (
            <li key={j} style={{ margin: '2px 0' }}>{renderInline(item)}</li>
          ))}
        </ul>
      )
      continue
    }

    // Ordered list item
    if (/^\d+\.\s/.test(line)) {
      const items: string[] = []
      while (i < lines.length && /^\d+\.\s/.test(lines[i])) {
        items.push(lines[i].replace(/^\d+\.\s/, ''))
        i++
      }
      elements.push(
        <ol key={i} style={{ margin: '4px 0', paddingLeft: 20 }}>
          {items.map((item, j) => (
            <li key={j} style={{ margin: '2px 0' }}>{renderInline(item)}</li>
          ))}
        </ol>
      )
      continue
    }

    // GFM Table — lines starting with | that form header / separator / rows
    if (/^\|/.test(line)) {
      const tableLines: string[] = []
      while (i < lines.length && /^\|/.test(lines[i])) {
        tableLines.push(lines[i])
        i++
      }
      // Need at least header + separator
      if (tableLines.length >= 2 && /^\|[\s|:-]+\|/.test(tableLines[1])) {
        const headerCells = tableLines[0].split('|').slice(1, -1).map(c => c.trim())
        const bodyRows = tableLines.slice(2).map(row =>
          row.split('|').slice(1, -1).map(c => c.trim())
        )
        elements.push(
          <div key={i} style={{ overflowX: 'auto', margin: '6px 0' }}>
            <table style={{
              borderCollapse: 'collapse',
              fontFamily: "'Menlo','Courier New',monospace",
              fontSize: 13,
              background: 'var(--surf2)',
              width: '100%',
            }}>
              <thead>
                <tr>
                  {headerCells.map((cell, j) => (
                    <th key={j} style={{
                      padding: '6px 10px',
                      borderBottom: '2px solid var(--border)',
                      borderRight: j < headerCells.length - 1 ? '1px solid var(--border)' : undefined,
                      textAlign: 'left',
                      color: 'var(--text)',
                      fontWeight: 600,
                      whiteSpace: 'nowrap',
                    }}>
                      {renderInline(cell)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {bodyRows.map((cells, ri) => (
                  <tr key={ri}>
                    {cells.map((cell, j) => (
                      <td key={j} style={{
                        padding: '6px 10px',
                        borderTop: '1px solid var(--border)',
                        borderRight: j < cells.length - 1 ? '1px solid var(--border)' : undefined,
                        color: 'var(--text)',
                        background: 'var(--surf)',
                        whiteSpace: 'nowrap',
                      }}>
                        {renderInline(cell)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      } else {
        // Doesn't look like a proper table — render lines as paragraphs
        for (const tl of tableLines) {
          elements.push(<p key={i + tl} style={{ margin: '2px 0' }}>{renderInline(tl)}</p>)
        }
      }
      continue
    }

    // Blank line — spacer
    if (line.trim() === '') {
      elements.push(<div key={i} style={{ height: 8 }} />)
      i++
      continue
    }

    // Paragraph
    elements.push(<p key={i} style={{ margin: '2px 0' }}>{renderInline(line)}</p>)
    i++
  }

  return <>{elements}</>
}

function renderInline(text: string): React.ReactNode {
  // Split on inline code, bold, italic
  const parts = text.split(/(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*)/g)
  return (
    <>
      {parts.map((part, i) => {
        if (part.startsWith('`') && part.endsWith('`')) {
          return (
            <code key={i} style={{
              background: 'var(--surf2)',
              borderRadius: 4,
              padding: '1px 5px',
              fontFamily: "'Menlo','Courier New',monospace",
              fontSize: '0.9em',
            }}>
              {part.slice(1, -1)}
            </code>
          )
        }
        if (part.startsWith('**') && part.endsWith('**')) {
          return <strong key={i}>{part.slice(2, -2)}</strong>
        }
        if (part.startsWith('*') && part.endsWith('*')) {
          return <em key={i}>{part.slice(1, -1)}</em>
        }
        return <React.Fragment key={i}>{part}</React.Fragment>
      })}
    </>
  )
}

export function AssistantText({ text, streaming }: AssistantTextProps) {
  return (
    <div style={{
      color: 'var(--text)',
      lineHeight: 1.6,
      wordBreak: 'break-word',
    }}>
      {renderMarkdown(text)}
      {streaming && (
        <span style={{
          display: 'inline-block',
          width: 2,
          height: '1em',
          background: 'var(--text)',
          verticalAlign: 'middle',
          animation: 'pulse-opacity 0.8s step-end infinite',
          marginLeft: 1,
        }} />
      )}
    </div>
  )
}
