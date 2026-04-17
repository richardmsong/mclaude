// @vitest-environment jsdom

import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { DashboardScreen } from './DashboardScreen'
import { SessionListVM } from '@/viewmodels/session-list-vm'
import { SessionStore } from '@/stores/session-store'
import { HeartbeatMonitor } from '@/stores/heartbeat-monitor'
import { MockNATSClient } from '@/testutil/mock-nats'
import { makeSessionKVState, makeProjectKVState } from '@/testutil/fixtures'
import type { IStorage } from '@/viewmodels/session-list-vm'

// Suppress logger output in tests
vi.mock('@/logger', () => ({
  logger: { info: vi.fn(), error: vi.fn(), debug: vi.fn(), warn: vi.fn() },
}))

/** In-memory localStorage substitute. */
function makeMemoryStorage(): IStorage {
  const store = new Map<string, string>()
  return {
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => { store.set(k, v) },
    removeItem: (k) => { store.delete(k) },
  }
}

describe('DashboardScreen — project filter', () => {
  let mockNats: MockNATSClient
  let sessionStore: SessionStore
  let heartbeat: HeartbeatMonitor
  let vm: SessionListVM
  let storage: IStorage

  beforeEach(() => {
    storage = makeMemoryStorage()
    mockNats = new MockNATSClient()
    sessionStore = new SessionStore(mockNats, 'user-1')
    sessionStore.startWatching()
    heartbeat = new HeartbeatMonitor(mockNats, 'user-1')
    vm = new SessionListVM(sessionStore, heartbeat, mockNats, 'user-1', storage)
  })

  afterEach(() => {
    vm.destroy()
  })

  it('shows sessions for the selected project after applying filter', () => {
    // Set up two projects; p-1 has sessions, p-2 has none.
    mockNats.kvSet('mclaude-projects', 'user-1.p-1', makeProjectKVState({ id: 'p-1', name: 'mclaude' }))
    mockNats.kvSet('mclaude-projects', 'user-1.p-2', makeProjectKVState({ id: 'p-2', name: 'other' }))
    mockNats.kvSet('mclaude-sessions', 'user-1.p-1.sess-1', makeSessionKVState({ id: 'sess-1', projectId: 'p-1', name: 'Session One' }))
    mockNats.kvSet('mclaude-sessions', 'user-1.p-1.sess-2', makeSessionKVState({ id: 'sess-2', projectId: 'p-1', name: 'Session Two' }))

    const { rerender } = render(
      <DashboardScreen
        sessionListVM={vm}
        connected={true}
        onSelectSession={vi.fn()}
        onSettings={vi.fn()}
        onUsage={vi.fn()}
      />
    )

    // Unfiltered: both sessions visible
    expect(screen.queryByText('No sessions in this project')).toBeNull()
    expect(screen.getByText('Session One')).toBeTruthy()
    expect(screen.getByText('Session Two')).toBeTruthy()

    // Apply filter to p-1
    act(() => {
      vm.setFilter('p-1')
    })

    rerender(
      <DashboardScreen
        sessionListVM={vm}
        connected={true}
        onSelectSession={vi.fn()}
        onSettings={vi.fn()}
        onUsage={vi.fn()}
      />
    )

    // After filtering: sessions must still be visible, NOT show empty state
    expect(screen.queryByText('No sessions in this project')).toBeNull()
    expect(screen.getByText('Session One')).toBeTruthy()
    expect(screen.getByText('Session Two')).toBeTruthy()
  })

  it('shows empty state only when the filtered project genuinely has no sessions', () => {
    mockNats.kvSet('mclaude-projects', 'user-1.p-1', makeProjectKVState({ id: 'p-1', name: 'mclaude' }))
    mockNats.kvSet('mclaude-projects', 'user-1.p-2', makeProjectKVState({ id: 'p-2', name: 'empty-project' }))
    mockNats.kvSet('mclaude-sessions', 'user-1.p-1.sess-1', makeSessionKVState({ id: 'sess-1', projectId: 'p-1', name: 'Session One' }))

    // Filter to p-2 which has no sessions
    act(() => {
      vm.setFilter('p-2')
    })

    render(
      <DashboardScreen
        sessionListVM={vm}
        connected={true}
        onSelectSession={vi.fn()}
        onSettings={vi.fn()}
        onUsage={vi.fn()}
      />
    )

    // p-2 has no sessions, so the empty state IS correct
    expect(screen.getByText('No sessions in this project')).toBeTruthy()
  })
})
