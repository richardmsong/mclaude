import { useState, useEffect, useCallback } from 'react'
import { NavBar } from './NavBar'
import { StatusDot } from './StatusDot'
import { ConnectSheet } from './ConnectSheet'
import type { ConnectedProvider, AdminProvider, SessionKVState, ProjectKVState } from '@/types'
import type { AuthClient } from '@/transport/auth-client'
import type { INATSClient } from '@/types'
import type { UserInfoStore } from '@/stores/user-info-store'
import { revokeTokenOnAllHosts, revokeTokenOnHost, deliverToken } from '@/stores/token-delivery-service'

interface SettingsProps {
  userId: string
  serverUrl: string
  connected: boolean
  sessionCount: number
  onBack: () => void
  onLogout: () => void
  onCacheReset?: () => void
  authClient?: AuthClient
  /** All sessions from SessionStore, used to build the CONNECTED HOSTS section. */
  sessions?: Map<string, SessionKVState>
  /** All projects from SessionStore, used to label the CONNECTED HOSTS section. */
  projects?: Map<string, ProjectKVState>
  /** Current user's role, decoded from JWT payload. Shows ADMINISTRATION section when 'admin'. */
  role?: string
  /** Navigate to a hash route (e.g. 'users'). */
  onNavigate?: (hash: string) => void
  // ── ADR-0092: Claude token management ──────────────────────────────────────
  /** User slug, needed for token delivery subjects. */
  userSlug?: string
  /** NATS client, needed for token delivery and revoke. */
  nc?: INATSClient
  /** User info store — provides token status and write/delete methods. */
  userInfoStore?: UserInfoStore
  /** Called after a new token is obtained (re-delivery to all hosts). */
  onTokenReconnected?: (token: string) => Promise<void>
}

// ── PAT form ─────────────────────────────────────────────────────────────────

interface PATFormProps {
  onSubmit: (baseUrl: string, displayName: string, token: string) => Promise<void>
  onCancel: () => void
}

function PATForm({ onSubmit, onCancel }: PATFormProps) {
  const [baseUrl, setBaseUrl] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [token, setToken] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!baseUrl.trim() || !displayName.trim() || !token.trim()) return
    setSubmitting(true)
    setError('')
    try {
      await onSubmit(baseUrl.trim(), displayName.trim(), token.trim())
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add provider')
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} style={{ padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 10, borderTop: '1px solid var(--border)' }}>
      <div style={{ fontSize: 12, color: 'var(--text2)', fontWeight: 600 }}>Add Provider with PAT</div>
      <input
        type="url"
        placeholder="Base URL (e.g. https://github.acme.com)"
        value={baseUrl}
        onChange={e => setBaseUrl(e.target.value)}
        style={inputStyle}
        required
      />
      <input
        type="text"
        placeholder="Display Name (e.g. ACME GitHub)"
        value={displayName}
        onChange={e => setDisplayName(e.target.value)}
        style={inputStyle}
        required
      />
      <input
        type="password"
        placeholder="Personal Access Token"
        value={token}
        onChange={e => setToken(e.target.value)}
        style={inputStyle}
        required
      />
      {error && <div style={{ color: 'var(--red)', fontSize: 12 }}>{error}</div>}
      <div style={{ display: 'flex', gap: 8 }}>
        <button
          type="submit"
          disabled={submitting || !baseUrl.trim() || !displayName.trim() || !token.trim()}
          style={{ ...btnBase, background: 'var(--blue)', color: '#fff', flex: 1, opacity: submitting ? 0.6 : 1 }}
        >
          {submitting ? 'Adding…' : 'Add'}
        </button>
        <button type="button" onClick={onCancel} style={{ ...btnBase, background: 'var(--surf2)', color: 'var(--text2)', flex: 1 }}>
          Cancel
        </button>
      </div>
    </form>
  )
}

// ── Git Providers section ─────────────────────────────────────────────────────

interface GitProvidersSectionProps {
  authClient: AuthClient
}

