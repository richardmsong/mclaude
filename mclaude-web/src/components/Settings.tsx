import { NavBar } from './NavBar'
import { StatusDot } from './StatusDot'

interface SettingsProps {
  userId: string
  serverUrl: string
  connected: boolean
  sessionCount: number
  onBack: () => void
  onLogout: () => void
  onCacheReset?: () => void
}

export function Settings({ userId, serverUrl, connected, sessionCount, onBack, onLogout, onCacheReset }: SettingsProps) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--bg)' }}>
      <NavBar title="Settings" onBack={onBack} connected={connected} />

      <div style={{ flex: 1, overflowY: 'auto', padding: '16px' }}>
        {/* HOST section */}
        <div style={sectionLabel}>HOST</div>
        <div style={card}>
          <div style={row}>
            <span style={{ color: 'var(--text2)' }}>Server</span>
            <span style={{ color: 'var(--text)', fontSize: 13 }}>
              {serverUrl || '—'}
            </span>
          </div>
        </div>

        {/* CONNECTION section */}
        <div style={{ ...sectionLabel, marginTop: 20 }}>CONNECTION</div>
        <div style={card}>
          <div style={row}>
            <span style={{ color: 'var(--text2)' }}>Status</span>
            <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <StatusDot state={connected ? 'connected' : 'disconnected'} size={8} />
              <span style={{ color: 'var(--text)', fontSize: 13 }}>
                {connected ? 'Connected' : 'Disconnected'}
              </span>
            </div>
          </div>
          <div style={{ ...row, borderTop: '1px solid var(--border)' }}>
            <span style={{ color: 'var(--text2)' }}>Sessions</span>
            <span style={{ color: 'var(--text)', fontSize: 13 }}>{sessionCount}</span>
          </div>
        </div>

        {/* ACCOUNT section */}
        <div style={{ ...sectionLabel, marginTop: 20 }}>ACCOUNT</div>
        <div style={card}>
          <div style={row}>
            <span style={{ color: 'var(--text2)' }}>User ID</span>
            <span style={{ color: 'var(--text)', fontSize: 12, fontFamily: 'monospace' }}>
              {userId.slice(0, 16)}…
            </span>
          </div>
        </div>

        {/* Cache reset */}
        {onCacheReset && (
          <div style={{ marginTop: 20 }}>
            <div style={sectionLabel}>ADVANCED</div>
            <div style={card}>
              <button
                onClick={onCacheReset}
                style={{
                  ...row,
                  width: '100%',
                  background: 'none',
                  textAlign: 'left',
                  color: 'var(--text)',
                }}
              >
                <span style={{ color: 'var(--text2)' }}>Reset Client Cache</span>
                <span style={{ color: 'var(--text3)', fontSize: 12 }}>Clear all local state and re-subscribe</span>
              </button>
            </div>
          </div>
        )}

        {/* Sign out */}
        <div style={{ marginTop: 20 }}>
          <button
            onClick={onLogout}
            style={{
              width: '100%',
              padding: '14px',
              background: 'var(--surf)',
              border: '1px solid var(--border)',
              borderRadius: 12,
              color: 'var(--red)',
              fontWeight: 500,
              fontSize: 15,
            }}
          >
            Sign Out
          </button>
        </div>
      </div>
    </div>
  )
}

const sectionLabel: React.CSSProperties = {
  fontSize: 12,
  fontWeight: 600,
  letterSpacing: '0.5px',
  textTransform: 'uppercase',
  color: 'var(--text2)',
  marginBottom: 8,
}

const card: React.CSSProperties = {
  background: 'var(--surf)',
  border: '1px solid var(--border)',
  borderRadius: 12,
  overflow: 'hidden',
}

const row: React.CSSProperties = {
  display: 'flex',
  justifyContent: 'space-between',
  alignItems: 'center',
  padding: '12px 14px',
}
