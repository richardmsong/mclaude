import { StatusDot } from './StatusDot'

interface NavBarProps {
  title: string
  onBack?: () => void
  right?: React.ReactNode
  connected?: boolean
  badge?: number
  onSettings?: () => void
  onUsage?: () => void
  onRefresh?: () => void
}

export function NavBar({ title, onBack, right, connected, badge, onSettings, onUsage, onRefresh }: NavBarProps) {
  const connState = connected === undefined ? 'disconnected' : connected ? 'connected' : 'disconnected'

  return (
    <div style={{
      height: 52,
      background: 'var(--surf)',
      borderBottom: '1px solid var(--border)',
      display: 'flex',
      alignItems: 'center',
      padding: '0 16px',
      flexShrink: 0,
      position: 'sticky',
      top: 0,
      zIndex: 100,
    }}>
      {/* Left */}
      <div style={{ flex: 1, display: 'flex', alignItems: 'center' }}>
        {onBack && (
          <button
            onClick={onBack}
            style={{ color: 'var(--blue)', fontSize: 15, display: 'flex', alignItems: 'center', gap: 2 }}
          >
            ‹ Back
          </button>
        )}
      </div>

      {/* Center */}
      <div style={{
        fontWeight: 600,
        fontSize: 17,
        display: 'flex',
        alignItems: 'center',
        gap: 6,
      }}>
        {title}
        {badge !== undefined && badge > 0 && (
          <span style={{
            background: 'var(--red)',
            color: '#fff',
            fontSize: 11,
            fontWeight: 700,
            borderRadius: 10,
            minWidth: 18,
            height: 18,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: '0 5px',
          }}>
            {badge}
          </span>
        )}
      </div>

      {/* Right */}
      <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: 12 }}>
        {right}
        {onRefresh && (
          <button onClick={onRefresh} style={{ fontSize: 16, color: 'var(--text2)' }} title="Refresh">↻</button>
        )}
        {onUsage && (
          <button onClick={onUsage} style={{ fontSize: 16 }}>📊</button>
        )}
        {onSettings && (
          <button onClick={onSettings} style={{ fontSize: 16 }}>⚙</button>
        )}
        <StatusDot state={connState} size={8} />
      </div>
    </div>
  )
}
