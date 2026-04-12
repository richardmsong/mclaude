import { useState } from 'react'
import type { SessionListVM } from '@/viewmodels/session-list-vm'

interface NewProjectSheetProps {
  sessionListVM: SessionListVM
  onClose: () => void
  onCreated?: (projectId: string) => void
}

export function NewProjectSheet({ sessionListVM, onClose, onCreated }: NewProjectSheetProps) {
  const [name, setName] = useState('')
  const [gitUrl, setGitUrl] = useState('')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) return
    setCreating(true)
    setError('')
    try {
      const projectId = await sessionListVM.createProject(name.trim(), gitUrl.trim() || undefined)
      onCreated?.(projectId)
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create project')
      setCreating(false)
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
      <div style={{ position: 'absolute', inset: 0, background: 'rgba(0,0,0,0.5)' }} />

      <div
        style={{
          position: 'relative',
          background: 'var(--surf)',
          borderRadius: '16px 16px 0 0',
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
          <div style={{ fontWeight: 600, fontSize: 16 }}>New Project</div>
          <button onClick={onClose} style={{ color: 'var(--text2)', fontSize: 18 }}>✕</button>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div>
            <div style={{ fontSize: 12, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.5px', color: 'var(--text2)', marginBottom: 8 }}>
              Name
            </div>
            <input
              autoFocus
              type="text"
              placeholder="My Project"
              value={name}
              onChange={e => setName(e.target.value)}
              style={inputStyle}
            />
          </div>

          <div>
            <div style={{ fontSize: 12, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.5px', color: 'var(--text2)', marginBottom: 8 }}>
              Git Repository <span style={{ fontWeight: 400, textTransform: 'none', letterSpacing: 0 }}>(optional)</span>
            </div>
            <input
              type="text"
              placeholder="https://github.com/org/repo"
              value={gitUrl}
              onChange={e => setGitUrl(e.target.value)}
              style={inputStyle}
            />
            <div style={{ fontSize: 12, color: 'var(--text2)', marginTop: 6 }}>
              Clone a repo, or leave blank to start from scratch.
            </div>
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

const inputStyle: React.CSSProperties = {
  width: '100%',
  padding: '11px 14px',
  background: 'var(--surf2)',
  border: '1px solid var(--border)',
  borderRadius: 10,
  color: 'var(--text)',
  fontSize: 15,
}