function GitProvidersSection({ authClient }: GitProvidersSectionProps) {
  const [connectedProviders, setConnectedProviders] = useState<ConnectedProvider[]>([])
  const [adminProviders, setAdminProviders] = useState<AdminProvider[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showPATForm, setShowPATForm] = useState(false)
  const [disconnecting, setDisconnecting] = useState<string | null>(null)
  const [connectingProvider, setConnectingProvider] = useState<string | null>(null)
  const [toast, setToast] = useState<string | null>(null)

  const showToast = (msg: string) => {
    setToast(msg)
    setTimeout(() => setToast(null), 3000)
  }

  const loadProviders = useCallback(async () => {
    setLoading(true)
    try {
      const [me, admins] = await Promise.all([
        authClient.getMe(),
        authClient.getAdminProviders(),
      ])
      setConnectedProviders(me.connectedProviders)
      setAdminProviders(admins)
      setError(null)
    } catch (err) {
      console.error('loadProviders failed:', err)
      setError('Failed to load providers')
    } finally {
      setLoading(false)
    }
  }, [authClient])

  useEffect(() => {
    void loadProviders()
  }, [loadProviders])

  const handleDisconnect = async (conn: ConnectedProvider) => {
    const confirmed = window.confirm(
      `Existing projects will keep their cloned repos but won't be able to fetch updates. Disconnect ${conn.displayName} (@${conn.username})?`
    )
    if (!confirmed) return
    setDisconnecting(conn.connectionId)
    try {
      await authClient.disconnectConnection(conn.connectionId)
      showToast(`Disconnected @${conn.username}`)
      await loadProviders()
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to disconnect')
    } finally {
      setDisconnecting(null)
    }
  }

  const handleConnect = async (provider: AdminProvider) => {
    setConnectingProvider(provider.id)
    try {
      const returnUrl = `/?provider=${encodeURIComponent(provider.id)}&connected=true&goto=settings`
      const redirectUrl = await authClient.startOAuthConnect(provider.id, returnUrl)
      window.location.href = redirectUrl
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to start connect flow')
      setConnectingProvider(null)
    }
  }

  const handleAddPAT = async (baseUrl: string, displayName: string, token: string) => {
    await authClient.addPAT(baseUrl, displayName, token)
    setShowPATForm(false)
    showToast(`Added ${displayName}`)
    await loadProviders()
  }

  if (loading) {
    return (
      <div style={{ color: 'var(--text3)', fontSize: 13, padding: '12px 14px' }}>
        Loading providers…
      </div>
    )
  }

  // Group connected providers by display name
  const grouped: Record<string, ConnectedProvider[]> = {}
  for (const p of connectedProviders) {
    const key = p.displayName
    if (!grouped[key]) grouped[key] = []
    grouped[key]!.push(p)
  }

  // Build a map of which admin providers have connections (for showing "Connect" button)
  const connectedAdminIds = new Set(
    connectedProviders
      .filter(p => p.authType === 'oauth')
      .map(p => p.providerId)
  )

  return (
    <div>
      {toast && (
        <div style={{
          position: 'fixed', top: 60, left: '50%', transform: 'translateX(-50%)',
          background: 'var(--surf2)', border: '1px solid var(--border)', borderRadius: 8,
          padding: '8px 16px', fontSize: 13, color: 'var(--text)', zIndex: 9999,
          boxShadow: '0 2px 12px rgba(0,0,0,0.4)',
        }}>
          {toast}
        </div>
      )}

      <div style={card}>
        {/* Admin OAuth providers */}
        {adminProviders.map((provider, i) => {
          const connections = connectedProviders.filter(
            c => c.providerId === provider.id && c.authType === 'oauth'
          )
          return (
            <div key={provider.id}>
              {i > 0 && <div style={{ borderTop: '1px solid var(--border)' }} />}
              {/* Each connection for this admin provider */}
              {connections.map(conn => (
                <div key={conn.connectionId} style={row}>
                  <div>
                    <span style={{ color: 'var(--text)', fontSize: 14 }}>
                      {provider.displayName}: @{conn.username}
                    </span>
                    <span style={{
                      marginLeft: 8, fontSize: 11, padding: '2px 6px',
                      background: 'var(--surf3)', borderRadius: 4, color: 'var(--text2)',
                    }}>OAuth</span>
                  </div>
                  <button
                    onClick={() => void handleDisconnect(conn)}
                    disabled={disconnecting === conn.connectionId}
                    style={{ ...btnBase, color: 'var(--red)', fontSize: 13 }}
                  >
                    {disconnecting === conn.connectionId ? '…' : 'Disconnect'}
                  </button>
                </div>
              ))}

              {/* "Connect" / "Connect again" button — always shown for admin providers */}
              <div style={{ ...row, borderTop: connections.length > 0 ? '1px solid var(--border)' : undefined }}>
                <span style={{ color: 'var(--text2)', fontSize: 13 }}>
                  {connectedAdminIds.has(provider.id)
                    ? `Add another ${provider.displayName} account`
                    : `Connect ${provider.displayName}`}
                </span>
                <button
                  onClick={() => void handleConnect(provider)}
                  disabled={connectingProvider === provider.id}
                  style={{ ...btnBase, color: 'var(--blue)', fontSize: 13 }}
                >
                  {connectingProvider === provider.id ? '…' : `Connect ${provider.displayName}`}
                </button>
              </div>
            </div>
          )
        })}

        {/* PAT connections */}
        {connectedProviders.filter(c => c.authType === 'pat').map((conn) => (
          <div key={conn.connectionId} style={{ ...row, borderTop: '1px solid var(--border)' }}>
            <div>
              <span style={{ color: 'var(--text)', fontSize: 14 }}>
                {conn.displayName}: @{conn.username}
              </span>
              <span style={{
                marginLeft: 8, fontSize: 11, padding: '2px 6px',
                background: 'var(--surf3)', borderRadius: 4, color: 'var(--text2)',
              }}>PAT</span>
            </div>
            <button
              onClick={() => void handleDisconnect(conn)}
              disabled={disconnecting === conn.connectionId}
              style={{ ...btnBase, color: 'var(--red)', fontSize: 13 }}
            >
              {disconnecting === conn.connectionId ? '…' : 'Remove'}
            </button>
          </div>
        ))}

        {/* Error state or empty state when no providers connected and no admin providers */}
        {error ? (
          <div style={{ ...row, justifyContent: 'flex-start' }}>
            <span style={{ color: 'var(--red)', fontSize: 13 }}>{error}</span>
          </div>
        ) : adminProviders.length === 0 && connectedProviders.length === 0 && (
          <div style={{ ...row, justifyContent: 'flex-start' }}>
            <span style={{ color: 'var(--text3)', fontSize: 13 }}>
              No providers configured. Ask your admin to configure OAuth providers.
            </span>
          </div>
        )}

        {/* PAT form */}
        {showPATForm ? (
          <PATForm
            onSubmit={handleAddPAT}
            onCancel={() => setShowPATForm(false)}
          />
        ) : (
          <div style={{ borderTop: adminProviders.length > 0 || connectedProviders.length > 0 ? '1px solid var(--border)' : undefined }}>
            <button
              onClick={() => setShowPATForm(true)}
              style={{ ...row, width: '100%', background: 'none', textAlign: 'left', color: 'var(--blue)', fontSize: 13 }}
            >
              + Add provider with PAT
            </button>
          </div>
        )}
      </div>
    </div>
  )
}

