import { useEffect, useLayoutEffect, useRef, useState, useCallback } from 'react'
import { NavBar } from './NavBar'
import { StatusDot } from './StatusDot'
import { EventList } from './events/EventList'
import { EditSessionSheet } from './EditSessionSheet'
import { TerminalTab } from './TerminalTab'
import type { Turn, SessionState, PendingMessage } from '@/types'
import type { ConversationVM, ConversationVMState } from '@/viewmodels/conversation-vm'
import type { SessionListVM } from '@/viewmodels/session-list-vm'
import type { TerminalVM } from '@/viewmodels/terminal-vm'
import { PRICE_PER_M, formatTokens, formatCost } from '@/lib/pricing'

interface SessionUsage {
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheWriteTokens: number
  costUsd: number
  // M4: context meter fields — optional, populated when server provides usage data
  contextTokensUsed?: number
  contextWindowSize?: number
}

interface SessionDetailScreenProps {
  sessionId: string
  sessionName: string
  sessionState: SessionState
  sessionModel?: string
  sessionUsage?: SessionUsage
  sessionExtraFlags?: string
  sessionListVM?: SessionListVM
  conversationVM: ConversationVM
  terminalVm?: TerminalVM
  onBack: () => void
  connected: boolean
  initialMessage?: string
  onInitialMessageSent?: () => void
}

const STATE_LABELS: Record<string, string> = {
  running: 'Working',
  requires_action: 'Needs permission',
  plan_mode: 'Plan mode',
  idle: 'Idle',
  updating: 'Updating...',
  restarting: 'Restarting',
  failed: 'Failed',
  unknown: 'Unknown',
  waiting_for_input: 'Waiting for input',
}

// Tab memory: persist active tab across navigation
const TAB_STORAGE_KEY = 'mclaude.activeTab'

function getStoredTab(): 'events' | 'terminal' {
  try {
    const stored = sessionStorage.getItem(TAB_STORAGE_KEY)
    if (stored === 'terminal') return 'terminal'
  } catch {}
  return 'events'
}

function storeTab(tab: 'events' | 'terminal'): void {
  try {
    sessionStorage.setItem(TAB_STORAGE_KEY, tab)
  } catch {}
}

// Scroll persistence: module-level map — cleared on every page refresh, persists for in-app navigation
const scrollPositions = new Map<string, number>()

// Skills autocomplete: filter skills by query
function filterSkills(skills: string[], query: string): string[] {
  const lq = query.toLowerCase()
  return skills.filter(s => s.toLowerCase().includes(lq))
}

