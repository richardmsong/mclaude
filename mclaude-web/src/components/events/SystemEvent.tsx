interface SystemEventProps {
  text: string
  variant?: 'normal' | 'compaction'
}

export function SystemEvent({ text, variant = 'normal' }: SystemEventProps) {
  return (
    <div style={{
      textAlign: 'center',
      color: 'var(--text3)',
      fontSize: 12,
      padding: '6px 0',
      fontStyle: variant === 'compaction' ? 'italic' : undefined,
    }}>
      {variant === 'compaction' ? `— ${text} —` : text}
    </div>
  )
}
