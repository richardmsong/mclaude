import { useEffect, useRef, useState } from 'react'
import { NavBar } from './NavBar'
import { StatusDot } from './StatusDot'
import { NewSessionSheet } from './NewSessionSheet'
import { NewProjectSheet } from './NewProjectSheet'
import type { SessionListVM, ProjectVM, SessionVM } from '@/viewmodels/session-list-vm'
import type { AuthClient } from '@/transport/auth-client'

const LAST_PROJECT_KEY = 'mclaude.lastProjectId'

// Show last 2 path segments, replacing $HOME with ~
function shortenPath(p: string): string {
  if (!p) return ''
  const parts = p.replace(/\/$/, '').split('/')
  const short = parts.slice(-2).join('/')
  return short.startsWith('~') ? short : `~/${short}`
}

interface DashboardScreenProps {
  sessionListVM: SessionListVM
  connected: boolean
  onSelectSession: (sessionId: string) => void
  onSettings: () => void
  onUsage: () => void
  authClient?: AuthClient
  openNewProject?: boolean
  onNewProjectOpened?: () => void
}

export function DashboardScreen({
  sessionListVM,
  connected,
  onSelectSession,
  onSettings,
  onUsage,
  authClient,
  openNewProject,
  onNewProjectOpened,
}: DashboardScreenProps) {
  const [projects, setProjects] = useState<ProjectVM[]>(sessionListVM.projects)
  const [activeGroup, setActiveGroup] = useState<string>('all')
  const [showNewSession, setShowNewSession] = useState(false)
  const [showNewProject, setShowNewProject] = useState(false)
  const [showMenu, setShowMenu] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    setProjects(sessionListVM.projects)
    const unsub = sessionListVM.onProjectsChanged(p => setProjects([...p]))
    return unsub
  }, [sessionListVM])

  // Open new project sheet programmatically (e.g. after OAuth redirect with goto=new-project)
  useEffect(() => {
    if (openNewProject) {
      setShowNewProject(true)
      onNewProjectOpened?.()
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openNewProject])

  // Close menu on outside click
  useEffect(() => {
    if (!showMenu) return
    const handler = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setShowMenu(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [showMenu])

  // Flatten all sessions
  const allSessions: Array<{ session: SessionVM; projectName: string }> = []
  for (const p of projects) {
    for (const s of p.sessions) {
      allSessions.push({ session: s, projectName: p.name })
    }
  }

  const badge = allSessions.filter(
    ({ session: s }) => s.state === 'requires_action' || s.hasPendingPermission
  ).length

  const unhealthyProjects = projects.filter(p => !p.healthy)

  const STATE_LABELS: Record<string, string> = {
    working: 'Working',
    running: 'Working',
    requires_action: 'Needs permission',
    idle: 'Idle',
    updating: 'Updating...',
    restarting: 'Restarting',
    failed: 'Failed',
    unknown: 'Unknown',
    waiting_for_input: 'Waiting for input',
  }

  const displayed = activeGroup === 'all'
    ? allSessions
    : allSessions.filter(({ session }) => session.name.includes(activeGroup))

  const groups = ['all', ...Array.from(new Set(allSessions.map(({ projectName }) => projectName))).sort()]
  const showChips = groups.length > 2

  const handleFAB = async () => {
    if (projects.length === 0) return
    if (projects.length === 1) {
      // Single project — create session directly
      try {
        const projectId = projects[0]!.id
        const sessionId = await sessionListVM.createSession(projectId, 'main', 'new-session')
        localStorage.setItem(LAST_PROJECT_KEY, projectId)
        onSelectSession(sessionId)
      } catch {
        // ignore — session store will reflect any created session
      }
    } else {
      setShowNewSession(true)
    }
  }

  const menuButton = (
    <div ref={menuRef} style={{ position: 'relative' }}>
      <button
        onClick={() => setShowMenu(v => !v)}
        style={{ fontSize: 16, color: 'var(--text2)', padding: '0 2px' }}
      >
        ⋯
      </button>
      {showMenu && (
        <div style={{
          position: 'absolute',
          top: 'calc(100% + 8px)',
          right: 0,
          background: 'var(--surf)',
          border: '1px solid var(--border)',
          borderRadius: 10,
          minWidth: 160,
          zIndex: 300,
          overflow: 'hidden',
          boxShadow: '0 4px 16px rgba(0,0,0,0.4)',
        }}>
          <button
            onClick={() => { setShowMenu(false); setShowNewProject(true) }}
            style={{
              width: '100%',
              padding: '12px 16px',
              textAlign: 'left',
              color: 'var(--text)',
              fontSize: 14,
              display: 'flex',
              alignItems: 'center',
              gap: 10,
            }}
          >
            <span>📁</span> New Project
          </button>
        </div>
      )}
    </div>
  )

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--bg)' }}>
      <NavBar
        title="MClaude"
        badge={badge}
        connected={connected}
        onSettings={onSettings}
        onUsage={onUsage}
        right={menuButton}
      />

      {/* Filter chips */}
      {showChips && (
        <div style={{
          display: 'flex',
          gap: 8,
          padding: '8px 16px',
          overflowX: 'auto',
          borderBottom: '1px solid var(--border)',
          flexShrink: 0,
        }}>
          {groups.map(group => (
            <button
              key={group}
              onClick={() => setActiveGroup(group)}
              style={{
                padding: '4px 12px',
                borderRadius: 14,
                fontSize: 13,
                fontWeight: 500,
                background: activeGroup === group ? 'var(--blue)' : 'var(--surf2)',
                color: activeGroup === group ? '#fff' : 'var(--text2)',
                flexShrink: 0,
              }}
            >
              {group === 'all' ? 'All' : group}
            </button>
          ))}
        </div>
      )}

      {/* P6: Agent health banner */}
      {unhealthyProjects.length > 0 && (
        <div style={{
          background: 'rgba(255,69,58,0.12)',
          borderBottom: '1px solid rgba(255,69,58,0.3)',
          padding: '8px 16px',
          color: 'var(--red)',
          fontSize: 13,
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          flexShrink: 0,
        }}>
          <span>⚠</span>
          <span>
            Agent down: {unhealthyProjects.map(p => p.name).join(', ')} — heartbeat stale
          </span>
        </div>
      )}

      {/* Session list */}
      <div style={{ flex: 1, overflowY: 'auto' }}>
        {displayed.length === 0 ? (
          <div style={{
            display: 'flex',
            flexDirection: 'column',
            height: '100%',
            padding: '16px 0',
          }}>
            {activeGroup === 'all' && projects.length > 0 ? (
              <>
                <div style={{
                  fontSize: 12,
                  fontWeight: 600,
                  textTransform: 'uppercase',
                  letterSpacing: '0.5px',
                  color: 'var(--text2)',
                  padding: '0 16px 8px',
                }}>
                  Your Projects
                </div>
                {projects.map(p => (
                  <button
                    key={p.id}
                    onClick={async () => {
                      try {
                        const sessionId = await sessionListVM.createSession(p.id, 'main', 'new-session')
                        onSelectSession(sessionId)
                      } catch {
                        // session-agent not available
                      }
                    }}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      width: '100%',
                      padding: '12px 16px',
                      borderBottom: '1px solid var(--border)',
                      background: 'none',
                      textAlign: 'left',
                      gap: 10,
                    }}
                  >
                    <span style={{ fontSize: 16 }}>📁</span>
                    <span style={{ flex: 1, color: 'var(--text)', fontSize: 15, fontWeight: 500 }}>{p.name}</span>
                    <span style={{ color: 'var(--text3)', fontSize: 18 }}>›</span>
                  </button>
                ))}
                <div style={{ fontSize: 14, color: 'var(--text2)', padding: '12px 16px' }}>
                  Tap + to start a session
                </div>
              </>
            ) : (
              <div style={{
                flex: 1,
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'center',
                justifyContent: 'center',
                gap: 8,
                color: 'var(--text2)',
              }}>
                <div style={{ fontSize: 18, fontWeight: 600, color: 'var(--text)' }}>No Sessions</div>
                <div style={{ fontSize: 14 }}>
                  {activeGroup !== 'all' ? 'No sessions in this group' : 'Tap + to start a Claude session'}
                </div>
              </div>
            )}
          </div>
        ) : (
          displayed.map(({ session, projectName }) => (
            <button
              key={session.id}
              onClick={() => onSelectSession(session.id)}
              style={{
                display: 'flex',
                alignItems: 'center',
                width: '100%',
                padding: '12px 16px',
                borderBottom: '1px solid var(--border)',
                background: 'none',
                textAlign: 'left',
                gap: 12,
              }}
            >
              <StatusDot state={session.state as 'idle' | 'running' | 'requires_action' | 'restarting' | 'failed' | 'updating'} size={12} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ color: 'var(--text)', fontWeight: 500, fontSize: 15, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {session.name || projectName}
                </div>
                <div style={{ color: 'var(--text2)', fontSize: 13, marginTop: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {STATE_LABELS[session.state] ?? session.state}
                  {session.cwd ? ` · ${shortenPath(session.cwd)}` : (session.name ? ` · ${projectName}` : '')}
                </div>
              </div>
              <span style={{ color: 'var(--text3)', fontSize: 18 }}>›</span>
            </button>
          ))
        )}
      </div>

      {/* FAB */}
      <button
        onClick={handleFAB}
        style={{
          position: 'fixed',
          bottom: 20,
          right: 20,
          width: 52,
          height: 52,
          borderRadius: '50%',
          background: 'var(--blue)',
          color: '#fff',
          fontSize: 24,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          boxShadow: '0 4px 12px rgba(10,132,255,0.4)',
          zIndex: 50,
        }}
      >
        +
      </button>

      {showNewSession && (
        <NewSessionSheet
          sessionListVM={sessionListVM}
          onClose={() => setShowNewSession(false)}
          onSessionCreated={sessionId => { onSelectSession(sessionId); setShowNewSession(false) }}
        />
      )}

      {showNewProject && (
        <NewProjectSheet
          sessionListVM={sessionListVM}
          onClose={() => setShowNewProject(false)}
          authClient={authClient}
          onCreated={async projectId => {
            // Always navigate into the new project by starting a session in it
            try {
              const sessionId = await sessionListVM.createSession(projectId, 'main', 'new-session')
              localStorage.setItem(LAST_PROJECT_KEY, projectId)
              onSelectSession(sessionId)
            } catch {
              // session-agent not available — project was still created, user can tap it later
            }
          }}
        />
      )}
    </div>
  )
}