export function SessionDetailScreen({
  sessionId,
  sessionName,
  sessionState,
  sessionModel,
  sessionUsage,
  sessionExtraFlags,
  sessionListVM,
  conversationVM,
  terminalVm,
  onBack,
  connected,
  initialMessage,
  onInitialMessageSent,
}: SessionDetailScreenProps) {
  const [vmState, setVmState] = useState<ConversationVMState>(conversationVM.state)
  const [activeTab, setActiveTab] = useState<'events' | 'terminal'>(getStoredTab)
  const [input, setInput] = useState('')
  const [showMenu, setShowMenu] = useState(false)
  const [showSkills, setShowSkills] = useState(false)
  const [showUsageOverlay, setShowUsageOverlay] = useState(false)
  const [showRawOutput, setShowRawOutput] = useState(false)
  const [showEditSession, setShowEditSession] = useState(false)
  const [stagedImage, setStagedImage] = useState<{ base64: string; mimeType: string; previewUrl: string } | null>(null)
  const [pttRecording, setPttRecording] = useState(false)
  const [pttSupported, setPttSupported] = useState<boolean | null>(null)  // null = not yet checked
  const [planCardOpen, setPlanCardOpen] = useState(false)
  const [inputMode, setInputMode] = useState<'text' | 'voice'>(() => {
    try {
      return (localStorage.getItem('mclaude.inputMode') === 'voice') ? 'voice' : 'text'
    } catch {
      return 'text'
    }
  })
  const pttRecognitionRef = useRef<{ stop(): void } | null>(null)
  const pttRecordingRef = useRef(false)
  const scrollRef = useRef<HTMLDivElement>(null)
  const atBottomRef = useRef(true)
  const initialMessageSentRef = useRef(false)
  const menuRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const rawScrollRef = useRef<HTMLDivElement>(null)
  const rawAtBottomRef = useRef(true)
  const [rawTranscript, setRawTranscript] = useState('')

  // Send pre-seeded onboarding message once the session is ready
  useEffect(() => {
    if (!initialMessage || initialMessageSentRef.current) return
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
    })
    return unsub
  }, [conversationVM])

  // Restore scroll position when session changes
  useEffect(() => {
    const saved = scrollPositions.get(sessionId) ?? null
    if (saved !== null && scrollRef.current) {
      requestAnimationFrame(() => {
        if (scrollRef.current) {
          scrollRef.current.scrollTop = saved
          atBottomRef.current =
            scrollRef.current.scrollHeight - saved - scrollRef.current.clientHeight < 100
        }
      })
    }
  }, [sessionId])

  // Save scroll position on unmount
  useEffect(() => {
    return () => {
      if (scrollRef.current) {
        scrollPositions.set(sessionId, scrollRef.current.scrollTop)
      }
    }
  }, [sessionId])

  // Close menu on outside click
  useEffect(() => {
    if (!showMenu) return
    const handler = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setShowMenu(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [showMenu])

  // Sync inputMode when Settings changes it via localStorage
  useEffect(() => {
    const handler = (e: StorageEvent) => {
      if (e.key === 'mclaude.inputMode') {
        setInputMode(e.newValue === 'voice' ? 'voice' : 'text')
      }
    }
    window.addEventListener('storage', handler)
    return () => window.removeEventListener('storage', handler)
  }, [])

  const turns: Turn[] = vmState.turns
  const pendingMessages: PendingMessage[] = vmState.pendingMessages ?? []
  const skills: string[] = vmState.skills ?? []

  const handleScroll = () => {
    if (!scrollRef.current) return
    const el = scrollRef.current
    atBottomRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 100
  }

  // After every render: if still at bottom, snap to latest content.
  // useLayoutEffect (no deps) fires synchronously after every React DOM commit,
  // before paint — it never races with handleScroll.
  useLayoutEffect(() => {
    if (scrollRef.current && atBottomRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  })

  const handleSend = () => {
    const text = input.trim()
    if (!text) return
    setInput('')
    setShowSkills(false)
    if (stagedImage) {
      conversationVM.sendMessageWithImage(text, stagedImage.base64, stagedImage.mimeType)
      setStagedImage(null)
    } else {
      conversationVM.sendMessage(text)
    }
    // Scroll to bottom after send
    requestAnimationFrame(() => {
      if (scrollRef.current) {
        scrollRef.current.scrollTop = scrollRef.current.scrollHeight
        atBottomRef.current = true
      }
    })
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
    // Close skills popup on Escape
    if (e.key === 'Escape') {
      setShowSkills(false)
    }
    // Navigate skills popup with arrow keys
    if (showSkills && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) {
      e.preventDefault()
    }
  }

  const handleInputChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const val = e.target.value
    setInput(val)
    // Show skills autocomplete when input starts with /
    if (val.startsWith('/') && !val.includes(' ')) {
      setShowSkills(true)
    } else {
      setShowSkills(false)
    }
  }

  const handleSkillSelect = (skillName: string) => {
    setInput(`/${skillName} `)
    setShowSkills(false)
    textareaRef.current?.focus()
  }

  const handleAttach = () => {
    fileInputRef.current?.click()
  }

  const stageImageFile = (file: File) => {
    const reader = new FileReader()
    reader.onload = (ev) => {
      const result = ev.target?.result as string
      // result is "data:image/png;base64,..."
      const [header, base64] = result.split(',')
      const mimeType = header?.match(/data:([^;]+)/)?.[1] ?? 'image/png'
      setStagedImage({ base64: base64 ?? '', mimeType, previewUrl: result })
    }
    reader.readAsDataURL(file)
  }

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    stageImageFile(file)
    // Reset so same file can be picked again
    e.target.value = ''
  }

  const handlePaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const items = e.clipboardData?.items
    if (!items) return
    for (let i = 0; i < items.length; i++) {
      const item = items[i]
      if (item.kind === 'file' && item.type.startsWith('image/')) {
        const file = item.getAsFile()
        if (file) {
          e.preventDefault()
          stageImageFile(file)
          return
        }
      }
    }
    // No image found — let the event propagate so text paste works normally
  }

  const handleTabChange = (tab: 'events' | 'terminal') => {
    setActiveTab(tab)
    storeTab(tab)
  }

  // PTT: check Speech API availability on mount
  useEffect(() => {
    type SpeechRecAPI = {
      new(): {
        lang: string
        interimResults: boolean
        maxAlternatives: number
        onresult: ((event: Event) => void) | null
        onerror: (() => void) | null
        onend: (() => void) | null
        start(): void
        stop(): void
      }
    }
    const win = window as Window & typeof globalThis & {
      SpeechRecognition?: SpeechRecAPI
      webkitSpeechRecognition?: SpeechRecAPI
    }
    const SpeechRecognitionCtor = win.SpeechRecognition ?? win.webkitSpeechRecognition
    const supported = !!SpeechRecognitionCtor && location.protocol !== 'http:'
    setPttSupported(supported)
  }, [])

  const handlePttStart = () => {
    if (!pttSupported) {
      const isHttp = location.protocol === 'http:'
      alert(isHttp
        ? 'Voice input requires HTTPS. Connect via a secure URL.'
        : 'Voice input is not supported in this browser.'
      )
      return
    }
    if (pttRecordingRef.current) return

    type SpeechRecAPI2 = {
      new(): {
        lang: string
        interimResults: boolean
        maxAlternatives: number
        onresult: ((event: Event) => void) | null
        onerror: (() => void) | null
        onend: (() => void) | null
        start(): void
        stop(): void
      }
    }
    const win2 = window as Window & typeof globalThis & {
      SpeechRecognition?: SpeechRecAPI2
      webkitSpeechRecognition?: SpeechRecAPI2
    }
    const SpeechRecognitionCtor = win2.SpeechRecognition ?? win2.webkitSpeechRecognition
    if (!SpeechRecognitionCtor) return

    const recognition = new SpeechRecognitionCtor()
    recognition.lang = 'en-US'
    recognition.interimResults = false
    recognition.maxAlternatives = 1

    recognition.onresult = (event: Event) => {
      const e = event as unknown as { results: Array<Array<{ transcript: string }>> }
      const transcript = e.results[0]?.[0]?.transcript
      if (transcript) {
        conversationVM.sendMessage(transcript)
        // Scroll to bottom after PTT send
        requestAnimationFrame(() => {
          if (scrollRef.current) {
            scrollRef.current.scrollTop = scrollRef.current.scrollHeight
            atBottomRef.current = true
          }
        })
      }
    }

    recognition.onerror = () => {
      pttRecordingRef.current = false
      setPttRecording(false)
      pttRecognitionRef.current = null
    }

    recognition.onend = () => {
      pttRecordingRef.current = false
      setPttRecording(false)
      pttRecognitionRef.current = null
    }

    pttRecognitionRef.current = recognition
    recognition.start()
    pttRecordingRef.current = true
    setPttRecording(true)
  }

  const handlePttStop = () => {
    if (!pttRecordingRef.current) return
    if (pttRecognitionRef.current) {
      pttRecognitionRef.current.stop()
      pttRecognitionRef.current = null
    }
    pttRecordingRef.current = false
    setPttRecording(false)
  }

  const handleApprove = useCallback(() => {
    // Find the first pending control request
    const pending = turns
      .flatMap(t => t.blocks)
      .find(b => b.type === 'control_request' && (b as { status: string }).status === 'pending')
    if (pending && 'requestId' in pending) {
      conversationVM.approvePermission(pending.requestId as string)
    }
  }, [turns, conversationVM])

  const handleDeny = useCallback(() => {
    const pending = turns
      .flatMap(t => t.blocks)
      .find(b => b.type === 'control_request' && (b as { status: string }).status === 'pending')
    if (pending && 'requestId' in pending) {
      conversationVM.denyPermission(pending.requestId as string)
    }
  }, [turns, conversationVM])

  const isWorking = sessionState === 'running'
  const isUpdating = sessionState === 'updating'
  const needsPermission = sessionState === 'requires_action'
  const isPlanMode = sessionState === 'plan_mode'
  const showActionBar = needsPermission || isPlanMode

  // Skills autocomplete filtered list
  const skillQuery = input.startsWith('/') ? input.slice(1) : ''
  const filteredSkills = showSkills ? filterSkills(skills, skillQuery) : []

  // Three-dot menu for session detail
  const menuButton = (
    <div ref={menuRef} style={{ position: 'relative' }}>
      <button
        onClick={() => setShowMenu(v => !v)}
        style={{ fontSize: 16, color: 'var(--text2)', padding: '0 2px' }}
      >
        ⋯
      </button>
      {showMenu && (
        <div style={{
          position: 'absolute',
          top: 'calc(100% + 8px)',
          right: 0,
          background: 'var(--surf)',
          border: '1px solid var(--border)',
          borderRadius: 10,
          minWidth: 180,
          zIndex: 300,
          overflow: 'hidden',
          boxShadow: '0 4px 16px rgba(0,0,0,0.4)',
        }}>
          {/* Model switcher */}
          <div style={{ padding: '8px 0', borderBottom: '1px solid var(--border)' }}>
            <div style={{ padding: '4px 16px 8px', color: 'var(--text3)', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
              Model
            </div>
            {['claude-opus-4-6', 'claude-sonnet-4-6', 'claude-haiku-4-5'].map(model => (
              <button
                key={model}
                onClick={() => {
                  conversationVM.switchModel(model)
                  setShowMenu(false)
                }}
                style={{
                  width: '100%',
                  padding: '8px 16px',
                  textAlign: 'left',
                  color: vmState.model === model ? 'var(--blue)' : 'var(--text)',
                  fontSize: 13,
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                }}
              >
                {vmState.model === model ? '✓' : ' '} {model.replace('claude-', '')}
              </button>
            ))}
          </div>
          {/* Effort switcher */}
          <div style={{ padding: '8px 0', borderBottom: '1px solid var(--border)' }}>
            <div style={{ padding: '4px 16px 8px', color: 'var(--text3)', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
              Thinking Budget
            </div>
            {([
              { label: 'None', budget: 0 },
              { label: 'Low', budget: 2000 },
              { label: 'Medium', budget: 8000 },
              { label: 'High', budget: 20000 },
            ] as Array<{ label: string; budget: number }>).map(({ label, budget }) => (
              <button
                key={label}
                onClick={() => {
                  conversationVM.setMaxThinkingTokens(budget)
                  setShowMenu(false)
                }}
                style={{
                  width: '100%',
                  padding: '8px 16px',
                  textAlign: 'left',
                  color: 'var(--text)',
                  fontSize: 13,
                }}
              >
                {label} {budget > 0 ? `(${(budget / 1000).toFixed(0)}K)` : ''}
              </button>
            ))}
          </div>
          {/* Token Usage */}
          <button
            onClick={() => { setShowMenu(false); setShowUsageOverlay(true) }}
            style={{
              width: '100%',
              padding: '12px 16px',
              textAlign: 'left',
              color: 'var(--text)',
              fontSize: 14,
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              borderBottom: '1px solid var(--border)',
            }}
          >
            <span>📊</span> Token Usage
          </button>
          {/* Raw Output */}
          <button
            onClick={() => { setShowMenu(false); setShowRawOutput(true) }}
            style={{
              width: '100%',
              padding: '12px 16px',
              textAlign: 'left',
              color: 'var(--text)',
              fontSize: 14,
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              borderBottom: sessionListVM ? '1px solid var(--border)' : undefined,
            }}
          >
            <span>📜</span> Raw Output
          </button>
          {/* Edit Session */}
          {sessionListVM && (
            <button
              onClick={() => { setShowMenu(false); setShowEditSession(true) }}
              style={{
                width: '100%',
                padding: '12px 16px',
                textAlign: 'left',
                color: 'var(--text)',
                fontSize: 14,
                display: 'flex',
                alignItems: 'center',
                gap: 10,
              }}
            >
              <span>⚙</span> Edit Session
            </button>
          )}
        </div>
      )}
    </div>
  )

  // Helpers for usage overlay





  const usageTiles = sessionUsage ? [
    { label: 'Input', tokens: sessionUsage.inputTokens, color: 'var(--blue)', cost: sessionUsage.inputTokens / 1_000_000 * PRICE_PER_M.input },
    { label: 'Output', tokens: sessionUsage.outputTokens, color: 'var(--green)', cost: sessionUsage.outputTokens / 1_000_000 * PRICE_PER_M.output },
    { label: 'Cache W', tokens: sessionUsage.cacheWriteTokens, color: 'var(--orange)', cost: sessionUsage.cacheWriteTokens / 1_000_000 * PRICE_PER_M.cacheWrite },
    { label: 'Cache R', tokens: sessionUsage.cacheReadTokens, color: 'var(--purple)', cost: sessionUsage.cacheReadTokens / 1_000_000 * PRICE_PER_M.cacheRead },
  ] : []
  const totalUsageCost = usageTiles.reduce((s, t) => s + t.cost, 0)
  const totalUsageTokens = usageTiles.reduce((s, t) => s + t.tokens, 0)

  // Raw output: build rich transcript from all turns
  function buildRawTranscript(turns: typeof vmState.turns): string {
    const stripAnsi = (s: string) => s.replace(/\x1b\[[0-9;]*[mGKHF]/g, '')
    const lines: string[] = []
    for (const turn of turns) {
      if (turn.type === 'user') {
        for (const block of turn.blocks) {
          if (block.type === 'text') {
            lines.push('[User] ' + stripAnsi(block.text))
          } else if (block.type === 'streaming_text') {
            const text = (block as { chunks: string[] }).chunks.join('')
            if (text) lines.push('[User] ' + stripAnsi(text))
          }
          // skip image blocks in raw view
        }
      } else if (turn.type === 'assistant') {
        for (const block of turn.blocks) {
          if (block.type === 'text') {
            lines.push('[Claude] ' + stripAnsi(block.text))
          } else if (block.type === 'streaming_text') {
            const text = (block as { chunks: string[] }).chunks.join('')
            if (text) lines.push('[Claude] ' + stripAnsi(text))
          } else if (block.type === 'tool_use') {
            const inputStr = block.inputSummary || JSON.stringify(block.fullInput ?? '')
            lines.push('[' + block.name + '] ' + stripAnsi(inputStr))
            if (block.result) {
              const resultText = stripAnsi(block.result.content)
              const prefix = block.result.isError ? '[Error] ' : '[Result] '
              lines.push(prefix + resultText)
            }
          } else if (block.type === 'thinking') {
            lines.push('[Thinking] ' + stripAnsi(block.text))
          } else if (block.type === 'control_request') {
            lines.push('[Permission] ' + block.toolName + ': ' + JSON.stringify(block.input) + ' (' + block.status + ')')
          } else if (block.type === 'system_message') {
            lines.push('[System] ' + stripAnsi(block.text))
          } else if (block.type === 'compaction') {
            lines.push('[Compaction] ' + stripAnsi(block.summary))
          }
        }
      } else if (turn.type === 'system') {
        for (const block of turn.blocks) {
          if (block.type === 'system_message') {
            lines.push('[System] ' + stripAnsi(block.text))
          }
        }
      }
    }
    return lines.join('\n')
  }

  // Auto-refresh raw transcript every 500ms while overlay is open
  useEffect(() => {
    if (!showRawOutput) return
    const update = () => {
      const text = buildRawTranscript(vmState.turns)
      setRawTranscript(text)
      // Auto-scroll if at bottom
      if (rawAtBottomRef.current && rawScrollRef.current) {
        rawScrollRef.current.scrollTop = rawScrollRef.current.scrollHeight
      }
    }
    update() // immediate update on open
    const id = setInterval(update, 500)
    return () => clearInterval(id)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [showRawOutput, vmState.turns])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--bg)' }}>
      {/* Token Usage overlay */}
      {showUsageOverlay && (
        <div style={{ position: 'fixed', inset: 0, zIndex: 500, display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
          <div style={{ display: 'flex', alignItems: 'center', padding: '14px 16px', borderBottom: '1px solid var(--border)', flexShrink: 0 }}>
            <button onClick={() => setShowUsageOverlay(false)} style={{ color: 'var(--blue)', fontSize: 15, marginRight: 12 }}>‹ Back</button>
            <span style={{ fontWeight: 600, fontSize: 17 }}>Token Usage</span>
          </div>
          <div style={{ flex: 1, overflowY: 'auto', padding: 16 }}>
            <div style={{ color: 'var(--text2)', fontSize: 13, marginBottom: 16 }}>
              {sessionModel ?? vmState.model} · {vmState.turns.filter(t => t.type === 'assistant').length} turns
            </div>
            {sessionUsage ? (
              <>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 16 }}>
                  {usageTiles.map(tile => (
                    <div key={tile.label} style={{ background: 'var(--surf)', border: '1px solid var(--border)', borderRadius: 12, padding: 14 }}>
                      <div style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 4 }}>{tile.label}</div>
                      <div style={{ fontSize: 22, fontWeight: 700, color: tile.color }}>{formatTokens(tile.tokens)}</div>
                      <div style={{ color: 'var(--text2)', fontSize: 12, marginTop: 2 }}>{formatCost(tile.cost)}</div>
                    </div>
                  ))}
                </div>
                <div style={{ background: 'var(--surf)', border: '1px solid var(--border)', borderRadius: 12, padding: 16 }}>
                  <div style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 4 }}>Estimated Cost</div>
                  <div style={{ fontSize: 28, fontWeight: 700, color: 'var(--text)' }}>{formatCost(totalUsageCost)}</div>
                  <div style={{ color: 'var(--text2)', fontSize: 13, marginTop: 4 }}>{formatTokens(totalUsageTokens)} total tokens</div>
                  <div style={{ color: 'var(--text3)', fontSize: 11, marginTop: 8 }}>
                    Prices: input ${PRICE_PER_M.input}/M · output ${PRICE_PER_M.output}/M
                  </div>
                </div>
              </>
            ) : (
              <div style={{ color: 'var(--text2)', fontSize: 14 }}>No usage data available.</div>
            )}
          </div>
        </div>
      )}

      {/* Raw Output overlay */}
      {showRawOutput && (
        <div style={{ position: 'fixed', inset: 0, zIndex: 500, display: 'flex', flexDirection: 'column', background: '#000' }}>
          <div style={{ display: 'flex', alignItems: 'center', padding: '14px 16px', borderBottom: '1px solid rgba(255,255,255,0.1)', flexShrink: 0, background: '#111' }}>
            <button onClick={() => { setShowRawOutput(false) }} style={{ color: 'var(--blue)', fontSize: 15, marginRight: 12, background: 'none', border: 'none', cursor: 'pointer' }}>‹ Back</button>
            <span style={{ fontWeight: 600, fontSize: 17, color: 'var(--text)' }}>Raw Output</span>
          </div>
          <div
            ref={rawScrollRef}
            onScroll={() => {
              if (!rawScrollRef.current) return
              const { scrollTop, scrollHeight, clientHeight } = rawScrollRef.current
              rawAtBottomRef.current = scrollHeight - scrollTop - clientHeight < 40
            }}
            style={{ flex: 1, overflowY: 'auto', padding: 12, background: '#000' }}
          >
            <pre style={{
              fontFamily: "'Menlo','Courier New',monospace",
              fontSize: 12,
              color: 'var(--text)',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-word',
              lineHeight: 1.5,
              margin: 0,
            }}>
              {rawTranscript || '(no output yet)'}
            </pre>
          </div>
        </div>
      )}
      {/* Edit Session sheet */}
      {showEditSession && sessionListVM && (
        <EditSessionSheet
          sessionId={sessionId}
          currentExtraFlags={sessionExtraFlags ?? ''}
          sessionListVM={sessionListVM}
          onClose={() => setShowEditSession(false)}
        />
      )}

      <NavBar
        title={sessionName}
        onBack={onBack}
        connected={connected}
        right={menuButton}
        onRefresh={() => {
          // Scroll to top to show refresh
          if (scrollRef.current) scrollRef.current.scrollTop = 0
        }}
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
        {/* M4: Context meter — only shown when server provides context usage data */}
        {sessionUsage?.contextTokensUsed !== undefined && sessionUsage?.contextWindowSize !== undefined && sessionUsage.contextWindowSize > 0 && (
          <div
            title={`Context: ${sessionUsage.contextTokensUsed.toLocaleString()} / ${sessionUsage.contextWindowSize.toLocaleString()} tokens`}
            style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 6 }}
          >
            <div style={{ width: 64, height: 4, background: 'var(--surf3)', borderRadius: 2, overflow: 'hidden' }}>
              <div style={{
                width: `${Math.min(100, (sessionUsage.contextTokensUsed / sessionUsage.contextWindowSize) * 100).toFixed(1)}%`,
                height: '100%',
                background: sessionUsage.contextTokensUsed / sessionUsage.contextWindowSize > 0.8 ? 'var(--red)' : 'var(--blue)',
                borderRadius: 2,
              }} />
            </div>
            <span style={{ color: 'var(--text3)', fontSize: 11 }}>
              {Math.round((sessionUsage.contextTokensUsed / sessionUsage.contextWindowSize) * 100)}%
            </span>
          </div>
        )}
      </div>

      {/* Plan card (plan_mode only) */}
      {isPlanMode && (
        <div style={{
          margin: '8px 16px 0',
          background: 'var(--surf)',
          border: '1px solid rgba(191,90,242,0.4)',
          borderLeft: '3px solid var(--purple)',
          borderRadius: 12,
          overflow: 'hidden',
          flexShrink: 0,
        }}>
          <div
            onClick={() => setPlanCardOpen(v => !v)}
            style={{
              padding: '10px 14px',
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              color: 'var(--purple)',
              fontWeight: 500,
              fontSize: 13,
              cursor: 'pointer',
              userSelect: 'none',
            }}
          >
            <span>📋</span>
            <span>View Plan</span>
            <span style={{ marginLeft: 'auto', fontSize: 11 }}>{planCardOpen ? '▼' : '▶'}</span>
          </div>
          {planCardOpen && (
            <div style={{
              padding: '8px 14px 12px',
              borderTop: '1px solid rgba(191,90,242,0.2)',
              background: 'var(--surf2)',
              color: 'var(--text2)',
              fontSize: 13,
              fontFamily: "'Menlo','Courier New',monospace",
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-word',
              lineHeight: 1.5,
            }}>
              Plan content not available
            </div>
          )}
        </div>
      )}

      {/* Action bar (needs_permission or plan_mode) */}
      {showActionBar && (
        <div style={{
          display: 'flex',
          gap: 8,
          padding: '8px 16px',
          borderBottom: '1px solid var(--border)',
          background: 'var(--surf)',
          flexShrink: 0,
          marginTop: isPlanMode ? 8 : 0,
        }}>
          <button
            onClick={handleApprove}
            style={{
              flex: 1,
              padding: '8px 0',
              background: 'var(--green)',
              color: '#000',
              borderRadius: 8,
              fontWeight: 600,
              fontSize: 14,
            }}
          >
            ✓ Approve
          </button>
          <button
            onClick={handleDeny}
            style={{
              flex: 1,
              padding: '8px 0',
              background: 'var(--red)',
              color: '#fff',
              borderRadius: 8,
              fontWeight: 600,
              fontSize: 14,
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
            onClick={() => handleTabChange(tab)}
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

      {/* Updating banner */}
      {isUpdating && (
        <div style={{
          margin: '8px 16px 0',
          background: 'rgba(10,132,255,0.1)',
          border: '1px solid rgba(10,132,255,0.3)',
          borderRadius: 10,
          padding: '10px 14px',
          display: 'flex',
          alignItems: 'flex-start',
          gap: 8,
          flexShrink: 0,
        }}>
          <span style={{ color: 'var(--blue)', fontSize: 14, marginTop: 1 }}>↻</span>
          <div>
            <div style={{ color: 'var(--blue)', fontWeight: 500, fontSize: 13 }}>Updating</div>
            <div style={{ color: 'var(--text2)', fontSize: 12, marginTop: 2 }}>
              Your session will resume shortly. Messages are queued.
            </div>
          </div>
        </div>
      )}

      {/* Content */}
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        style={{
          flex: 1,
          overflowY: activeTab === 'terminal' ? 'hidden' : 'auto',
          padding: activeTab === 'terminal' ? 0 : '12px 16px',
          display: 'flex',
          flexDirection: 'column',
          minHeight: 0,
        }}
      >
        {activeTab === 'events' ? (
          <EventList
            turns={turns}
            pendingMessages={pendingMessages}
            onApprove={id => conversationVM.approvePermission(id)}
            onDeny={id => conversationVM.denyPermission(id)}
          />
        ) : (
          terminalVm ? (
            <TerminalTab terminalVm={terminalVm} />
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
          )
        )}
      </div>

      {/* Input bar (Events tab only) */}
      {activeTab === 'events' && (
        <div style={{
          background: 'var(--surf)',
          borderTop: '1px solid var(--border)',
          flexShrink: 0,
          position: 'relative',
        }}>
          {/* Screenshot preview strip */}
          {stagedImage && (
            <div style={{
              padding: '8px 12px',
              borderBottom: '1px solid var(--border)',
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              background: 'var(--surf)',
            }}>
              <img
                src={stagedImage.previewUrl}
                alt="staged screenshot"
                style={{ width: 48, height: 36, objectFit: 'cover', borderRadius: 6, flexShrink: 0 }}
              />
              <span style={{ color: 'var(--text2)', fontSize: 13, flex: 1 }}>Screenshot ready</span>
              <button
                onClick={() => setStagedImage(null)}
                style={{ color: 'var(--text3)', fontSize: 16, padding: 4 }}
              >
                ✕
              </button>
            </div>
          )}

          {/* Skills autocomplete popup */}
          {showSkills && filteredSkills.length > 0 && (
            <div style={{
              position: 'absolute',
              bottom: '100%',
              left: 0,
              right: 0,
              background: 'var(--surf)',
              border: '1px solid var(--border)',
              borderRadius: 10,
              margin: '0 8px 4px',
              maxHeight: 200,
              overflowY: 'auto',
              boxShadow: '0 -4px 16px rgba(0,0,0,0.4)',
              zIndex: 200,
            }}>
              {filteredSkills.map(skill => (
                <button
                  key={skill}
                  onClick={() => handleSkillSelect(skill)}
                  style={{
                    width: '100%',
                    padding: '10px 14px',
                    textAlign: 'left',
                    display: 'flex',
                    alignItems: 'center',
                    gap: 10,
                    borderBottom: '1px solid var(--border)',
                    color: 'var(--text)',
                    fontSize: 13,
                  }}
                >
                  <span style={{ color: 'var(--blue)', fontFamily: 'monospace', flex: 1 }}>/{skill}</span>
                  <span style={{
                    fontSize: 11,
                    fontWeight: 500,
                    padding: '2px 7px',
                    borderRadius: 10,
                    background: 'rgba(10,132,255,0.15)',
                    color: 'var(--blue)',
                    flexShrink: 0,
                  }}>built-in</span>
                </button>
              ))}
            </div>
          )}

          {/* Input row */}
          <div style={{
            padding: '8px 12px',
            display: 'flex',
            alignItems: 'flex-end',
            gap: 8,
            position: 'relative',
          }}>
            {/* Stop button (only when working) */}
            {isWorking && (
              <button
                onClick={() => conversationVM.interrupt()}
                style={{
                  width: 32,
                  height: 32,
                  borderRadius: '50%',
                  background: 'rgba(255,69,58,0.15)',
                  color: 'var(--red)',
                  flexShrink: 0,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  fontSize: 12,
                  border: '1px solid rgba(255,69,58,0.3)',
                }}
              >
                ✕
              </button>
            )}

            {/* Attach button */}
            <button
              onClick={handleAttach}
              style={{
                width: 32,
                height: 32,
                borderRadius: '50%',
                background: 'var(--surf2)',
                color: 'var(--text2)',
                flexShrink: 0,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontSize: 15,
              }}
              title="Attach image"
            >
              📷
            </button>
            <input
              ref={fileInputRef}
              type="file"
              accept="image/*"
              style={{ display: 'none' }}
              onChange={handleFileChange}
            />

            {/* Textarea wrapper — relative so keyboard icon can be positioned inside */}
            <div style={{ flex: 1, position: 'relative' }}>
              {/* Keyboard icon (voice mode only) — focuses textarea */}
              {inputMode === 'voice' && (
                <button
                  onClick={() => textareaRef.current?.focus()}
                  style={{
                    position: 'absolute',
                    top: 4,
                    right: 8,
                    width: 22,
                    height: 22,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    fontSize: 13,
                    color: 'var(--text2)',
                    opacity: 0.6,
                    zIndex: 1,
                    background: 'none',
                    border: 'none',
                    cursor: 'pointer',
                    padding: 0,
                  }}
                  title="Switch to keyboard"
                >
                  ⌨
                </button>
              )}
              <textarea
                ref={textareaRef}
                value={input}
                onChange={handleInputChange}
                onKeyDown={handleKeyDown}
                onPaste={handlePaste}
                placeholder="Message… or / for skills"
                rows={1}
                style={{
                  width: '100%',
                  background: 'var(--surf2)',
                  border: '1px solid var(--border)',
                  borderRadius: 20,
                  padding: inputMode === 'voice' ? '7px 32px 7px 14px' : '7px 14px',
                  color: 'var(--text)',
                  WebkitTextFillColor: 'var(--text)',
                  WebkitAppearance: 'none',
                  fontSize: 15,
                  resize: 'none',
                  minHeight: 36,
                  maxHeight: inputMode === 'voice' ? 72 : 120,
                  overflowY: 'auto',
                  lineHeight: 1.4,
                  boxSizing: 'border-box',
                }}
              />
            </div>

            {/* Text mode: small PTT button (between textarea and Send) */}
            {inputMode === 'text' && (
              <button
                onPointerDown={handlePttStart}
                onPointerUp={handlePttStop}
                onPointerLeave={handlePttStop}
                style={{
                  width: 32,
                  height: 32,
                  borderRadius: '50%',
                  background: pttRecording ? 'var(--red)' : 'var(--surf2)',
                  color: pttRecording ? '#fff' : (pttSupported === false ? 'var(--text3)' : 'var(--text2)'),
                  flexShrink: 0,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  fontSize: 15,
                  opacity: pttSupported === false ? 0.4 : 1,
                  animation: pttRecording ? 'pulse-opacity 1.2s ease-in-out infinite' : undefined,
                  transition: 'background 0.15s, color 0.15s',
                }}
                title={pttRecording ? 'Recording… release to send' : 'Hold to record (push-to-talk)'}
              >
                🎙
              </button>
            )}

            {/* Text mode: Send button */}
            {inputMode === 'text' && (
              <button
                onClick={() => handleSend()}
                disabled={!input.trim() || !connected}
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
            )}

            {/* Voice mode: large PTT button (replaces Send button) */}
            {inputMode === 'voice' && (
              <button
                onPointerDown={handlePttStart}
                onPointerUp={handlePttStop}
                onPointerLeave={handlePttStop}
                style={{
                  width: 56,
                  height: 56,
                  borderRadius: '50%',
                  background: pttRecording ? 'var(--red)' : 'var(--blue)',
                  color: '#fff',
                  flexShrink: 0,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  fontSize: 24,
                  opacity: pttSupported === false ? 0.4 : 1,
                  animation: pttRecording ? 'pulse-opacity 1.2s ease-in-out infinite' : undefined,
                  transition: 'background 0.15s',
                }}
                title={pttRecording ? 'Recording… release to send' : 'Hold to record (push-to-talk)'}
              >
                🎙
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
