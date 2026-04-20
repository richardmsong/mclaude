// @vitest-environment jsdom
// Tests for ADR-0022: Stop button visibility predicate fix.
// The Stop button must appear for every state where Claude is actively processing
// a turn -- not just 'running'.

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SessionDetailScreen } from './SessionDetailScreen'
import { ConversationVM } from '@/viewmodels/conversation-vm'
import { EventStore } from '@/stores/event-store'
import { SessionStore } from '@/stores/session-store'
import { MockNATSClient } from '@/testutil/mock-nats'
import { makeSessionKVState } from '@/testutil/fixtures'
import type { SessionState } from '@/types'

// Suppress logger output in tests
vi.mock('@/logger', () => ({
  logger: { info: vi.fn(), error: vi.fn(), debug: vi.fn(), warn: vi.fn() },
}))

// xterm is not available in jsdom -- stub it so TerminalTab does not blow up.
vi.mock('@xterm/xterm', () => ({ Terminal: class { open() {} write() {} dispose() {} onData() { return { dispose() {} } } } }))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { activate() {} fit() {} dispose() {} } }))

function makeVM(mockNats: MockNATSClient) {
  const sessionState = makeSessionKVState({ id: 'sess-1', projectId: 'proj-1' })
  mockNats.kvSet('mclaude-sessions', 'user-1.proj-1.sess-1', sessionState)
  const sessionStore = new SessionStore(mockNats, 'user-1')
  sessionStore.startWatching()
  const eventStore = new EventStore({
    natsClient: mockNats,
    userId: 'user-1',
    projectId: 'proj-1',
    sessionId: 'sess-1',
  })
  const vm = new ConversationVM(eventStore, sessionStore, mockNats, 'user-1', 'proj-1', 'sess-1')
  return vm
}

function renderScreen(state: SessionState) {
  const mockNats = new MockNATSClient()
  const vm = makeVM(mockNats)
  return render(
    <SessionDetailScreen
      sessionId="sess-1"
      sessionName="Test Session"
      sessionState={state}
      conversationVM={vm}
      onBack={vi.fn()}
      connected={true}
    />
  )
}

describe('SessionDetailScreen -- Stop button visibility (ADR-0022)', () => {
  // States where Claude is actively processing a turn -- Stop button must be visible.
  const workingStates: SessionState[] = ['running', 'requires_action', 'plan_mode', 'waiting_for_input']

  // States where Claude is NOT processing a turn -- Stop button must be hidden.
  const idleStates: SessionState[] = ['idle', 'updating', 'restarting', 'failed', 'unknown']

  for (const state of workingStates) {
    it(`shows Stop button when sessionState="${state}"`, () => {
      renderScreen(state)
      const buttons = screen.getAllByRole('button')
      const stopButton = buttons.find(b => b.textContent?.trim() === '\u2715' && b.getAttribute('title') !== 'Attach image')
      expect(stopButton, `Stop button should be visible when state="${state}"`).toBeTruthy()
    })
  }

  for (const state of idleStates) {
    it(`hides Stop button when sessionState="${state}"`, () => {
      renderScreen(state)
      const buttons = screen.queryAllByRole('button')
      const stopLikeButtons = buttons.filter(b => {
        const text = b.textContent?.trim()
        const style = b.getAttribute('style') ?? ''
        // The Stop button has round red style with border-radius: 50%
        return text === '\u2715' && style.includes('border-radius: 50%')
      })
      expect(stopLikeButtons.length, `Stop button should be hidden when state="${state}"`).toBe(0)
    })
  }
})

describe('SessionDetailScreen -- Stop button publishes interrupt on click', () => {
  it('calls conversationVM.interrupt() when Stop button is clicked in running state', async () => {
    const mockNats = new MockNATSClient()
    const vm = makeVM(mockNats)
    const interruptSpy = vi.spyOn(vm, 'interrupt')

    render(
      <SessionDetailScreen
        sessionId="sess-1"
        sessionName="Test Session"
        sessionState="running"
        conversationVM={vm}
        onBack={vi.fn()}
        connected={true}
      />
    )

    const buttons = screen.getAllByRole('button')
    const stopButton = buttons.find(b => b.textContent?.trim() === '\u2715' && b.getAttribute('title') !== 'Attach image')
    expect(stopButton).toBeTruthy()
    stopButton!.click()
    expect(interruptSpy).toHaveBeenCalledOnce()
  })

  it('calls conversationVM.interrupt() when Stop button is clicked in requires_action state', async () => {
    const mockNats = new MockNATSClient()
    const vm = makeVM(mockNats)
    const interruptSpy = vi.spyOn(vm, 'interrupt')

    render(
      <SessionDetailScreen
        sessionId="sess-1"
        sessionName="Test Session"
        sessionState="requires_action"
        conversationVM={vm}
        onBack={vi.fn()}
        connected={true}
      />
    )

    const buttons = screen.getAllByRole('button')
    // In requires_action, the action bar has Cancel (\u2715) and the input bar Stop (\u2715)
    // Stop button is identified by round red style (border-radius: 50%)
    const stopButton = buttons.find(b => {
      const text = b.textContent?.trim()
      const style = b.getAttribute('style') ?? ''
      return text === '\u2715' && style.includes('border-radius: 50%')
    })
    expect(stopButton, 'Stop button must be visible in requires_action').toBeTruthy()
    stopButton!.click()
    expect(interruptSpy).toHaveBeenCalledOnce()
  })
})