// ── Claude Account section ───────────────────────────────────────────────────

interface ClaudeAccountSectionProps {
  userInfoStore: UserInfoStore
  authClient: AuthClient
  nc: INATSClient
  userSlug: string
  sessions?: Map<string, SessionKVState>
  onTokenReconnected?: (token: string) => Promise<void>
}

function ClaudeAccountSection({ userInfoStore, authClient, nc, userSlug, sessions, onTokenReconnected }: ClaudeAccountSectionProps) {
  const [token, setToken] = useState<string | null>(userInfoStore.token)
  const [showConnectSheet, setShowConnectSheet] = useState(false)
  const [disconnecting, setDisconnecting] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  // Track token changes from KV
  useEffect(() => {
    setToken(userInfoStore.token)
    return userInfoStore.onTokenChanged(t => setToken(t))
  }, [userInfoStore])

  // "Token invalid" detection: any active session with status token_expired
  const hasTokenExpired = sessions
    ? Array.from(sessions.values()).some(s => s.state === 'token_expired')
    : false

  // Determine display state
  const claudeStatus: 'connected' | 'invalid' | 'not_connected' = !token
    ? 'not_connected'
    : hasTokenExpired
    ? 'invalid'
    : 'connected'

  const handleTokenObtained = async (newToken: string) => {
    setShowConnectSheet(false)
    await userInfoStore.writeToken(newToken)
    await onTokenReconnected?.(newToken)
  }

  const handleDisconnect = async () => {
    // Count active sessions for the confirmation message
    const activeCount = sessions ? Array.from(sessions.values()).filter(s =>
      s.state === 'running' || s.state === 'requires_action' || s.state === 'idle'
    ).length : 0
    const hostCount = sessions ? new Set(Array.from(sessions.values()).map(s => s.hostSlug).filter(Boolean)).size : 0

    const msg = activeCount > 0
      ? `Disconnect Claude account? All active sessions on ${hostCount} host${hostCount !== 1 ? 's' : ''} will stop.`
      : 'Disconnect Claude account?'

    if (!window.confirm(msg)) return

    setDisconnecting(true)
    setActionError(null)
    try {
      await userInfoStore.deleteToken()
      await revokeTokenOnAllHosts(nc, authClient, userSlug)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Disconnect failed')
    } finally {
      setDisconnecting(false)
    }
  }

  const statusDotColor = claudeStatus === 'connected' ? 'connected'
    : claudeStatus === 'invalid' ? 'disconnected'
    : 'disconnected'

  return (
    <>
      <div style={card}>
        <div style={row}>
          <span style={{ color: 'var(--text2)' }}>Status</span>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <StatusDot state={statusDotColor} size={8} />
            <span style={{ color: 'var(--text)', fontSize: 13 }}>
              {claudeStatus === 'connected' ? 'Connected' : claudeStatus === 'invalid' ? 'Token invalid' : 'Not connected'}
            </span>
          </div>
        </div>
        <div style={{ ...row, borderTop: '1px solid var(--border)', gap: 8, justifyContent: 'flex-end' }}>
          {claudeStatus === 'not_connected' ? (
            <button onClick={() => setShowConnectSheet(true)} style={{ ...btnBase, color: 'var(--blue)' }}>
              Connect
            </button>
          ) : (
            <>
              <button onClick={() => setShowConnectSheet(true)} style={{ ...btnBase, color: 'var(--blue)' }}>
                Reconnect
              </button>
              <button
                onClick={() => void handleDisconnect()}
                disabled={disconnecting}
                style={{ ...btnBase, color: 'var(--red)', opacity: disconnecting ? 0.5 : 1 }}
              >
                {disconnecting ? '…' : 'Disconnect'}
              </button>
            </>
          )}
        </div>
        {actionError && (
          <div style={{ ...row, color: 'var(--red)', fontSize: 13, justifyContent: 'flex-start', borderTop: '1px solid var(--border)' }}>
            {actionError}
          </div>
        )}
      </div>

      {showConnectSheet && (
        <ConnectSheet
          firstRun={false}
          onTokenObtained={(t) => void handleTokenObtained(t)}
          onDismiss={() => setShowConnectSheet(false)}
          authClient={authClient}
        />
      )}
    </>
  )
}

