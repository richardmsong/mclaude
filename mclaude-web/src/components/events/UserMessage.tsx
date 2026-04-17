interface UserMessageProps {
  text: string
  /** When true the message is optimistic (not yet echoed by server) — rendered dimmed. */
  pending?: boolean
}

// Check if text is a screenshot path
function isScreenshotPath(text: string): boolean {
  return /\/tmp\/mclaude-screenshots\/.*\.png$/i.test(text.trim())
}

export function UserMessage({ text, pending = false }: UserMessageProps) {
  const isImage = isScreenshotPath(text)
  return (
    <div style={{ display: 'flex', justifyContent: 'flex-end', margin: '4px 0', opacity: pending ? 0.5 : 1 }}>
      {isImage ? (
        <div style={{
          maxWidth: '80%',
          borderRadius: 12,
          overflow: 'hidden',
          border: '1px solid var(--border)',
        }}>
          <img
            src={text.trim()}
            alt="screenshot"
            style={{ display: 'block', maxWidth: '100%', borderRadius: 12 }}
            onError={(e) => {
              (e.target as HTMLImageElement).style.display = 'none'
            }}
          />
        </div>
      ) : (
        <div style={{
          background: 'var(--blue)',
          color: '#ffffff',
          padding: '8px 14px',
          borderRadius: 18,
          borderBottomRightRadius: 4,
          maxWidth: '80%',
          wordBreak: 'break-word',
          lineHeight: 1.4,
          whiteSpace: 'pre-wrap',
        }}>
          {text}
        </div>
      )}
    </div>
  )
}
