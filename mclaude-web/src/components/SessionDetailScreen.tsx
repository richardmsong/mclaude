import { useEffect, useRef, useState } from 'react'
import { NavBar } from './NavBar'
import { StatusDot } from './StatusDot'
import { EventList } from './events/EventList'
import type { Turn, SessionState } from '@/types'
import type { ConversationVM, ConversationVMState } from '@/viewmodels/conversation-vm'

interface SessionDetailScreenProps {
  sessionId: string
  sessionName: string
  sessionState: SessionState
  conversationVM: ConversationVM
  onBack: () => void
  connected: boolean
  initialMessage?: string
  onInitialMessageSent?: () => void
}

const STATE_LABELS: Record<string, string> = {
  running: 'Working',
  requires_action: 'Needs permission',
  idle: 'Idle',
  restarting: 'Restarting',
  failed: 'Failed',
}

export function SessionDetailScreen({
  sessionId,
  sessionName,
  sessionState,
  conversationVM,
  onBack,
  connected,
  initialMessage,
  onInitialMessageSent,
}: SessionDetailScreenProps) {
  const [vmState, setVmState] = useState<ConversationVMState>(conversationVM.state)
  const [activeTab, setActiveTab] = useState<'events' | 'terminal'>('events')
  const [input, setInput] = useState('')
  const [sending, setSending] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)
  const atBottomRef = useRef(true)
  const initialMessageSentRef = useRef(false)

  // Send pre-seeded onboarding message once the session is ready
  useEffect(() => {
    if (!initialMessage || initialMessageSentRef.current) return
    // Wait a tick for the EventStore to subscribe before publishing
    const timer = setTimeout(() => {
      initialMessageSentRef.current = true
      conversationVM.sendMessage(initialMessage)
      onInitialMessageSent?.()
    }, 500)
    return () => clearTimeout(timer)
  }, [initialMessage, conversationVM, onInitialMessageSent])

  useEffect(() => {
    setVmState(conversationVM.state)
    const unsub = conversationVM.onStateChanged(s => {
      setVmState({ ...s })
      if (atBottomRef.current) {
        requestAnimationFrame(() => {
          if (scrollRef.current) {
            scrollRef.current.scrollTop = scrollRef.current.scrollHeight
          }
        })
      }
    })
    return unsub
  }, [conversationVM])

  const turns: Turn[] = vmState.turns

  const handleScroll = () => {
    if (!scrollRef.current) return
    const el = scrollRef.current
    atBottomRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 100
  }

  const handleSend = async () => {
    const text = input.trim()
    if (!text || sending) return
    setInput('')
    setSending(true)
    try {
      conversationVM.sendMessage(text)
    } finally {
      setSending(false)
    }
    // Scroll to bottom after send
    requestAnimationFrame(() => {
      if (scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    })
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void handleSend()
    }
  }

  const isWorking = sessionState === 'running'
  const needsPermission = sessionState === 'requires_action'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--bg)' }}>
      <NavBar
        title={sessionName}
        onBack={onBack}
        connected={connected}
      />

      {/* Det-meta row */}
      <div style={{
        padding: '8px 16px',
        display: 'flex',
        alignItems: 'center',
        gap: 8,
        borderBottom: '1px solid var(--border)',
        background: 'var(--surf)',
        flexShrink: 0,
      }}>
        <StatusDot state={sessionState} size={10} />
        <span style={{ color: 'var(--text2)', fontSize: 13 }}>
          {STATE_LABELS[sessionState] ?? sessionState} · #{sessionId.slice(0, 8)}
        </span>
      </div>

      {/* Action bar (needs_permission) */}
      {needsPermission && (
        <div style={{
          display: 'flex',
          gap: 8,
          padding: '8px 16px',
          borderBottom: '1px solid var(--border)',
          background: 'var(--surf)',
          flexShrink: 0,
        }}>
          <button
            onClick={() => {
              const pending = turns
                .flatMap(t => t.blocks)
                .find(b => b.type === 'control_request' && (b as { status: string }).status === 'pending')
              if (pending && 'requestId' in pending) {
                conversationVM.approvePermission(pending.requestId as string)
              }
            }}
            style={{
              flex: 1,
              padding: '8px 0',
              background: 'var(--green)',
              color: '#000',
              borderRadius: 8,
              fontWeight: 600,
            }}
          >
            ✓ Approve
          </button>
          <button
            onClick={() => {
              const pending = turns
                .flatMap(t => t.blocks)
                .find(b => b.type === 'control_request' && (b as { status: string }).status === 'pending')
              if (pending && 'requestId' in pending) {
                conversationVM.denyPermission(pending.requestId as string)
              }
            }}
            style={{
              flex: 1,
              padding: '8px 0',
              background: 'var(--surf2)',
              color: 'var(--red)',
              borderRadius: 8,
              fontWeight: 600,
            }}
          >
            ✕ Cancel
          </button>
        </div>
      )}

      {/* Tab bar */}
      <div style={{
        display: 'flex',
        borderBottom: '1px solid var(--border)',
        background: 'var(--surf)',
        flexShrink: 0,
      }}>
        {(['events', 'terminal'] as const).map(tab => (
          <button
            key={tab}
            onClick={() => setActiveTab(tab)}
            style={{
              flex: 1,
              padding: '10px 0',
              fontSize: 14,
              fontWeight: 500,
              color: activeTab === tab ? 'var(--text)' : 'var(--text2)',
              borderBottom: activeTab === tab ? '2px solid var(--blue)' : '2px solid transparent',
              textTransform: 'capitalize',
            }}
          >
            {tab === 'events' ? 'Events' : 'Terminal'}
          </button>
        ))}
      </div>

      {/* Content */}
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        style={{ flex: 1, overflowY: 'auto', padding: '12px 16px' }}
      >
        {activeTab === 'events' ? (
          <EventList
            turns={turns}
            onApprove={id => conversationVM.approvePermission(id)}
            onDeny={id => conversationVM.denyPermission(id)}
          />
        ) : (
          <div style={{
            background: '#000',
            borderRadius: 8,
            padding: 12,
            fontFamily: "'Menlo','Courier New',monospace",
            fontSize: 13,
            color: '#eee',
            minHeight: 200,
          }}>
            <div style={{ color: 'var(--text3)', fontSize: 12, marginBottom: 8 }}>
              Terminal — connect via NATS
            </div>
          </div>
        )}
      </div>

      {/* Input bar (Events tab only) */}
      {activeTab === 'events' && (
        <div style={{
          background: 'var(--surf)',
          borderTop: '1px solid var(--border)',
          padding: '8px 12px',
          display: 'flex',
          alignItems: 'flex-end',
          gap: 8,
          flexShrink: 0,
        }}>
          {/* Stop button */}
          {isWorking && (
            <button
              onClick={() => conversationVM.interrupt()}
              style={{
                width: 32,
                height: 32,
                borderRadius: '50%',
                background: 'var(--surf2)',
                color: 'var(--red)',
                flexShrink: 0,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              ✕
            </button>
          )}

          {/* Textarea */}
          <textarea
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Message… or / for skills"
            rows={1}
            style={{
              flex: 1,
              background: 'var(--surf2)',
              border: '1px solid var(--border)',
              borderRadius: 20,
              padding: '7px 14px',
              color: 'var(--text)',
              fontSize: 15,
              resize: 'none',
              minHeight: 36,
              maxHeight: 120,
              overflowY: 'auto',
              lineHeight: 1.4,
            }}
          />

          {/* Send button */}
          <button
            onClick={() => void handleSend()}
            disabled={!input.trim() || sending}
            style={{
              width: 32,
              height: 32,
              borderRadius: '50%',
              background: input.trim() ? 'var(--blue)' : 'var(--surf3)',
              color: '#fff',
              flexShrink: 0,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: 16,
              transition: 'background 0.15s',
            }}
          >
            ↑
          </button>
        </div>
      )}
    </div>
  )
}
