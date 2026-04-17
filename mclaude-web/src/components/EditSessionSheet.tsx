import { useState } from 'react'
import type { SessionListVM } from '@/viewmodels/session-list-vm'

interface EditSessionSheetProps {
  sessionId: string
  currentExtraFlags: string
  sessionListVM: SessionListVM
  onClose: () => void
}

export function EditSessionSheet({ sessionId, currentExtraFlags, sessionListVM, onClose }: EditSessionSheetProps) {
  const [extraFlagsText, setExtraFlagsText] = useState(currentExtraFlags)
  const [restarting, setRestarting] = useState(false)

  const handleRestart = async () => {
    setRestarting(true)
    try {
      await sessionListVM.restartSession(sessionId, { extraFlags: extraFlagsText.trim() || undefined })
    } finally {
      setRestarting(false)
      onClose()
    }
  }

  return (
    <div
      style={{
        position: 'fixed', inset: 0, zIndex: 300,
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
          <div style={{ fontWeight: 600, fontSize: 16 }}>Edit Session</div>
          <button onClick={onClose} style={{ color: 'var(--text2)', fontSize: 18 }}>✕</button>
        </div>

        {/* Body */}
        <div style={{ padding: '16px 16px 8px' }}>
          <label style={{ display: 'block', fontSize: 12, color: 'var(--text2)', marginBottom: 4 }}>
            Extra flags
          </label>
          <textarea
            value={extraFlagsText}
            onChange={e => setExtraFlagsText(e.target.value)}
            rows={4}
            style={{
              width: '100%',
              resize: 'vertical',
              fontSize: 12,
              fontFamily: 'monospace',
              padding: '6px 8px',
              background: 'var(--surf2)',
              color: 'var(--text)',
              border: '1px solid var(--border)',
              borderRadius: 4,
              boxSizing: 'border-box',
            }}
          />
        </div>

        {/* Footer */}
        <div style={{ padding: '8px 16px 24px' }}>
          <button
            onClick={handleRestart}
            disabled={restarting}
            style={{
              width: '100%',
              padding: '12px 0',
              background: 'var(--blue)',
              color: '#fff',
              borderRadius: 10,
              fontWeight: 600,
              fontSize: 15,
              opacity: restarting ? 0.6 : 1,
            }}
          >
            {restarting ? 'Restarting…' : 'Restart Session'}
          </button>
        </div>
      </div>
    </div>
  )
}
