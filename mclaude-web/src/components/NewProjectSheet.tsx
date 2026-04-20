import { useState, useEffect, useRef, useCallback } from 'react'
import { slugify } from '@/lib/slug'
import type { SessionListVM } from '@/viewmodels/session-list-vm'
import type { ConnectedProvider, AdminProvider, Repo } from '@/types'
import type { AuthClient } from '@/transport/auth-client'

interface NewProjectSheetProps {
  sessionListVM: SessionListVM
  onClose: () => void
  onCreated?: (projectId: string) => void
  authClient?: AuthClient
}

// ── Repo picker ───────────────────────────────────────────────────────────────

interface RepoPickerProps {
  connections: ConnectedProvider[]
  authClient: AuthClient
  onSelect: (repo: Repo, connectionId: string) => void
}

function RepoPicker({ connections, authClient, onSelect }: RepoPickerProps) {
  const [selectedConnectionId, setSelectedConnectionId] = useState(connections[0]?.connectionId ?? '')
  const [query, setQuery] = useState('')
  const [repos, setRepos] = useState<Repo[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [open, setOpen] = useState(false)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const fetchRepos = useCallback(async (connId: string, q: string) => {
    if (!connId) return
    setLoading(true)
    setError('')
    try {
      const result = await authClient.getConnectionRepos(connId, q)
      setRepos(result.repos)
      setOpen(result.repos.length > 0)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load repos')
      setRepos([])
    } finally {
      setLoading(false)
    }
  }, [authClient])

  // Auto-load on mount (first page, empty query)
  useEffect(() => {
    if (selectedConnectionId) {
      void fetchRepos(selectedConnectionId, '')
    }
  }, [selectedConnectionId, fetchRepos])

  const handleQueryChange = (q: string) => {
    setQuery(q)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      void fetchRepos(selectedConnectionId, q)
    }, 300)
  }

  const selectedConn = connections.find(c => c.connectionId === selectedConnectionId)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {/* Identity selector (shown when multiple identities) */}
      {connections.length > 1 && (
        <select
          value={selectedConnectionId}
          onChange={e => setSelectedConnectionId(e.target.value)}
          style={inputStyle}
        >
          {connections.map(c => {
            const host = new URL(c.baseUrl).hostname
            return (
              <option key={c.connectionId} value={c.connectionId}>
                Browse as @{c.username} on {host}
              </option>
            )
          })}
        </select>
      )}
      {connections.length === 1 && selectedConn && (
        <div style={{ fontSize: 12, color: 'var(--text2)' }}>
          Browse as @{selectedConn.username} on {new URL(selectedConn.baseUrl).hostname}
        </div>
      )}

      {/* Search input */}
      <div style={{ position: 'relative' }}>
        <input
          type="text"
          placeholder="Search repositories…"
          value={query}
          onChange={e => handleQueryChange(e.target.value)}
          style={inputStyle}
          onFocus={() => repos.length > 0 && setOpen(true)}
        />
        {loading && (
          <span style={{ position: 'absolute', right: 12, top: '50%', transform: 'translateY(-50%)', color: 'var(--text3)', fontSize: 12 }}>
            …
          </span>
        )}

        {/* Dropdown */}
        {open && repos.length > 0 && (
          <div style={{
            position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 100,
            background: 'var(--surf)', border: '1px solid var(--border)', borderRadius: 10,
            marginTop: 4, maxHeight: 220, overflowY: 'auto',
            boxShadow: '0 4px 16px rgba(0,0,0,0.4)',
          }}>
            {repos.map(repo => (
              <button
                key={repo.fullName}
                onClick={() => {
                  onSelect(repo, selectedConnectionId)
                  setOpen(false)
                  setQuery(repo.fullName)
                }}
                style={{
                  width: '100%', textAlign: 'left', padding: '10px 14px',
                  background: 'none', borderBottom: '1px solid var(--border)',
                  display: 'flex', flexDirection: 'column', gap: 2, cursor: 'pointer',
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                  <span style={{ color: 'var(--text)', fontSize: 14, fontWeight: 500 }}>{repo.fullName}</span>
                  {repo.private && (
                    <span style={{
                      fontSize: 10, padding: '1px 5px', background: 'var(--surf2)',
                      borderRadius: 4, color: 'var(--text3)',
                    }}>private</span>
                  )}
                </div>
                {repo.description && (
                  <span style={{ color: 'var(--text2)', fontSize: 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {repo.description}
                  </span>
                )}
              </button>
            ))}
          </div>
        )}
      </div>

      {error && <div style={{ color: 'var(--red)', fontSize: 12 }}>{error}</div>}
    </div>
  )
}

// ── Connect provider buttons (no providers connected, admin providers exist) ──

interface ConnectProvidersProps {
  adminProviders: AdminProvider[]
  authClient: AuthClient
}

function ConnectProviderButtons({ adminProviders, authClient }: ConnectProvidersProps) {
  const [connecting, setConnecting] = useState<string | null>(null)
  const [error, setError] = useState('')

  const handleConnect = async (provider: AdminProvider) => {
    setConnecting(provider.id)
    setError('')
    try {
      const returnUrl = `/?provider=${encodeURIComponent(provider.id)}&connected=true&goto=new-project`
      const redirectUrl = await authClient.startOAuthConnect(provider.id, returnUrl)
      window.location.href = redirectUrl
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to start connect flow')
      setConnecting(null)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {adminProviders.map(provider => (
        <button
          key={provider.id}
          onClick={() => void handleConnect(provider)}
          disabled={connecting === provider.id}
          style={{
            padding: '10px 14px',
            background: 'var(--surf2)',
            border: '1px solid var(--border)',
            borderRadius: 10,
            color: connecting === provider.id ? 'var(--text2)' : 'var(--blue)',
            fontSize: 14,
            fontWeight: 500,
            textAlign: 'left',
          }}
        >
          {connecting === provider.id ? 'Redirecting…' : `Connect ${provider.displayName}`}
        </button>
      ))}
      {error && <div style={{ color: 'var(--red)', fontSize: 12 }}>{error}</div>}
    </div>
  )
}

// ── Main NewProjectSheet ──────────────────────────────────────────────────────

export function NewProjectSheet({ sessionListVM, onClose, onCreated, authClient }: NewProjectSheetProps) {
  const [name, setName] = useState('')
  const [gitUrl, setGitUrl] = useState('')
  const [gitIdentityId, setGitIdentityId] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')

  // Provider state
  const [connectedProviders, setConnectedProviders] = useState<ConnectedProvider[]>([])
  const [adminProviders, setAdminProviders] = useState<AdminProvider[]>([])
  const [providersLoading, setProvidersLoading] = useState(false)

  // Load provider info if authClient is provided
  useEffect(() => {
    if (!authClient) return
    setProvidersLoading(true)
    Promise.all([
      authClient.getMe().catch(() => ({ connectedProviders: [] as ConnectedProvider[] })),
      authClient.getAdminProviders().catch(() => [] as AdminProvider[]),
    ]).then(([me, admins]) => {
      setConnectedProviders((me as { connectedProviders: ConnectedProvider[] }).connectedProviders ?? [])
      setAdminProviders(admins)
    }).catch(() => {
      // silently ignore
    }).finally(() => {
      setProvidersLoading(false)
    })
  }, [authClient])

  const handleRepoSelect = (repo: Repo, connectionId: string) => {
    setGitUrl(repo.cloneUrl)
    setGitIdentityId(connectionId)
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) return
    setCreating(true)
    setError('')
    try {
      const projectId = await sessionListVM.createProject(
        name.trim(),
        gitUrl.trim() || undefined,
        gitIdentityId ?? undefined,
      )
      onCreated?.(projectId)
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create project')
      setCreating(false)
    }
  }

  const hasConnectedProviders = connectedProviders.length > 0
  const hasAdminProviders = adminProviders.length > 0

  return (
    <div
      style={{
        position: 'fixed', inset: 0, zIndex: 200,
        display: 'flex', flexDirection: 'column', justifyContent: 'flex-end',
      }}
      onClick={onClose}
    >
      <div style={{ position: 'absolute', inset: 0, background: 'rgba(0,0,0,0.5)' }} />

      <div
        style={{
          position: 'relative',
          background: 'var(--surf)',
          borderRadius: '16px 16px 0 0',
          display: 'flex',
          flexDirection: 'column',
          maxHeight: '90vh',
          overflowY: 'auto',
        }}
        onClick={e => e.stopPropagation()}
      >
        {/* Header */}
        <div style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: '16px 16px 12px',
          borderBottom: '1px solid var(--border)',
        }}>
          <div style={{ fontWeight: 600, fontSize: 16 }}>New Project</div>
          <button onClick={onClose} style={{ color: 'var(--text2)', fontSize: 18 }}>✕</button>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div>
            <div style={fieldLabel}>Name</div>
            <input
              autoFocus
              type="text"
              placeholder="My Project"
              value={name}
              onChange={e => setName(e.target.value)}
              style={inputStyle}
            />
            {name.trim() && (
              <div style={{
                marginTop: 6,
                fontSize: 12,
                color: 'var(--text3)',
                fontFamily: 'monospace',
              }}>
                saved as: <span style={{ color: 'var(--text2)' }}>{slugify(name.trim()) || '—'}</span>
              </div>
            )}
          </div>

          {/* Git Repository section */}
          <div>
            <div style={fieldLabel}>
              Git Repository <span style={{ fontWeight: 400, textTransform: 'none', letterSpacing: 0 }}>(optional)</span>
            </div>

            {authClient && !providersLoading && (
              <>
                {/* Case 1: providers connected — show repo picker */}
                {hasConnectedProviders && (
                  <>
                    <RepoPicker
                      connections={connectedProviders}
                      authClient={authClient}
                      onSelect={handleRepoSelect}
                    />
                    <div style={{ fontSize: 12, color: 'var(--text2)', margin: '8px 0 4px' }}>
                      or enter URL manually
                    </div>
                  </>
                )}

                {/* Case 2: no providers connected but admin providers exist — show connect buttons */}
                {!hasConnectedProviders && hasAdminProviders && (
                  <>
                    <ConnectProviderButtons
                      adminProviders={adminProviders}
                      authClient={authClient}
                    />
                    <div style={{ fontSize: 12, color: 'var(--text2)', margin: '8px 0 4px' }}>
                      or enter URL manually
                    </div>
                  </>
                )}

                {/* Case 3: no admin providers — show only manual URL (no provider section) */}
              </>
            )}

            {/* Manual URL field — always visible */}
            <input
              type="text"
              placeholder="https://github.com/org/repo"
              value={gitUrl}
              onChange={e => {
                setGitUrl(e.target.value)
                // Clear identity when user types manually
                if (gitIdentityId) setGitIdentityId(null)
              }}
              style={inputStyle}
            />
            {!hasConnectedProviders && (
              <div style={{ fontSize: 12, color: 'var(--text2)', marginTop: 6 }}>
                Clone a repo, or leave blank to start from scratch.
              </div>
            )}
          </div>

          {error && (
            <div style={{ color: 'var(--red)', fontSize: 13 }}>{error}</div>
          )}

          <button
            type="submit"
            disabled={!name.trim() || creating}
            style={{
              padding: '13px 0',
              background: 'var(--blue)',
              color: '#fff',
              borderRadius: 12,
              fontSize: 15,
              fontWeight: 600,
              opacity: !name.trim() || creating ? 0.5 : 1,
              marginBottom: 8,
            }}
          >
            {creating ? 'Creating…' : 'Create Project'}
          </button>
        </form>
      </div>
    </div>
  )
}

const fieldLabel: React.CSSProperties = {
  fontSize: 12,
  fontWeight: 600,
  textTransform: 'uppercase',
  letterSpacing: '0.5px',
  color: 'var(--text2)',
  marginBottom: 8,
}

const inputStyle: React.CSSProperties = {
  width: '100%',
  padding: '11px 14px',
  background: 'var(--surf2)',
  border: '1px solid var(--border)',
  borderRadius: 10,
  color: 'var(--text)',
  fontSize: 15,
}
