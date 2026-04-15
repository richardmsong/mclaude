import type { SessionState } from '@/types'

type ConnState = 'connected' | 'connecting' | 'error' | 'disconnected'

interface StatusDotProps {
  state: SessionState | ConnState
  size?: number
}

const STATE_COLORS: Record<string, string> = {
  working: 'var(--orange)',
  running: 'var(--orange)',
  requires_action: 'var(--red)',
  needs_permission: 'var(--red)',
  plan_mode: 'var(--purple)',
  idle: 'var(--green)',
  connected: 'var(--green)',
  connecting: 'var(--text3)',
  error: 'var(--red)',
  disconnected: 'var(--text3)',
  restarting: 'var(--orange)',
  failed: 'var(--red)',
  updating: 'var(--blue)',
  unknown: 'var(--text3)',
  waiting_for_input: 'var(--text3)',
}

const PULSE_STATES = new Set(['working', 'running', 'connecting', 'updating'])

export function StatusDot({ state, size = 8 }: StatusDotProps) {
  const color = STATE_COLORS[state] ?? 'var(--text3)'
  const shouldPulse = PULSE_STATES.has(state)
  return (
    <span
      className={shouldPulse ? 'pulse' : undefined}
      style={{
        display: 'inline-block',
        width: size,
        height: size,
        borderRadius: '50%',
        background: color,
        flexShrink: 0,
      }}
    />
  )
}
