import { describe, it, expect, beforeEach } from 'vitest'
import { EventStore } from './event-store'
import { MockNATSClient } from '../testutil/mock-nats'

const SUBJECT = 'mclaude.users.user-1.hosts.local.projects.project-1.sessions.session-1.events'

function makeStore(mockNats: MockNATSClient): EventStore {
  return new EventStore({
    natsClient: mockNats,
    userId: 'user-1',
    projectId: 'project-1',
    sessionId: 'session-1',
  })
}

describe('EventStore reconnect', () => {
  let mockNats: MockNATSClient
  let store: EventStore

  beforeEach(() => {
    mockNats = new MockNATSClient()
    store = makeStore(mockNats)
    mockNats.clearRecorded()
  })

  it('noGapsAfterReconnect: events before and after reconnect all present', () => {
    store.start()

    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 1' } }, 1)
    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 2' } }, 2)
    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 3' } }, 3)

    expect(store.conversation.turns).toHaveLength(3)
    expect(store.lastSequence).toBe(3)

    // Simulate reconnect — caller stops and restarts from seq 4 (no overlap)
    store.stop()
    store.start(4)

    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 4' } }, 4)
    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 5' } }, 5)

    // All 5 turns should be present, no gaps
    expect(store.conversation.turns).toHaveLength(5)
    expect(store.lastSequence).toBe(5)
  })

  it('deduplicationOnReconnect: overlapping replay does not create duplicate turns', () => {
    store.start()

    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 1' } }, 1)
    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 2' } }, 2)
    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 3' } }, 3)

    expect(store.conversation.turns).toHaveLength(3)
    expect(store.lastSequence).toBe(3)

    // Reconnect from seq 2 (overlap: server will replay 2 and 3)
    store.stop()
    store.start(2)

    // Duplicate replays of seq 2 and 3 — should be deduplicated
    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 2' } }, 2)
    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 3' } }, 3)
    // seq 4 is new
    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 4' } }, 4)

    // Should be 4 turns (3 original + 1 new), not 6
    expect(store.conversation.turns).toHaveLength(4)
    expect(store.lastSequence).toBe(4)
  })

  it('replayFromSeqRespected: caller computes max(lastSeq+1, replayFromSeq) correctly', () => {
    store.start()

    // Feed events up to seq 5
    for (let i = 1; i <= 5; i++) {
      mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: `msg ${i}` } }, i)
    }

    expect(store.lastSequence).toBe(5)
    const replayFromSeq = 3

    // Caller computes: max(lastSeq+1, replayFromSeq) = max(6, 3) = 6
    const startSeq = Math.max(store.lastSequence + 1, replayFromSeq)
    expect(startSeq).toBe(6)

    store.stop()
    store.start(startSeq)

    // Only seq 6 is new; seq 3–5 would be deduplicated even if server sent them
    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 6' } }, 6)

    expect(store.conversation.turns).toHaveLength(6)
    expect(store.lastSequence).toBe(6)
  })

  it('natsDisconnectSignal: events do not flow after simulateDisconnect; flow resumes after restart', () => {
    store.start()

    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 1' } }, 1)
    expect(store.conversation.turns).toHaveLength(1)

    // Disconnect breaks the subscription
    mockNats.simulateDisconnect()
    store.stop()

    // Attempt to send while stopped — subscriber is gone
    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 2' } }, 2)
    expect(store.conversation.turns).toHaveLength(1) // not received

    // Caller re-subscribes after reconnect signal
    mockNats.simulateReconnect()
    store.start(2)

    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 2' } }, 2)
    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 3' } }, 3)

    expect(store.conversation.turns).toHaveLength(3)
  })

  it('lastSequence persists across stop/start so dedup works after reconnect', () => {
    store.start()

    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 1' } }, 10)
    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 2' } }, 20)

    expect(store.lastSequence).toBe(20)

    store.stop()
    store.start()

    // lastSequence is still 20 after restart — old events deduplicated
    expect(store.lastSequence).toBe(20)

    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 2 dup' } }, 20)
    expect(store.conversation.turns).toHaveLength(2) // no duplicate

    mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: 'msg 3' } }, 21)
    expect(store.conversation.turns).toHaveLength(3)
  })

  it('jsSubscribe is used with startSeq=0 on fresh start', () => {
    store.start()
    expect(mockNats.jsSubscribeCalls).toHaveLength(1)
    expect(mockNats.jsSubscribeCalls[0].startSeq).toBe(0)
    expect(mockNats.jsSubscribeCalls[0].subject).toBe(SUBJECT)
  })

  it('jsSubscribe is called with provided replayFromSeq on start', () => {
    store.start(7)
    expect(mockNats.jsSubscribeCalls).toHaveLength(1)
    expect(mockNats.jsSubscribeCalls[0].startSeq).toBe(7)
  })

  it('jsSubscribe called with max(lastSeq+1, replayFromSeq) on reconnect', () => {
    store.start()
    for (let i = 1; i <= 5; i++) {
      mockNats.simulateReceive(SUBJECT, { type: 'user', message: { role: 'user', content: `msg ${i}` } }, i)
    }
    expect(store.lastSequence).toBe(5)
    mockNats.clearRecorded()

    // Reconnect with replayFromSeq=3; max(5+1, 3) = 6
    store.stop()
    const startSeq = Math.max(store.lastSequence + 1, 3)
    store.start(startSeq)

    expect(mockNats.jsSubscribeCalls).toHaveLength(1)
    expect(mockNats.jsSubscribeCalls[0].startSeq).toBe(6)
  })
})