// ── Host detail CLAUDE ACCESS sub-section ─────────────────────────────────────

interface ClaudeAccessSectionProps {
  hslug: string
  uslug: string
  nc: INATSClient
  authClient: AuthClient
  userInfoStore: UserInfoStore
  /** Active sessions on this host, for impact count in confirm dialog. */
  hostSessionCount?: number
}

function ClaudeAccessSection({ hslug, uslug, nc, authClient, userInfoStore, hostSessionCount = 0 }: ClaudeAccessSectionProps) {
  const [accessState, setAccessState] = useState<'authorized' | 'revoked' | 'pending' | 'offline'>('authorized')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const handleRevoke = async () => {
    const msg = hostSessionCount > 0
      ? `Revoke Claude access on ${hslug}? ${hostSessionCount} active session${hostSessionCount !== 1 ? 's' : ''} will be stopped.`
      : `Revoke Claude access on ${hslug}?`
    if (!window.confirm(msg)) return

    setBusy(true)
    setError(null)
    const result = await revokeTokenOnHost(nc, uslug, hslug)
    setBusy(false)
    if (result.ok) {
      setAccessState('revoked')
    } else {
      setError(`Could not reach ${hslug} — token may still be active. Try again when the host is online.`)
    }
  }

  const handleReauthorize = async () => {
    const token = userInfoStore.token
    if (!token) {
      setError('No token to deliver. Connect your Claude account first.')
      return
    }
    setBusy(true)
    setError(null)
    const result = await deliverToken(nc, authClient, uslug, hslug, token)
    setBusy(false)
    if (result.ok) {
      setAccessState('authorized')
    } else {
      setError('Host is offline — try again later')
    }
  }

  const dotColor = accessState === 'authorized' ? 'connected' : 'disconnected'

  return (
    <div>
      <div style={{ ...sectionLabel, marginTop: 20 }}>CLAUDE ACCESS</div>
      <div style={card}>
        <div style={row}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <StatusDot state={dotColor} size={8} />
            <span style={{ color: 'var(--text)', fontSize: 14 }}>
              {accessState === 'authorized' ? 'Authorized'
                : accessState === 'revoked' ? 'Revoked'
                : accessState === 'pending' ? 'Pending delivery'
                : 'Host offline'}
            </span>
          </div>
          {accessState === 'authorized' && (
            <button
              onClick={() => void handleRevoke()}
              disabled={busy}
              style={{ ...btnBase, color: 'var(--red)', opacity: busy ? 0.5 : 1 }}
            >
              {busy ? '…' : 'Revoke'}
            </button>
          )}
          {accessState === 'revoked' && (
            <button
              onClick={() => void handleReauthorize()}
              disabled={busy}
              style={{ ...btnBase, color: 'var(--blue)', opacity: busy ? 0.5 : 1 }}
            >
              {busy ? '…' : 'Re-authorize'}
            </button>
          )}
        </div>
        {error && (
          <div style={{ ...row, color: 'var(--red)', fontSize: 13, justifyContent: 'flex-start', borderTop: '1px solid var(--border)' }}>
            {error}
          </div>
        )}
      </div>
    </div>
  )
}

