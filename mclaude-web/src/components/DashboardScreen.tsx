import { useEffect, useState } from 'react'
import { NavBar } from './NavBar'
import { StatusDot } from './StatusDot'
import { NewSessionSheet } from './NewSessionSheet'
import type { SessionListVM, ProjectVM, SessionVM } from '@/viewmodels/session-list-vm'

interface DashboardScreenProps {
  sessionListVM: SessionListVM
  connected: boolean
  onSelectSession: (sessionId: string) => void
  onSettings: () => void
  onUsage: () => void
}

export function DashboardScreen({
  sessionListVM,
  connected,
  onSelectSession,
  onSettings,
  onUsage,
}: DashboardScreenProps) {
  const [projects, setProjects] = useState<ProjectVM[]>(sessionListVM.projects)
  const [activeGroup, setActiveGroup] = useState<string>('all')
  const [showSheet, setShowSheet] = useState(false)

  useEffect(() => {
    setProjects(sessionListVM.projects)
    const unsub = sessionListVM.onProjectsChanged(p => setProjects([...p]))
    return unsub
  }, [sessionListVM])

  // Flatten all sessions
  const allSessions: Array<{ session: SessionVM; projectName: string }> = []
  for (const p of projects) {
    for (const s of p.sessions) {
      allSessions.push({ session: s, projectName: p.name })
    }
  }

  // Badge count — sessions needing attention
  const badge = allSessions.filter(
    ({ session: s }) => s.state === 'requires_action' || s.hasPendingPermission
  ).length

  const STATE_LABELS: Record<string, string> = {
    working: 'Working',
    running: 'Working',
    requires_action: 'Needs permission',
    idle: 'Idle',
    restarting: 'Restarting',
    failed: 'Failed',
    unknown: 'Unknown',
    waiting_for_input: 'Waiting for input',
  }

  const displayed = activeGroup === 'all'
    ? allSessions
    : allSessions.filter(({ session }) => session.name.includes(activeGroup))

  // Group names for filter chips (use project names as groups)
  const groups = ['all', ...Array.from(new Set(allSessions.map(({ projectName }) => projectName))).sort()]
  const showChips = groups.length > 2 // more than just 'all' + one group

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--bg)' }}>
      <NavBar
        title="MClaude"
        badge={badge}
        connected={connected}
        onSettings={onSettings}
        onUsage={onUsage}
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

      {/* Session list */}
      <div style={{ flex: 1, overflowY: 'auto' }}>
        {displayed.length === 0 ? (
          <div style={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            justifyContent: 'center',
            height: '100%',
            gap: 8,
            color: 'var(--text2)',
          }}>
            <div style={{ fontSize: 18, fontWeight: 600, color: 'var(--text)' }}>No Sessions</div>
            <div style={{ fontSize: 14 }}>
              {activeGroup !== 'all' ? 'No sessions in this group' : 'Tap + to start a session'}
            </div>
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
              <StatusDot state={session.state as 'idle' | 'running' | 'requires_action' | 'restarting' | 'failed'} size={12} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ color: 'var(--text)', fontWeight: 500, fontSize: 15 }}>
                  {session.name || projectName}
                </div>
                <div style={{ color: 'var(--text2)', fontSize: 13, marginTop: 2 }}>
                  {STATE_LABELS[session.state] ?? session.state}
                  {session.name ? ` · ${projectName}` : ''}
                </div>
              </div>
              <span style={{ color: 'var(--text3)', fontSize: 18 }}>›</span>
            </button>
          ))
        )}
      </div>

      {/* FAB */}
      <button
        onClick={() => setShowSheet(true)}
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

      {showSheet && (
        <NewSessionSheet
          sessionListVM={sessionListVM}
          onClose={() => setShowSheet(false)}
        />
      )}
    </div>
  )
}
