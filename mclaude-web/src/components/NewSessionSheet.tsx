import { useEffect, useState } from 'react'
import type { SessionListVM, ProjectVM } from '@/viewmodels/session-list-vm'

const LAST_PROJECT_KEY = 'mclaude.lastProjectId'

interface NewSessionSheetProps {
  sessionListVM: SessionListVM
  onClose: () => void
  onSessionCreated?: (sessionId: string) => void
}

function sortProjects(projects: ProjectVM[]): ProjectVM[] {
  const lastId = localStorage.getItem(LAST_PROJECT_KEY)
  return [...projects].sort((a, b) => {
    if (a.id === lastId) return -1
    if (b.id === lastId) return 1
    return a.name.localeCompare(b.name)
  })
}

export function NewSessionSheet({ sessionListVM, onClose, onSessionCreated }: NewSessionSheetProps) {
  const [projects, setProjects] = useState<ProjectVM[]>(() => sortProjects(sessionListVM.projects))
  const [creating, setCreating] = useState<string | null>(null)

  useEffect(() => {
    setProjects(sortProjects(sessionListVM.projects))
    const unsub = sessionListVM.onProjectsChanged(p => setProjects(sortProjects(p)))
    return unsub
  }, [sessionListVM])

  const handleSelect = async (projectId: string) => {
    setCreating(projectId)
    try {
      const sessionId = await sessionListVM.createSession(projectId, 'main', 'new-session')
      localStorage.setItem(LAST_PROJECT_KEY, projectId)
      onSessionCreated?.(sessionId)
      onClose()
    } catch {
      setCreating(null)
    }
  }

  return (
    <div
      style={{
        position: 'fixed', inset: 0, zIndex: 200,
        display: 'flex', flexDirection: 'column', justifyContent: 'flex-end',
      }}
      onClick={onClose}
    >
      {/* Scrim */}
      <div style={{
        position: 'absolute', inset: 0,
        background: 'rgba(0,0,0,0.5)',
      }} />

      {/* Sheet */}
      <div
        style={{
          position: 'relative',
          background: 'var(--surf)',
          borderRadius: '16px 16px 0 0',
          maxHeight: '70vh',
          overflow: 'hidden',
          display: 'flex',
          flexDirection: 'column',
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
          <div style={{ fontWeight: 600, fontSize: 16 }}>New Session</div>
          <button onClick={onClose} style={{ color: 'var(--text2)', fontSize: 18 }}>✕</button>
        </div>

        {/* Project list */}
        <div style={{ overflowY: 'auto', flex: 1 }}>
          {projects.length === 0 && (
            <div style={{
              padding: 24,
              textAlign: 'center',
              color: 'var(--text2)',
            }}>
              No projects found
            </div>
          )}
          {projects.map(project => (
            <button
              key={project.id}
              onClick={() => handleSelect(project.id)}
              disabled={creating !== null}
              style={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'flex-start',
                padding: '12px 16px',
                width: '100%',
                borderBottom: '1px solid var(--border)',
                opacity: creating === project.id ? 0.6 : 1,
                background: 'none',
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span>📁</span>
                <span style={{ color: 'var(--text)', fontWeight: 500 }}>{project.name}</span>
                {creating === project.id && (
                  <span style={{ color: 'var(--text3)', fontSize: 12 }}>creating…</span>
                )}
              </div>
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}
