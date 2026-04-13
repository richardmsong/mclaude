import { useEffect, useMemo, useState, Fragment } from 'react'
import { useVersionPoller } from '@/hooks/useVersionPoller'
import { AuthClient } from '@/transport/auth-client'
import { NATSClient } from '@/transport/nats-client'
import { AuthStore } from '@/stores/auth-store'
import { SessionStore } from '@/stores/session-store'
import { EventStore } from '@/stores/event-store'
import { HeartbeatMonitor } from '@/stores/heartbeat-monitor'
import { SessionListVM } from '@/viewmodels/session-list-vm'
import { ConversationVM } from '@/viewmodels/conversation-vm'
import { AuthScreen } from './AuthScreen'
import { DashboardScreen } from './DashboardScreen'
import { SessionDetailScreen } from './SessionDetailScreen'
import { Settings } from './Settings'
import { TokenUsage } from './TokenUsage'
import type { UsageStats } from '@/types'

// ── Hash routing ──────────────────────────────────────────────────────────
function getRoute(): { screen: string; sessionId?: string } {
  const hash = window.location.hash.slice(1) // remove leading #
  if (!hash || hash === '/') return { screen: 'dashboard' }
  if (hash === 'settings') return { screen: 'settings' }
  if (hash === 'usage') return { screen: 'usage' }
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

// ── Global singleton ──────────────────────────────────────────────────────
const natsClient = new NATSClient()

export function App() {
  // AuthStore uses window.location.origin as the server URL — no user input needed.
  const [authStore, setAuthStore] = useState<AuthStore>(() => {
    return new AuthStore(new AuthClient(window.location.origin), natsClient)
  })

  const [authState, setAuthState] = useState(authStore.state)
  const [connected, setConnected] = useState(false)
  const [route, setRoute] = useState(getRoute)

  // Session store and heartbeat (created after login)
  const [sessionStore, setSessionStore] = useState<SessionStore | null>(null)
  const [heartbeatMonitor, setHeartbeatMonitor] = useState<HeartbeatMonitor | null>(null)

  // Auth state subscription — re-subscribe when authStore changes
  useEffect(() => {
    setAuthState(authStore.state)
    return authStore.onStateChanged(s => setAuthState({ ...s }))
  }, [authStore])

  // NATS connection lifecycle
  useEffect(() => {
    const unsub1 = natsClient.onDisconnect(() => setConnected(false))
    const unsub2 = natsClient.onReconnect(() => setConnected(true))
    return () => { unsub1(); unsub2() }
  }, [])

  // Hash routing
  useEffect(() => {
    const handler = () => setRoute(getRoute())
    window.addEventListener('hashchange', handler)
    return () => window.removeEventListener('hashchange', handler)
  }, [])

  // Track session store version — increments whenever any session changes so
  // App re-renders and picks up fresh session data (name, projectId, state).
  const [sessionVersion, setSessionVersion] = useState(0)

  // Bootstrap session store after login
  useEffect(() => {
    if (authState.status === 'authenticated' && authState.userId) {
      const store = new SessionStore(natsClient, authState.userId)
      const hb = new HeartbeatMonitor(natsClient, authState.userId)
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
    return new SessionListVM(sessionStore, heartbeatMonitor, natsClient, authState.userId)
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

  // Per-session EventStore + ConversationVM
  const [eventStore, setEventStore] = useState<EventStore | null>(null)
  const [conversationVM, setConversationVM] = useState<ConversationVM | null>(null)

  useEffect(() => {
    if (route.screen !== 'session' || !route.sessionId || !authState.userId || !sessionStore) {
      setEventStore(null)
      setConversationVM(null)
      return
    }
    const session = sessionStore.sessions.get(route.sessionId)
    // Wait until the session appears in KV — avoids publishing to 'unknown' projectId
    // and ensures the title shows the real name. Re-runs when sessionVersion increments.
    if (!session) return
    const projectId = session.projectId
    const store = new EventStore({
      natsClient,
      userId: authState.userId,
      projectId,
      sessionId: route.sessionId,
    })
    store.start()
    const vm = new ConversationVM(store, sessionStore, natsClient, authState.userId, projectId, route.sessionId)
    setEventStore(store)
    setConversationVM(vm)
    return () => {
      store.stop()
      vm.destroy()
    }
  // sessionVersion causes re-run when KV watch fires with the session data
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [route.screen, route.sessionId, authState.userId, sessionStore, sessionVersion])

  // ── Version poller ───────────────────────────────────────────────────
  const { updateAvailable } = useVersionPoller()

  // ── Login handler ─────────────────────────────────────────────────────
  const handleConnect = async (email: string, password: string) => {
    const serverUrl = window.location.origin
    const ac = new AuthClient(serverUrl)
    const freshStore = new AuthStore(ac, natsClient)
    await freshStore.login(email, password)
    const tokens = ac.getStoredTokens()
    if (!tokens) throw new Error('Login did not return tokens')
    // Use natsUrl from login response; fall back to ws(s)://host/nats
    const natsUrl = tokens.natsUrl
      ?? serverUrl.replace(/^https:/, 'wss:').replace(/^http:/, 'ws:') + '/nats'
    await natsClient.connect({ url: natsUrl, jwt: tokens.jwt, nkeySeed: tokens.nkeySeed })
    setConnected(true)
    freshStore.startRefreshLoop()
    setAuthStore(freshStore)
  }

  // ── Logout ────────────────────────────────────────────────────────────
  const handleLogout = async () => {
    await authStore.logout()
    setSessionStore(null)
    setHeartbeatMonitor(null)
    setConnected(false)
    navigate('/')
  }

  // Aggregate usage across all sessions
  const totalUsage: UsageStats = useMemo(() => {
    if (!sessionStore) return { inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, costUsd: 0 }
    let agg = { inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, costUsd: 0 }
    for (const s of sessionStore.sessions.values()) {
      agg.inputTokens += s.usage.inputTokens
      agg.outputTokens += s.usage.outputTokens
      agg.cacheReadTokens += s.usage.cacheReadTokens
      agg.cacheWriteTokens += s.usage.cacheWriteTokens
      agg.costUsd += s.usage.costUsd
    }
    return agg
  }, [sessionStore])

  // ── Auth gate ─────────────────────────────────────────────────────────
  if (authState.status === 'unauthenticated' || authState.status === 'expired') {
    return (
      <Fragment>
        <AuthScreen onConnect={handleConnect} />
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
        />
        {updateAvailable && <UpdateBanner />}
      </Fragment>
    )
  }

  if (route.screen === 'usage') {
    return (
      <Fragment>
        <TokenUsage
          usage={totalUsage}
          onBack={() => navigate('/')}
          connected={connected}
        />
        {updateAvailable && <UpdateBanner />}
      </Fragment>
    )
  }

  if (route.screen === 'session' && route.sessionId && conversationVM && eventStore && sessionStore) {
    const session = sessionStore.sessions.get(route.sessionId)
    return (
      <Fragment>
        <SessionDetailScreen
          sessionId={route.sessionId}
          sessionName={session?.name ?? route.sessionId.slice(0, 8)}
          sessionState={session?.state ?? 'idle'}
          conversationVM={conversationVM}
          onBack={() => navigate('/')}
          connected={connected}
          initialMessage={initialMessage ?? undefined}
          onInitialMessageSent={() => setInitialMessage(null)}
        />
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
        />
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
      {updateAvailable && <UpdateBanner />}
    </Fragment>
  )
}
