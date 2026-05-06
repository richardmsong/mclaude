/**
 * ADR-0092: ConnectSheet — bottom sheet for connecting a Claude account.
 *
 * Reused from:
 *  - First-run gate (auto-opened, [✕] disabled)
 *  - Settings → [Reconnect] / [Connect] (dismissible)
 *  - Session banner → [Reconnect] (dismissible)
 *
 * OAuth PKCE flow:
 *  1. Generate code_verifier (32 random bytes, base64url), code_challenge (SHA-256 S256), state.
 *  2. Open popup to claude.ai/oauth/authorize.
 *  3. Popup redirects to /auth/oauth/callback (static HTML), which posts
 *     { type: 'mclaude:oauth-callback', code, state } via postMessage.
 *  4. Parent validates origin + state, exchanges code via CP proxy.
 *  5. Writes access token to KV via onTokenObtained().
 *
 * Manual paste:
 *  - Write-only password input + [Connect] button.
 *  - Calls onTokenObtained(token) on submit; clears input immediately.
 */

import { useEffect, useRef, useState } from 'react'
import type { AuthClient } from '@/transport/auth-client'

const OAUTH_CLIENT_ID = 'mclaude-cli'
const CLAUDE_OAUTH_URL = 'https://claude.ai/oauth/authorize'

interface ConnectSheetProps {
  /** If true, the [✕] close button is disabled (first-run — token is mandatory). */
  firstRun?: boolean
  /** Called when a token has been successfully obtained (OAuth or paste). */
  onTokenObtained: (token: string) => void
  /** Called when the sheet is dismissed (only allowed when firstRun=false). */
  onDismiss?: () => void
  authClient: AuthClient
}

// ─── PKCE helpers ─────────────────────────────────────────────────────────────

function generateRandomBase64url(byteCount: number): string {
  const bytes = crypto.getRandomValues(new Uint8Array(byteCount))
  return btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '')
}

async function sha256Base64url(input: string): Promise<string> {
  const encoded = new TextEncoder().encode(input)
  const hashBuffer = await crypto.subtle.digest('SHA-256', encoded)
  const hashArray = Array.from(new Uint8Array(hashBuffer))
  return btoa(String.fromCharCode(...hashArray))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '')
}

// ─── ConnectSheet ─────────────────────────────────────────────────────────────

type SheetState = 'idle' | 'oauth-pending' | 'success' | 'error'

