import { useEffect, useMemo, useRef, useState, Fragment } from 'react'
import { useVersionPoller } from '@/hooks/useVersionPoller'
import { AuthClient } from '@/transport/auth-client'
import { NATSClient } from '@/transport/nats-client'
import { AuthStore } from '@/stores/auth-store'
import { SessionStore } from '@/stores/session-store'
import { EventStore } from '@/stores/event-store'
import { HeartbeatMonitor } from '@/stores/heartbeat-monitor'
import { LifecycleStore } from '@/stores/lifecycle-store'
import { SessionListVM } from '@/viewmodels/session-list-vm'
import { ConversationVM } from '@/viewmodels/conversation-vm'
import { TerminalVM } from '@/viewmodels/terminal-vm'
import { AuthScreen } from './AuthScreen'
import { DashboardScreen } from './DashboardScreen'
import { SessionDetailScreen } from './SessionDetailScreen'
import { Settings } from './Settings'
import { TokenUsage } from './TokenUsage'
import { UserManagement } from './UserManagement'

// ── Hash routing (ADR-0024: typed slugs) ─────────────────────────────────
// Session hash format: #s/{uslug}/{pslug}/{sslug} (new, slug-based)
// Legacy format: #s/{sessionId} (UUID, kept for backward compat)
function getRoute(): { screen: string; sessionId?: string } {
  const hash = window.location.hash.slice(1) // remove leading #
  if (!hash || hash === '/') return { screen: 'dashboard' }
  if (hash === 'settings') return { screen: 'settings' }
  if (hash === 'usage') return { screen: 'usage' }
  if (hash === 'users') return { screen: 'users' }
  // New slug-based format: s/{uslug}/{pslug}/{sslug}
  const slugSessionMatch = /^s\/([a-z0-9][a-z0-9-]*)\/(.[a-z0-9-]*)\/(.[a-z0-9-]*)$/.exec(hash)
  if (slugSessionMatch) return { screen: 'session', sessionId: slugSessionMatch[3] }
  // Legacy UUID format: s/{uuid-or-id}
  const sessionMatch = /^s\/(.+)$/.exec(hash)
  if (sessionMatch) return { screen: 'session', sessionId: sessionMatch[1] }
  return { screen: 'dashboard' }
}

function navigate(hash: string) {
  window.location.hash = hash
}


// ── Update banner ─────────────────────────────────────────────────────────
function UpdateBanner() {
  return (
    <div
      onClick={() => window.location.reload()}
      style={{
        position: 'fixed',
        bottom: 72,  // above FAB (56px) + some gap
        left: '50%',
        transform: 'translateX(-50%)',
        zIndex: 9999,
        background: 'var(--surf2)',
        border: '1px solid var(--border)',
        borderRadius: 10,
        padding: '8px 16px',
        fontSize: 13,
        color: 'var(--text2)',
        cursor: 'pointer',
        whiteSpace: 'nowrap',
        boxShadow: '0 2px 12px rgba(0,0,0,0.4)',
      }}
    >
      New version available — tap to reload
    </div>
  )
}

// ── Toast component ───────────────────────────────────────────────────────
interface ToastProps {
  message: string
  isError?: boolean
}

function Toast({ message, isError }: ToastProps) {
  return (
    <div style={{
      position: 'fixed', top: 60, left: '50%', transform: 'translateX(-50%)',
      zIndex: 10000,
      background: isError ? 'rgba(255,69,58,0.15)' : 'var(--surf2)',
      border: `1px solid ${isError ? 'var(--red)' : 'var(--border)'}`,
      borderRadius: 10, padding: '10px 18px', fontSize: 14,
      color: isError ? 'var(--red)' : 'var(--text)',
      boxShadow: '0 2px 12px rgba(0,0,0,0.4)',
      maxWidth: '90vw', textAlign: 'center',
    }}>
      {message}
    </div>
  )
}

// ── Post-redirect query param handling ────────────────────────────────────
//
// Spec (plan-github-oauth.md §SPA): On page load, read query params: provider,
// connected, goto, error. Show toast for success or error. Navigate to goto
// route (settings → #settings, new-project → open sheet). Clean query string
// via history.replaceState.

interface RedirectParams {
  provider?: string
  connected?: string
  goto?: string
  error?: string
  username?: string
}

