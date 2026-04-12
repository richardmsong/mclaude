import { useState } from 'react'

interface AuthScreenProps {
  onConnect: (email: string, password: string) => Promise<void>
  error?: string
}

export function AuthScreen({ onConnect, error }: AuthScreenProps) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [localError, setLocalError] = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!password.trim()) return
    setLoading(true)
    setLocalError('')
    try {
      await onConnect(email.trim(), password.trim())
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : 'Connection failed')
    } finally {
      setLoading(false)
    }
  }

  const displayError = localError || error

  return (
    <div
      data-testid="auth-screen"
      style={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        minHeight: '100vh',
        padding: 24,
        background: 'var(--bg)',
      }}
    >
      <div style={{ width: '100%', maxWidth: 360 }}>
        {/* Icon + title */}
        <div style={{ textAlign: 'center', marginBottom: 36 }}>
          <div style={{ fontSize: 52, marginBottom: 12 }}>⚡</div>
          <div data-testid="auth-title" style={{ fontSize: 28, fontWeight: 700, color: 'var(--text)' }}>MClaude</div>
          <div style={{ color: 'var(--text2)', marginTop: 6 }}>Enter your access token</div>
        </div>

        <form onSubmit={handleSubmit} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <input
            type="email"
            placeholder="Email (optional)"
            value={email}
            onChange={e => setEmail(e.target.value)}
            style={inputStyle}
          />
          <input
            data-testid="password-field"
            type="password"
            placeholder="Access token"
            value={password}
            onChange={e => setPassword(e.target.value)}
            required
            autoFocus
            style={inputStyle}
          />
          {displayError && (
            <div style={{ color: 'var(--red)', fontSize: 13, textAlign: 'center' }}>
              {displayError}
            </div>
          )}
          <button
            type="submit"
            data-testid="connect-button"
            disabled={loading || !password.trim()}
            style={{
              padding: '12px 0',
              background: 'var(--blue)',
              color: '#fff',
              borderRadius: 12,
              fontSize: 16,
              fontWeight: 600,
              opacity: loading ? 0.7 : 1,
              transition: 'opacity 0.15s',
            }}
          >
            {loading ? 'Connecting…' : 'Connect'}
          </button>
        </form>
      </div>
    </div>
  )
}

const inputStyle: React.CSSProperties = {
  padding: '12px 14px',
  background: 'var(--surf)',
  border: '1px solid var(--border)',
  borderRadius: 12,
  color: 'var(--text)',
  fontSize: 15,
  width: '100%',
}
