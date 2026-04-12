import { useState } from 'react'
import type { ControlRequestBlock } from '@/types'

interface AskUserQuestionProps {
  block: ControlRequestBlock
  onApprove: (requestId: string) => void
  onDeny: (requestId: string) => void
}

interface ToolOptions {
  question?: string
  options?: Array<{ value: string; description?: string }>
}

export function AskUserQuestion({ block, onApprove, onDeny }: AskUserQuestionProps) {
  const [selected, setSelected] = useState<string | null>(null)
  const isDone = block.status !== 'pending'

  // Try to parse structured options from input
  const inp = block.input as ToolOptions | null
  const question = inp?.question ?? block.toolName
  const options = inp?.options

  const handleSubmit = () => {
    if (!selected) return
    onApprove(block.requestId)
  }

  if (options && options.length > 0) {
    return (
      <div style={{
        background: 'var(--surf)',
        border: '1px solid var(--border)',
        borderRadius: 12,
        padding: '12px 16px',
        margin: '4px 0',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
          <span>❓</span>
          <span style={{ fontWeight: 500 }}>Question</span>
        </div>
        <p style={{ color: 'var(--text)', marginBottom: 12 }}>{question}</p>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 12 }}>
          {options.map(opt => (
            <label
              key={opt.value}
              style={{
                display: 'flex',
                alignItems: 'flex-start',
                gap: 10,
                cursor: isDone ? 'default' : 'pointer',
                opacity: isDone ? 0.7 : 1,
              }}
            >
              <input
                type="radio"
                name={`q-${block.requestId}`}
                value={opt.value}
                checked={selected === opt.value}
                disabled={isDone}
                onChange={() => setSelected(opt.value)}
                style={{ marginTop: 3 }}
              />
              <div>
                <div style={{ color: 'var(--text)' }}>{opt.value}</div>
                {opt.description && (
                  <div style={{ color: 'var(--text2)', fontSize: 12 }}>{opt.description}</div>
                )}
              </div>
            </label>
          ))}
        </div>
        {!isDone ? (
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              onClick={handleSubmit}
              disabled={!selected}
              style={{
                flex: 1,
                padding: '8px 0',
                background: selected ? 'var(--blue)' : 'var(--surf3)',
                color: 'var(--text)',
                borderRadius: 8,
                fontWeight: 500,
                opacity: selected ? 1 : 0.5,
              }}
            >
              Submit
            </button>
            <button
              onClick={() => onDeny(block.requestId)}
              style={{
                padding: '8px 16px',
                background: 'var(--surf2)',
                color: 'var(--red)',
                borderRadius: 8,
              }}
            >
              Cancel
            </button>
          </div>
        ) : (
          <div style={{ color: 'var(--green)', fontSize: 13 }}>✓ Submitted</div>
        )}
      </div>
    )
  }

  // Generic permission request
  return (
    <div style={{
      background: 'var(--surf)',
      border: `1px solid ${block.status === 'pending' ? 'var(--orange)' : 'var(--border)'}`,
      borderRadius: 12,
      padding: '10px 14px',
      margin: '4px 0',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
        <span>🔐</span>
        <span style={{ fontWeight: 500, color: 'var(--orange)' }}>{block.toolName}</span>
        {isDone && (
          <span style={{
            marginLeft: 'auto',
            color: block.status === 'approved' ? 'var(--green)' : 'var(--red)',
            fontSize: 12,
          }}>
            {block.status === 'approved' ? '✓ approved' : '✕ denied'}
          </span>
        )}
      </div>
      {block.input !== null && block.input !== undefined && (
        <pre style={{
          fontFamily: "'Menlo','Courier New',monospace",
          fontSize: 11,
          color: 'var(--text2)',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-all',
          marginBottom: isDone ? 0 : 10,
        }}>
          {JSON.stringify(block.input, null, 2).slice(0, 300)}
        </pre>
      )}
      {!isDone && (
        <div style={{ display: 'flex', gap: 8 }}>
          <button
            onClick={() => onApprove(block.requestId)}
            style={{
              flex: 1,
              padding: '7px 0',
              background: 'var(--green)',
              color: '#000',
              borderRadius: 8,
              fontWeight: 600,
              fontSize: 13,
            }}
          >
            ✓ Approve
          </button>
          <button
            onClick={() => onDeny(block.requestId)}
            style={{
              flex: 1,
              padding: '7px 0',
              background: 'var(--surf2)',
              color: 'var(--red)',
              borderRadius: 8,
              fontWeight: 600,
              fontSize: 13,
            }}
          >
            ✕ Deny
          </button>
        </div>
      )}
    </div>
  )
}