// Suppress unused variable lint warning — ClaudeAccessSection exported for use in host detail screens
export { ClaudeAccessSection }

// ── Main Settings component ───────────────────────────────────────────────────

export function Settings({ userId, serverUrl, connected, sessionCount, onBack, onLogout, onCacheReset, authClient, sessions, projects, role, onNavigate, userSlug, nc, userInfoStore, onTokenReconnected }: SettingsProps) {
  const [inputMode, setInputMode] = useState<'text' | 'voice'>(() => {
    try {
      return (localStorage.getItem('mclaude.inputMode') === 'voice') ? 'voice' : 'text'
    } catch {
      return 'text'
    }
  })

  const handleInputModeChange = (mode: 'text' | 'voice') => {
    setInputMode(mode)
    try {
      localStorage.setItem('mclaude.inputMode', mode)
      // Dispatch storage event so SessionDetailScreen picks up the change
      window.dispatchEvent(new StorageEvent('storage', {
        key: 'mclaude.inputMode',
        newValue: mode,
        storageArea: localStorage,
      }))
    } catch {
      // ignore
    }
  }

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

        {/* CONNECTED HOSTS section */}
        {sessions && sessions.size > 0 && (() => {
          // Group sessions by projectId — one row per project showing name + count.
          const byProject = new Map<string, { name: string; count: number }>()
          for (const s of sessions.values()) {
            const existing = byProject.get(s.projectId)
            const name = projects?.get(s.projectId)?.name ?? s.projectId
            if (existing) {
              existing.count++
            } else {
              byProject.set(s.projectId, { name, count: 1 })
            }
          }
          const rows = Array.from(byProject.entries())
          return (
            <>
              <div style={{ ...sectionLabel, marginTop: 20 }}>CONNECTED HOSTS</div>
              <div style={card}>
                {rows.map(([projectId, { name, count }], i) => (
                  <div key={projectId} style={{ ...row, borderTop: i > 0 ? '1px solid var(--border)' : undefined }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                      <StatusDot state="connected" size={8} />
                      <span style={{ color: 'var(--text)', fontSize: 14 }}>{name}</span>
                    </div>
                    <span style={{ color: 'var(--text2)', fontSize: 13 }}>
                      {count} {count === 1 ? 'sess' : 'sess'}
                    </span>
                  </div>
                ))}
              </div>
            </>
          )
        })()}

        {/* CLAUDE ACCOUNT section — ADR-0092 */}
        {userInfoStore && authClient && nc && userSlug && (
          <>
            <div style={{ ...sectionLabel, marginTop: 20 }}>CLAUDE ACCOUNT</div>
            <ClaudeAccountSection
              userInfoStore={userInfoStore}
              authClient={authClient}
              nc={nc}
              userSlug={userSlug}
              sessions={sessions}
              onTokenReconnected={onTokenReconnected}
            />
          </>
        )}

        {/* ADMINISTRATION section — admin only */}
        {role === 'admin' && (
          <>
            <div style={{ ...sectionLabel, marginTop: 20 }}>ADMINISTRATION</div>
            <div style={card}>
              <button
                onClick={() => onNavigate?.('users')}
                style={{
                  ...row,
                  width: '100%',
                  background: 'none',
                  textAlign: 'left',
                  color: 'var(--text)',
                  cursor: 'pointer',
                }}
              >
                <span style={{ color: 'var(--text2)' }}>User Management</span>
                <span style={{ color: 'var(--text3)', fontSize: 16 }}>›</span>
              </button>
            </div>
          </>
        )}

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

        {/* INPUT section */}
        <div style={{ ...sectionLabel, marginTop: 20 }}>INPUT</div>
        <div style={card}>
          <div style={row}>
            <span style={{ color: 'var(--text2)' }}>Default method</span>
            <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
              <label style={{ display: 'flex', alignItems: 'center', gap: 5, cursor: 'pointer', fontSize: 14, color: 'var(--text)' }}>
                <input
                  type="radio"
                  name="inputMode"
                  value="text"
                  checked={inputMode === 'text'}
                  onChange={() => handleInputModeChange('text')}
                  style={{ accentColor: 'var(--blue)' }}
                />
                Text
              </label>
              <label style={{ display: 'flex', alignItems: 'center', gap: 5, cursor: 'pointer', fontSize: 14, color: 'var(--text)' }}>
                <input
                  type="radio"
                  name="inputMode"
                  value="voice"
                  checked={inputMode === 'voice'}
                  onChange={() => handleInputModeChange('voice')}
                  style={{ accentColor: 'var(--blue)' }}
                />
                Voice
              </label>
            </div>
          </div>
        </div>

        {/* GIT PROVIDERS section */}
        {authClient && (
          <>
            <div style={{ ...sectionLabel, marginTop: 20 }}>GIT PROVIDERS</div>
            <GitProvidersSection authClient={authClient} />
          </>
        )}

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

const btnBase: React.CSSProperties = {
  background: 'none',
  border: 'none',
  cursor: 'pointer',
  padding: '4px 8px',
  borderRadius: 6,
  fontWeight: 500,
}

const inputStyle: React.CSSProperties = {
  width: '100%',
  padding: '9px 12px',
  background: 'var(--surf2)',
  border: '1px solid var(--border)',
  borderRadius: 8,
  color: 'var(--text)',
  fontSize: 14,
}
