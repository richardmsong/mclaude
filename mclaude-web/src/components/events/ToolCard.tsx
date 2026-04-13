import { useState } from 'react'
import type { ToolUseBlock, Turn } from '@/types'
import { DiffView } from './DiffView'
import { EventDetailModal } from './EventDetailModal'

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

// Bash keywords that should be highlighted in purple
const BASH_KEYWORDS = new Set([
  'if', 'then', 'else', 'elif', 'fi',
  'for', 'do', 'done', 'while', 'until',
  'case', 'esac', 'in',
  'function', 'return', 'exit', 'break', 'continue',
  'echo', 'printf', 'export', 'local', 'readonly', 'declare',
  'cd', 'pwd', 'ls', 'cp', 'mv', 'rm', 'mkdir', 'touch',
  'grep', 'sed', 'awk', 'cut', 'sort', 'uniq', 'head', 'tail',
  'cat', 'find', 'xargs', 'tr', 'wc',
  'true', 'false', 'test', 'source', '.', 'exec',
])

// Bash operators — pipe, redirects, logical
const BASH_OPERATORS = /^(\|\||&&|[|&;><]|>>|<<|2>|2>>)$/

// Tokenize a bash command into typed segments for syntax highlighting
type BashToken = { kind: 'command' | 'keyword' | 'operator' | 'string' | 'flag' | 'variable' | 'plain'; text: string }

function tokenizeBash(cmd: string): BashToken[] {
  // Split preserving whitespace, strings, operators, flags, variables, and words
  const re = /("(?:[^"\\]|\\.)*"|'[^']*'|`[^`]*`|\$\{[^}]*\}|\$\w+|&&|\|\||>>|<<|2>>|2>|[|&;><]|--?\w[\w-]*|\S+)/g
  const tokens: BashToken[] = []
  let lastIndex = 0
  let isFirst = true
  let match: RegExpExecArray | null

  while ((match = re.exec(cmd)) !== null) {
    // Add whitespace before token
    if (match.index > lastIndex) {
      tokens.push({ kind: 'plain', text: cmd.slice(lastIndex, match.index) })
    }
    lastIndex = match.index + match[0].length
    const t = match[0]

    if (t.startsWith('"') || t.startsWith("'") || t.startsWith('`')) {
      tokens.push({ kind: 'string', text: t })
      isFirst = false
    } else if (t.startsWith('$')) {
      tokens.push({ kind: 'variable', text: t })
    } else if (BASH_OPERATORS.test(t)) {
      tokens.push({ kind: 'operator', text: t })
      isFirst = true // token after operator is a new command
    } else if (t.startsWith('--') || (t.startsWith('-') && t.length > 1 && /^-[a-zA-Z]/.test(t))) {
      tokens.push({ kind: 'flag', text: t })
    } else if (isFirst) {
      tokens.push({ kind: BASH_KEYWORDS.has(t) ? 'keyword' : 'command', text: t })
      isFirst = false
    } else if (BASH_KEYWORDS.has(t)) {
      tokens.push({ kind: 'keyword', text: t })
    } else {
      tokens.push({ kind: 'plain', text: t })
    }
  }
  if (lastIndex < cmd.length) {
    tokens.push({ kind: 'plain', text: cmd.slice(lastIndex) })
  }
  return tokens
}

const TOKEN_COLORS: Record<BashToken['kind'], string | undefined> = {
  command: 'var(--blue)',
  keyword: 'var(--purple)',
  operator: 'var(--orange)',
  string: 'var(--green)',
  flag: '#57c0ff', // cyan
  variable: '#ffd60a', // yellow
  plain: undefined,
}

function highlightBash(cmd: string): React.ReactNode {
  const tokens = tokenizeBash(cmd)
  return (
    <>
      {tokens.map((tok, i) => {
        const color = TOKEN_COLORS[tok.kind]
        return color
          ? <span key={i} style={{ color }}>{tok.text}</span>
          : <span key={i}>{tok.text}</span>
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
  turn?: Turn
}

export function ToolCard({ block, turn }: ToolCardProps) {
  const [showDetail, setShowDetail] = useState(false)
  const [showModal, setShowModal] = useState(false)
  const { summary, isDiff, diff } = formatInput(block.name, block.fullInput)
  const isBash = block.name === 'Bash' || block.name === '!'
  const isError = block.result?.isError

  return (
    <>
    {showModal && turn && (
      <EventDetailModal block={block} turn={turn} onClose={() => setShowModal(false)} />
    )}
    <div style={{
      background: 'var(--surf)',
      border: '1px solid var(--border)',
      borderRadius: 12,
      overflow: 'hidden',
      margin: '4px 0',
    }}>
      {/* Tool header + body — tap to open detail modal */}
      <div
        onClick={() => turn ? setShowModal(true) : setShowDetail(s => !s)}
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
    </>
  )
}