export function ConnectSheet({ firstRun = false, onTokenObtained, onDismiss, authClient }: ConnectSheetProps) {
  const [state, setState] = useState<SheetState>('idle')
  const [error, setError] = useState<string | null>(null)
  const [pasteToken, setPasteToken] = useState('')
  const [pasteError, setPasteError] = useState<string | null>(null)
  const [pasteSubmitting, setPasteSubmitting] = useState(false)

  // PKCE values stored in parent memory (not localStorage — they must not leave the parent window)
  const codeVerifierRef = useRef<string | null>(null)
  const expectedStateRef = useRef<string | null>(null)

  // postMessage listener for OAuth callback
  useEffect(() => {
    const handleMessage = async (event: MessageEvent) => {
      // Validate origin — must be same origin as the SPA
      if (event.origin !== window.location.origin) return

      const data = event.data as {
        type?: string
        code?: string | null
        state?: string | null
        error?: string | null
        errorDescription?: string | null
      }

      if (data?.type !== 'mclaude:oauth-callback') return

      if (data.error) {
        setState('error')
        setError('Sign-in failed — try again or enter a token manually')
        return
      }

      // CSRF check — validate state matches what we generated
      if (!data.state || data.state !== expectedStateRef.current) {
        setState('error')
        setError('Sign-in failed — state mismatch. Try again.')
        return
      }

      if (!data.code || !codeVerifierRef.current) {
        setState('error')
        setError('Sign-in failed — missing authorization code. Try again.')
        return
      }

      // Exchange code for token via CP proxy
      try {
        const token = await authClient.exchangeOAuthCode({
          code: data.code,
          code_verifier: codeVerifierRef.current,
          redirect_uri: `${window.location.origin}/auth/oauth/callback`,
          client_id: OAUTH_CLIENT_ID,
        })
        setState('success')
        onTokenObtained(token)
        // Spec (ADR-0092, spec-web.md): auto-dismiss after 1 second on success.
        setTimeout(() => onDismiss?.(), 1000)
      } catch (err) {
        setState('error')
        setError('Sign-in failed — try again or enter a token manually')
        console.error('[ConnectSheet] OAuth exchange failed:', err)
      }
    }

    window.addEventListener('message', handleMessage)
    return () => window.removeEventListener('message', handleMessage)
  }, [authClient, onTokenObtained])

  const handleOAuthClick = async () => {
    setState('oauth-pending')
    setError(null)

    // Generate PKCE params — store in parent's JS memory only
    const codeVerifier = generateRandomBase64url(32)
    const codeChallenge = await sha256Base64url(codeVerifier)
    const state = generateRandomBase64url(32)
    codeVerifierRef.current = codeVerifier
    expectedStateRef.current = state

    const redirectUri = `${window.location.origin}/auth/oauth/callback`
    const authUrl = new URL(CLAUDE_OAUTH_URL)
    authUrl.searchParams.set('response_type', 'code')
    authUrl.searchParams.set('client_id', OAUTH_CLIENT_ID)
    authUrl.searchParams.set('redirect_uri', redirectUri)
    authUrl.searchParams.set('code_challenge', codeChallenge)
    authUrl.searchParams.set('code_challenge_method', 'S256')
    authUrl.searchParams.set('state', state)
    authUrl.searchParams.set('scope', 'user:inference')

    const popup = window.open(authUrl.toString(), '_blank', 'width=500,height=700,menubar=no,toolbar=no,status=no')

    if (!popup || popup.closed) {
      setState('error')
      setError('Allow popups for this site and try again')
      return
    }

    // Detect popup blocked (some browsers return a window that immediately closes)
    setTimeout(() => {
      if (popup.closed && state === 'oauth-pending') {
        setState('error')
        setError('Allow popups for this site and try again')
      }
    }, 500)
  }

  const handlePasteSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    const trimmed = pasteToken.trim()
    if (!trimmed) {
      setPasteError('Please enter a valid token')
      return
    }
    setPasteSubmitting(true)
    setPasteError(null)
    setPasteToken('') // clear immediately
    try {
      onTokenObtained(trimmed)
      setState('success')
      // Spec (ADR-0092, spec-web.md): auto-dismiss after 1 second on success.
      setTimeout(() => onDismiss?.(), 1000)
    } catch (err) {
      setPasteError(err instanceof Error ? err.message : 'Failed to save token')
      setPasteSubmitting(false)
    }
  }

  const canDismiss = !firstRun && state !== 'oauth-pending'

  return (
    <>
      {/* Scrim */}
      <div
        onClick={canDismiss ? onDismiss : undefined}
        style={{
          position: 'fixed', inset: 0, zIndex: 9000,
          background: 'rgba(0,0,0,0.5)',
          cursor: canDismiss ? 'pointer' : 'default',
        }}
      />

      {/* Sheet */}
      <div
        role="dialog"
        aria-label="Connect Claude Account"
        style={{
          position: 'fixed', bottom: 0, left: 0, right: 0, zIndex: 9001,
          background: 'var(--surf)',
          borderTop: '1px solid var(--border)',
          borderRadius: '16px 16px 0 0',
          padding: '20px 20px 32px',
          maxWidth: 480,
          margin: '0 auto',
        }}
      >
        {/* Header */}
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
          <span style={{ fontSize: 17, fontWeight: 600, color: 'var(--text)' }}>
            Connect Claude Account
          </span>
          <button
            onClick={canDismiss ? onDismiss : undefined}
            disabled={!canDismiss}
            aria-label="Close"
            style={{
              background: 'none', border: 'none', cursor: canDismiss ? 'pointer' : 'default',
              fontSize: 20, color: canDismiss ? 'var(--text2)' : 'var(--text3)',
              padding: '4px 8px',
              opacity: !canDismiss ? 0.3 : 1,
            }}
          >
            ✕
          </button>
        </div>

        {/* Description */}
        <p style={{ fontSize: 14, color: 'var(--text2)', marginBottom: 20, lineHeight: 1.5 }}>
          MClaude uses your Claude account to power coding sessions.
        </p>

        {state === 'success' ? (
          <div style={{ textAlign: 'center', padding: '16px 0', color: 'var(--green)', fontSize: 15, fontWeight: 500 }}>
            Connected
          </div>
        ) : (
          <>
            {/* OAuth button */}
            <button
              onClick={() => void handleOAuthClick()}
              disabled={state === 'oauth-pending'}
              style={{
                width: '100%',
                padding: '14px',
                background: 'var(--blue)',
                color: '#fff',
                border: 'none',
                borderRadius: 12,
                fontSize: 15,
                fontWeight: 600,
                cursor: state === 'oauth-pending' ? 'default' : 'pointer',
                opacity: state === 'oauth-pending' ? 0.7 : 1,
                marginBottom: 8,
              }}
            >
              {state === 'oauth-pending' ? 'Waiting for sign-in…' : 'Sign in with Claude'}
            </button>

            {/* OAuth error */}
            {error && (
              <div style={{ color: 'var(--red)', fontSize: 13, marginBottom: 8 }}>
                {error}
              </div>
            )}

            {/* Manual paste — collapsed <details> */}
            <details style={{ marginTop: 12 }}>
              <summary style={{
                fontSize: 13, color: 'var(--text2)', cursor: 'pointer',
                listStyle: 'none', display: 'flex', alignItems: 'center', gap: 4,
              }}>
                Or enter a token manually ▶
              </summary>
              <form onSubmit={(e) => void handlePasteSubmit(e)} style={{ marginTop: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>
                <input
                  type="password"
                  value={pasteToken}
                  onChange={e => setPasteToken(e.target.value)}
                  placeholder="sk-ant-oat01-…"
                  autoComplete="off"
                  style={{
                    width: '100%',
                    padding: '10px 12px',
                    background: 'var(--surf2)',
                    border: '1px solid var(--border)',
                    borderRadius: 8,
                    color: 'var(--text)',
                    fontSize: 14,
                    boxSizing: 'border-box',
                  }}
                />
                {pasteError && (
                  <div style={{ color: 'var(--red)', fontSize: 13 }}>{pasteError}</div>
                )}
                <button
                  type="submit"
                  disabled={pasteSubmitting || !pasteToken.trim()}
                  style={{
                    padding: '10px',
                    background: 'var(--surf2)',
                    border: '1px solid var(--border)',
                    borderRadius: 8,
                    color: 'var(--text)',
                    fontSize: 14,
                    fontWeight: 500,
                    cursor: pasteSubmitting || !pasteToken.trim() ? 'default' : 'pointer',
                    opacity: pasteSubmitting || !pasteToken.trim() ? 0.6 : 1,
                  }}
                >
                  {pasteSubmitting ? 'Connecting…' : 'Connect'}
                </button>
              </form>
            </details>
          </>
        )}
      </div>
    </>
  )
}
