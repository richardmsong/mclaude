import { useState, useEffect } from 'react'

interface DeviceCodeVerifyPageProps {
  /** The SPA auth session cookie is sent automatically — this flag indicates whether
   *  the user is currently authenticated. If not, the page redirects to login. */
  isAuthenticated: boolean
  onNavigateToLogin: () => void
}

/**
 * Device-Code Verification Page — served at /auth/device-code/verify.
 *
 * Flow:
 * 1. Reads ?code= query param to pre-fill the user code input.
 * 2. If not authenticated, redirects to login.
 * 3. On submit: POST /api/auth/device-code/verify with { userCode }.
 * 4. On 200: show success screen.
 * 5. On 400/404/410: show error message, keep form visible.
 */
export function DeviceCodeVerifyPage({ isAuthenticated, onNavigateToLogin }: DeviceCodeVerifyPageProps) {
  // Pre-fill from ?code= query param
  const [userCode, setUserCode] = useState<string>(() => {
    const params = new URLSearchParams(window.location.search)
    return params.get('code') ?? ''
  })
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState(false)

  // Redirect to login if not authenticated
  useEffect(() => {
    if (!isAuthenticated) {
      onNavigateToLogin()
    }
  }, [isAuthenticated, onNavigateToLogin])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    const code = userCode.trim()
    if (!code) return

    setLoading(true)
    setError(null)
    try {
      const res = await fetch('/api/auth/device-code/verify', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ userCode: code }),
        credentials: 'include',
      })
      if (res.ok) {
        setSuccess(true)
      } else if (res.status === 400 || res.status === 404 || res.status === 410) {
        setError('Invalid or expired code. Please try again.')
      } else {
        setError(`Unexpected error (${res.status}). Please try again.`)
      }
    } catch {
      setError('Network error. Please try again.')
    } finally {
      setLoading(false)
    }
  }

  if (success) {
    return (
      <div
        data-testid="device-code-success"
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
        <div style={{ width: '100%', maxWidth: 400, textAlign: 'center' }}>
          <div style={{ fontSize: 52, marginBottom: 16 }}>✓</div>
          <div style={{ fontSize: 22, fontWeight: 700, color: 'var(--text)', marginBottom: 10 }}>
            Authentication successful!
          </div>
          <div style={{ color: 'var(--text2)', fontSize: 15 }}>
            You can close this tab. The CLI is now authorized.
          </div>
        </div>
      </div>
    )
  }

  return (
    <div
      data-testid="device-code-verify-page"
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
      <div style={{ width: '100%', maxWidth: 400 }}>
        <div style={{ textAlign: 'center', marginBottom: 32 }}>
          <div style={{ fontSize: 52, marginBottom: 12 }}>⚡</div>
          <div style={{ fontSize: 22, fontWeight: 700, color: 'var(--text)', marginBottom: 8 }}>
            Authorize CLI
          </div>
          <div style={{ color: 'var(--text2)', fontSize: 14 }}>
            Enter the code shown in your terminal to authorize the MClaude CLI.
          </div>
        </div>

        <form onSubmit={handleSubmit} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <input
            data-testid="device-code-input"
            type="text"
            placeholder="XXXXXXXX"
            value={userCode}
            onChange={e => setUserCode(e.target.value.toUpperCase())}
            autoFocus
            autoComplete="off"
            spellCheck={false}
            style={{
              padding: '14px 18px',
              background: 'var(--surf)',
              border: '1px solid var(--border)',
              borderRadius: 12,
              color: 'var(--text)',
              WebkitTextFillColor: 'var(--text)',
              WebkitAppearance: 'none',
              fontSize: 22,
              fontFamily: "'Menlo','Courier New',monospace",
              letterSpacing: '0.15em',
              textAlign: 'center',
              width: '100%',
            }}
          />
          {error && (
            <div
              data-testid="device-code-error"
              style={{ color: 'var(--red)', fontSize: 13, textAlign: 'center' }}
            >
              {error}
            </div>
          )}
          <button
            data-testid="device-code-verify-button"
            type="submit"
            disabled={loading || !userCode.trim()}
            style={{
              padding: '13px 0',
              background: 'var(--blue)',
              color: '#fff',
              borderRadius: 12,
              fontSize: 16,
              fontWeight: 600,
              opacity: loading || !userCode.trim() ? 0.6 : 1,
              transition: 'opacity 0.15s',
            }}
          >
            {loading ? 'Verifying…' : 'Approve'}
          </button>
        </form>
      </div>
    </div>
  )
}
