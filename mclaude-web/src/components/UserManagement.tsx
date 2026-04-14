import { NavBar } from './NavBar'

interface User {
  email: string
  name: string
  role: 'admin' | 'user'
}

interface UserManagementProps {
  connected: boolean
  onBack: () => void
}

export function UserManagement({ connected, onBack }: UserManagementProps) {
  // In production this would be loaded from the control plane API.
  // Showing placeholder UI per spec.
  const users: User[] = []

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--bg)' }}>
      <NavBar title="Users" onBack={onBack} connected={connected} />

      <div style={{ flex: 1, overflowY: 'auto', padding: '16px' }}>
        {/* Invite button */}
        <button
          style={{
            width: '100%',
            padding: '14px',
            background: 'var(--surf)',
            border: '1px solid var(--border)',
            borderRadius: 12,
            color: 'var(--blue)',
            fontWeight: 500,
            fontSize: 15,
            display: 'flex',
            alignItems: 'center',
            gap: 10,
            marginBottom: 16,
          }}
        >
          <span style={{ fontSize: 18, lineHeight: 1 }}>+</span>
          Invite User
        </button>

        {/* User list */}
        {users.length === 0 ? (
          <div style={{ color: 'var(--text2)', fontSize: 14, textAlign: 'center', marginTop: 32 }}>
            No users yet
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
            {users.map(user => (
              <div
                key={user.email}
                style={{
                  padding: '12px 16px',
                  background: 'var(--surf)',
                  borderBottom: '1px solid var(--border)',
                }}
              >
                <div style={{ color: 'var(--text)', fontSize: 15, fontWeight: 500 }}>
                  {user.email}
                </div>
                <div style={{ color: 'var(--text2)', fontSize: 13, marginTop: 2 }}>
                  {user.name} · {user.role}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