function readAndClearRedirectParams(): RedirectParams | null {
  const params = new URLSearchParams(window.location.search)
  const provider = params.get('provider') ?? undefined
  const connected = params.get('connected') ?? undefined
  const goto = params.get('goto') ?? undefined
  const error = params.get('error') ?? undefined
  const username = params.get('username') ?? undefined

  if (!provider && !error) return null

  // Clean the query string — spec requires history.replaceState
  const cleanUrl = window.location.pathname + window.location.hash
  history.replaceState(null, '', cleanUrl)

  return { provider, connected, goto, error, username }
}

// ── Global singleton ──────────────────────────────────────────────────────
const natsClient = new NATSClient()

export function App() {
  // AuthStore uses window.location.origin as the server URL — no user input needed.
  const [authClient] = useState<AuthClient>(() => new AuthClient(window.location.origin))
  const [authStore, setAuthStore] = useState<AuthStore>(() => {
    return new AuthStore(authClient, natsClient)
  })

  const [authState, setAuthState] = useState(authStore.state)
  const [connected, setConnected] = useState(false)
  const [route, setRoute] = useState(getRoute)

  // Decode role from JWT payload — returns 'admin' | 'user' | undefined
  const userRole = useMemo(() => {
    if (!authState.jwt) return undefined
    try {
      const parts = authState.jwt.split('.')
      if (parts.length !== 3) return undefined
      const payload = JSON.parse(atob(parts[1]!)) as { role?: string }
      return payload.role
    } catch {
      return undefined
    }
  }, [authState.jwt])

  // Session store and heartbeat (created after login)
  const [sessionStore, setSessionStore] = useState<SessionStore | null>(null)
  const [heartbeatMonitor, setHeartbeatMonitor] = useState<HeartbeatMonitor | null>(null)

  // Toast state for post-redirect notifications
  const [toast, setToast] = useState<{ message: string; isError: boolean } | null>(null)

  // Post-redirect param handling: open New Project sheet if goto=new-project
  const [openNewProject, setOpenNewProject] = useState(false)

  // Auth state subscription — re-subscribe when authStore changes
  useEffect(() => {
    setAuthState(authStore.state)
    return authStore.onStateChanged(s => setAuthState({ ...s }))
  }, [authStore])

  // NATS connection lifecycle
  useEffect(() => {
    const unsub1 = natsClient.onDisconnect(() => setConnected(false))
    const unsub2 = natsClient.onReconnect(() => {
      setConnected(true)
      // Spec (plan-client-architecture.md): on reconnect, EventStore must
      // re-subscribe from max(lastSequence + 1, replayFromSeq) so no events
      // are missed and no duplicates are delivered.
      const store = eventStoreRef.current
      if (store) {
        const resumeSeq = Math.max(store.lastSequence + 1, store.replayFromSeq)
        store.stop()
        store.start(resumeSeq)
      }
    })
    return () => { unsub1(); unsub2() }
  }, [])

  // On mount: process query params FIRST (before restoring session) so the
  // redirect toast is shown even before the NATS connection is ready.
  useEffect(() => {
    const params = readAndClearRedirectParams()
    if (!params) return

    if (params.error) {
      const errorMessages: Record<string, string> = {
        denied: 'Authorization was denied',
        csrf: 'Authentication state mismatch — please try again',
        storage: 'Failed to save credentials — please try again',
        exchange_failed: 'Failed to connect — please try again',
        profile_failed: 'Failed to fetch your profile — please try again',
      }
      const msg = errorMessages[params.error] ?? `Connection error: ${params.error}`
      setToast({ message: msg, isError: true })
      setTimeout(() => setToast(null), 5000)
    } else if (params.connected === 'true') {
      const providerLabel = params.provider
        ? params.provider.charAt(0).toUpperCase() + params.provider.slice(1)
        : 'Provider'
      const msg = params.username
        ? `${providerLabel} connected as @${params.username}`
        : `${providerLabel} connected successfully`
      setToast({ message: msg, isError: false })
      setTimeout(() => setToast(null), 4000)
    }

    // Navigate to the goto route
    if (params.goto === 'settings') {
      navigate('settings')
    } else if (params.goto === 'new-project') {
      // Defer opening the sheet until auth + initial data load complete
      setOpenNewProject(true)
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // On mount: restore session from localStorage so refresh doesn't log the user out
  useEffect(() => {
    const tokens = authClient.loadFromStorage()
    if (!tokens) return
    const serverUrl = window.location.origin
    const natsUrl = tokens.natsUrl
      ?? serverUrl.replace(/^https:/, 'wss:').replace(/^http:/, 'ws:') + '/nats'
    const freshStore = new AuthStore(authClient, natsClient)
    freshStore.restoreTokens(tokens)
    natsClient.connect({ url: natsUrl, jwt: tokens.jwt, nkeySeed: tokens.nkeySeed })
      .then(() => {
        setConnected(true)
        freshStore.startRefreshLoop()
        setAuthStore(freshStore)
      })
      .catch(() => {
        // Stored tokens invalid/expired — clear and show login
        authClient.clearTokens()
      })
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Hash routing
  useEffect(() => {
    const handler = () => setRoute(getRoute())
    window.addEventListener('hashchange', handler)
    return () => window.removeEventListener('hashchange', handler)
  }, [])

  // X3: Background reconnect — mobile browsers (iOS Safari) kill the WebSocket
  // when the tab is backgrounded. On visibility restore, reconnect NATS so
  // the session store and event store resume without a full page reload.
  useEffect(() => {
    const handler = () => {
      if (document.visibilityState !== 'visible') return
      if (!natsClient.isConnected()) {
        natsClient.reconnect('').catch(() => {
          // Reconnect failure is handled by the NATS disconnect/reconnect listeners
          // which update the connected state and eventually trigger auth refresh
        })
      }
    }
    document.addEventListener('visibilitychange', handler)
    return () => document.removeEventListener('visibilitychange', handler)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Track session store version — increments whenever any session changes so
  // App re-renders and picks up fresh session data (name, projectId, state).
  const [sessionVersion, setSessionVersion] = useState(0)

  // R2: Desktop notification permission — request once on first NATS connect.
  useEffect(() => {
    if (connected && typeof Notification !== 'undefined' && Notification.permission === 'default') {
      Notification.requestPermission().catch(() => {
        // Permission request failed or was denied — ignore silently
      })
    }
  }, [connected])

  // R2: Fire a desktop notification when any session transitions to requires_action
  // and the tab is not visible.
  const prevSessionStatesRef = useRef<Map<string, string>>(new Map())
  useEffect(() => {
    if (!sessionStore) return
    return sessionStore.onSessionChanged((sessions) => {
      if (typeof Notification === 'undefined') return
      if (Notification.permission !== 'granted') return
      if (document.visibilityState === 'visible') return

      for (const [id, session] of sessions) {
        const prev = prevSessionStatesRef.current.get(id)
        if (session.state === 'requires_action' && prev !== 'requires_action') {
          new Notification('MClaude — Permission needed', {
            body: 'A session needs your approval',
          })
        }
      }
      // Snapshot current states for next comparison
      for (const [id, session] of sessions) {
        prevSessionStatesRef.current.set(id, session.state)
      }
    })
  }, [sessionStore])

  // Bootstrap session store after login
  useEffect(() => {
    if (authState.status === 'authenticated' && authState.userId) {
      const uslug = authState.userSlug ?? authState.userId
      const store = new SessionStore(natsClient, authState.userId, uslug)
      const hb = new HeartbeatMonitor(natsClient, authState.userId, 60_000, uslug)
      store.startWatching()
      hb.start()
      const unsub = store.onSessionChanged(() => setSessionVersion(v => v + 1))
      setSessionStore(store)
      setHeartbeatMonitor(hb)
      return () => {
        unsub()
        store.stopWatching()
        hb.stop()
      }
    }
    return undefined
  }, [authState.status, authState.userId])

  // SessionListVM (memo: recreate when store changes)
  const sessionListVM = useMemo(() => {
    if (!sessionStore || !heartbeatMonitor || !authState.userId) return null
    return new SessionListVM(sessionStore, heartbeatMonitor, natsClient, authState.userId, undefined, authState.userSlug ?? authState.userId)
  }, [sessionStore, heartbeatMonitor, authState.userId])

  // First-run: auto-create session if no sessions exist (handles seeded projects with no sessions)
  const [initialMessage, setInitialMessage] = useState<string | null>(null)
  useEffect(() => {
    if (!sessionListVM) return
    const timer = setTimeout(async () => {
      const projs = sessionListVM.projects
      const totalSessions = projs.reduce((sum, p) => sum + p.sessions.length, 0)
      if (totalSessions > 0) return  // sessions already exist — nothing to do
      try {
        // Use existing project or create a default one
        const projectId = projs.length > 0
          ? projs[0]!.id
          : await sessionListVM.createProject('Default')
        const sessionId = await sessionListVM.createSession(projectId, 'main', 'Getting Started')
        setInitialMessage(
          "Hi! I'm Claude. You're in MClaude — a real-time coding environment powered by Claude Code.\n\n" +
          "Here's what you can do here:\n" +
          "- Write and edit files across your project\n" +
          "- Run shell commands (git, npm, make, etc.)\n" +
          "- Search and read your codebase\n" +
          "- Create more sessions for different tasks or branches\n\n" +
          "Ask me anything to get started — like \"what's in this project?\" or \"help me fix this bug\". What would you like to work on?"
        )
        navigate(`s/${sessionId}`)
      } catch {
        // server unavailable (e.g. no session-agent) — user can create manually
      }
    }, 1000)
    return () => clearTimeout(timer)
  }, [sessionListVM])

  // Per-session EventStore + ConversationVM + TerminalVM
  const [eventStore, setEventStore] = useState<EventStore | null>(null)
  // Ref kept in sync so the NATS reconnect handler can access the current store
  // without a stale closure (the handler effect runs only once, on mount).
  const eventStoreRef = useRef<EventStore | null>(null)
  // Sync ref immediately on every render so it's always current.
  eventStoreRef.current = eventStore
  const [conversationVM, setConversationVM] = useState<ConversationVM | null>(null)
  const [terminalVm, setTerminalVm] = useState<TerminalVM | null>(null)

  // Resolve projectId from session KV once, without recreating the EventStore
  // on every subsequent KV update (which would lose accumulated conversation data).
  const [resolvedProjectId, setResolvedProjectId] = useState<string | null>(null)

  // Reset resolved projectId when leaving the session screen or switching sessions
  useEffect(() => {
    if (route.screen !== 'session' || !route.sessionId) {
      setResolvedProjectId(null)
    }
  }, [route.screen, route.sessionId])

  // Resolve projectId from session KV — re-runs when session data arrives (sessionVersion)
  // but uses functional update to only set it once per session (never overwrite once resolved).
  useEffect(() => {
    if (route.screen !== 'session' || !route.sessionId || !sessionStore) return
    const session = sessionStore.resolveSession(route.sessionId)
    if (session) {
      setResolvedProjectId(prev => prev ?? session.projectId)
    }
  }, [route.screen, route.sessionId, sessionStore, sessionVersion])

  // ADR-0024: Rewrite the hash URL to slug format once all slugs are available.
  // This runs after session KV data arrives and all three slugs (user/project/session) are known.
  // Only rewrites if the current URL is NOT already in slug format (no double-rewrite).
  useEffect(() => {
    if (route.screen !== 'session' || !route.sessionId || !sessionStore) return
    const session = sessionStore.resolveSession(route.sessionId)
    if (!session?.slug || !session.projectSlug) return
    const uslug = authState.userSlug ?? authState.userId
    if (!uslug) return
    // Check if current hash is already in slug format: s/{uslug}/{pslug}/{sslug}
    const currentHash = window.location.hash.slice(1)
    const expectedHash = `s/${uslug}/${session.projectSlug}/${session.slug}`
    if (currentHash !== expectedHash) {
      // Use replaceState so the slug URL doesn't create an extra history entry
      history.replaceState(null, '', `#${expectedHash}`)
    }
  }, [route.screen, route.sessionId, sessionStore, sessionVersion, authState.userSlug, authState.userId])

  // Per-session EventStore + ConversationVM + TerminalVM + LifecycleStore — created ONCE per sessionId+projectId.
  // Does NOT depend on sessionVersion so KV updates (idle→running→idle) don't destroy
  // and recreate the store, which would lose all accumulated conversation data.
  useEffect(() => {
    if (!route.sessionId || !authState.userId || !resolvedProjectId || !sessionStore) {
      setEventStore(null)
      setConversationVM(null)
      setTerminalVm(null)
      return
    }
    const session = sessionStore?.resolveSession(route.sessionId!)
    const project = session ? sessionStore?.projects.get(session.projectId) : undefined
    const resolvedUserSlug = authState.userSlug ?? authState.userId
    const resolvedProjectSlug = project?.slug ?? resolvedProjectId
    const resolvedSessionSlug = session?.slug ?? route.sessionId!
    const store = new EventStore({
      natsClient,
      userId: authState.userId,
      projectId: resolvedProjectId,
      sessionId: route.sessionId,
      userSlug: resolvedUserSlug,
      projectSlug: resolvedProjectSlug ?? undefined,
      sessionSlug: resolvedSessionSlug,
    })
    // Start from replayFromSeq in KV — skips events before last clear/compaction (spec: plan-client-architecture.md)
    const replayFromSeq = session?.replayFromSeq ?? undefined
    store.start(replayFromSeq)
    const vm = new ConversationVM(store, sessionStore, natsClient, authState.userId, resolvedProjectId, route.sessionId, resolvedUserSlug, resolvedProjectSlug ?? resolvedProjectId)
    const lifecycle = new LifecycleStore(natsClient, authState.userId, resolvedProjectId, resolvedUserSlug, resolvedProjectSlug ?? resolvedProjectId)
    lifecycle.start()
    const tvm = new TerminalVM(natsClient, authState.userId, resolvedProjectId, resolvedUserSlug, resolvedProjectSlug ?? resolvedProjectId)
    setEventStore(store)
    setConversationVM(vm)
    setTerminalVm(tvm)
    return () => {
      store.stop()
      vm.destroy()
      lifecycle.stop()
    }
  // resolvedProjectId is set once per session (functional update), so this effect fires
  // exactly once when the projectId is first resolved, and again only if sessionId changes.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [route.sessionId, authState.userId, resolvedProjectId])

  // ── Version poller ───────────────────────────────────────────────────
  const { updateAvailable, blocked: versionBlocked, blockMessage: versionBlockMessage } = useVersionPoller()

  // ── Login handler ─────────────────────────────────────────────────────
  const handleConnect = async (email: string, password: string) => {
    const serverUrl = window.location.origin
    const freshStore = new AuthStore(authClient, natsClient)
    await freshStore.login(email, password)
    const tokens = authClient.getStoredTokens()
    if (!tokens) throw new Error('Login did not return tokens')
    // Use natsUrl from login response; fall back to ws(s)://host/nats
    const natsUrl = tokens.natsUrl
      ?? serverUrl.replace(/^https:/, 'wss:').replace(/^http:/, 'ws:') + '/nats'
    try {
      await natsClient.connect({ url: natsUrl, jwt: tokens.jwt, nkeySeed: tokens.nkeySeed })
    } catch (err) {
      throw new Error(`Login succeeded but could not connect to messaging (${natsUrl}): ${err instanceof Error ? err.message : err}`)
    }
    setConnected(true)
    freshStore.startRefreshLoop()
    setAuthStore(freshStore)
  }

  // ── Cache reset ───────────────────────────────────────────────────────
  const handleCacheReset = () => {
    // X1: Clear all client-side caches and re-subscribe from scratch
    // Stop all watches/subscriptions
    sessionStore?.stopWatching()
    heartbeatMonitor?.stop()
    // Re-start to re-subscribe from scratch (replayFromSeq = 0)
    if (sessionStore && heartbeatMonitor) {
      sessionStore.startWatching()
      heartbeatMonitor.start()
    }
    // Reset resolvedProjectId so the EventStore effect re-runs and rebuilds from scratch.
    // sessionVersion bump causes the projectId-resolver effect to re-fire and set it again.
    setResolvedProjectId(null)
    setSessionVersion(v => v + 1)
  }

  // ── Logout ────────────────────────────────────────────────────────────
  const handleLogout = async () => {
    await authStore.logout()
    setSessionStore(null)
    setHeartbeatMonitor(null)
    setConnected(false)
    navigate('/')
  }


  // ── X4: Version block screen ──────────────────────────────────────────
  // Shown when the client is below minClientVersion and a reload didn't fix it.
  if (versionBlocked) {
    return (
      <div style={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        height: '100vh',
        gap: 16,
        padding: 24,
        textAlign: 'center',
        background: 'var(--bg)',
      }}>
        <div style={{ fontSize: 32 }}>↻</div>
        <div style={{ fontWeight: 600, fontSize: 18, color: 'var(--text)' }}>
          {versionBlockMessage ?? 'Server is updating, please wait…'}
        </div>
        <div style={{ color: 'var(--text2)', fontSize: 14 }}>
          Reload the page once the update is complete.
        </div>
        <button
          onClick={() => window.location.reload()}
          style={{
            marginTop: 8,
            padding: '10px 24px',
            background: 'var(--blue)',
            color: '#fff',
            borderRadius: 10,
            fontSize: 15,
            fontWeight: 600,
          }}
        >
          Reload now
        </button>
      </div>
    )
  }

  // ── Auth gate ─────────────────────────────────────────────────────────
  if (authState.status === 'unauthenticated' || authState.status === 'expired') {
    return (
      <Fragment>
        <AuthScreen onConnect={handleConnect} />
        {toast && <Toast message={toast.message} isError={toast.isError} />}
        {updateAvailable && <UpdateBanner />}
      </Fragment>
    )
  }

  // ── Route rendering ───────────────────────────────────────────────────
  if (route.screen === 'settings') {
    return (
      <Fragment>
        <Settings
          userId={authState.userId ?? ''}
          serverUrl={window.location.origin}
          connected={connected}
          sessionCount={sessionStore?.sessions.size ?? 0}
          onBack={() => navigate('/')}
          onLogout={handleLogout}
          onCacheReset={handleCacheReset}
          authClient={authClient}
          sessions={sessionStore?.sessions}
          projects={sessionStore?.projects}
          role={userRole}
          onNavigate={navigate}
        />
        {toast && <Toast message={toast.message} isError={toast.isError} />}
        {updateAvailable && <UpdateBanner />}
      </Fragment>
    )
  }

  if (route.screen === 'usage') {
    return (
      <Fragment>
        <TokenUsage
          sessions={sessionStore ? Array.from(sessionStore.sessions.values()) : []}
          onBack={() => navigate('/')}
          connected={connected}
        />
        {toast && <Toast message={toast.message} isError={toast.isError} />}
        {updateAvailable && <UpdateBanner />}
      </Fragment>
    )
  }

  if (route.screen === 'users') {
    return (
      <Fragment>
        <UserManagement
          connected={connected}
          onBack={() => navigate('/')}
        />
        {toast && <Toast message={toast.message} isError={toast.isError} />}
        {updateAvailable && <UpdateBanner />}
      </Fragment>
    )
  }

  if (route.screen === 'session' && route.sessionId && conversationVM && eventStore && sessionStore) {
    const session = sessionStore.resolveSession(route.sessionId)
    const project = session ? sessionStore.projects.get(session.projectId) : undefined
    return (
      <Fragment>
        <SessionDetailScreen
          sessionId={route.sessionId}
          sessionName={project?.name ?? session?.name ?? route.sessionId.slice(0, 8)}
          sessionState={session?.state ?? 'idle'}
          sessionModel={session?.model}
          sessionUsage={session?.usage}
          sessionExtraFlags={session?.extraFlags ?? ''}
          sessionListVM={sessionListVM ?? undefined}
          conversationVM={conversationVM}
          terminalVm={terminalVm ?? undefined}
          onBack={() => navigate('/')}
          connected={connected}
          initialMessage={initialMessage ?? undefined}
          onInitialMessageSent={() => setInitialMessage(null)}
        />
        {toast && <Toast message={toast.message} isError={toast.isError} />}
        {updateAvailable && <UpdateBanner />}
      </Fragment>
    )
  }

  if (sessionListVM) {
    return (
      <Fragment>
        <DashboardScreen
          sessionListVM={sessionListVM}
          connected={connected}
          onSelectSession={id => navigate(`s/${id}`)}
          onSettings={() => navigate('settings')}
          onUsage={() => navigate('usage')}
          authClient={authClient}
          openNewProject={openNewProject}
          onNewProjectOpened={() => setOpenNewProject(false)}
        />
        {toast && <Toast message={toast.message} isError={toast.isError} />}
        {updateAvailable && <UpdateBanner />}
      </Fragment>
    )
  }

  // Loading state
  return (
    <Fragment>
      <div style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        height: '100vh',
        color: 'var(--text2)',
        fontSize: 14,
      }}>
        Connecting…
      </div>
      {toast && <Toast message={toast.message} isError={toast.isError} />}
      {updateAvailable && <UpdateBanner />}
    </Fragment>
  )
}
