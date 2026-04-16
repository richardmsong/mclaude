import type { ProjectVM } from '@/viewmodels/session-list-vm'

interface ProjectFilterSheetProps {
  projects: ProjectVM[]
  activeFilterId: string
  onSelect: (projectId: string) => void
  onClose: () => void
}

export function ProjectFilterSheet({
  projects,
  activeFilterId,
  onSelect,
  onClose,
}: ProjectFilterSheetProps) {
  const sorted = [...projects].sort((a, b) => a.name.localeCompare(b.name))

  const RadioIcon = ({ filled }: { filled: boolean }) => (
    <span style={{
      display: 'inline-block',
      width: 18,
      height: 18,
      borderRadius: '50%',
      border: `2px solid ${filled ? 'var(--blue)' : 'var(--text3)'}`,
      background: filled ? 'var(--blue)' : 'none',
      flexShrink: 0,
      position: 'relative',
    }}>
      {filled && (
        <span style={{
          position: 'absolute',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%, -50%)',
          width: 7,
          height: 7,
          borderRadius: '50%',
          background: '#fff',
          display: 'block',
        }} />
      )}
    </span>
  )

  const handleSelect = (projectId: string) => {
    onSelect(projectId)
    onClose()
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
          <div style={{ fontWeight: 600, fontSize: 16 }}>Filter by Project</div>
          <button onClick={onClose} style={{ color: 'var(--text2)', fontSize: 18 }}>&#x2715;</button>
        </div>

        {/* List */}
        <div style={{ overflowY: 'auto', flex: 1 }}>
          {/* All Projects row */}
          <button
            onClick={() => handleSelect('')}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 12,
              width: '100%',
              padding: '14px 16px',
              borderBottom: '1px solid var(--border)',
              background: 'none',
              textAlign: 'left',
            }}
          >
            <RadioIcon filled={!activeFilterId} />
            <span style={{ color: 'var(--text)', fontSize: 15 }}>All Projects</span>
          </button>

          {sorted.map(project => (
            <button
              key={project.id}
              onClick={() => handleSelect(project.id)}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 12,
                width: '100%',
                padding: '14px 16px',
                borderBottom: '1px solid var(--border)',
                background: 'none',
                textAlign: 'left',
              }}
            >
              <RadioIcon filled={activeFilterId === project.id} />
              <span style={{ color: 'var(--text)', fontSize: 15 }}>{project.name}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}
