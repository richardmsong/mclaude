import { useEffect, useRef, useState, useCallback } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import type { TerminalVM } from '@/viewmodels/terminal-vm'
import '@xterm/xterm/css/xterm.css'

interface TerminalTabProps {
  terminalVm: TerminalVM
}

export function TerminalTab({ terminalVm }: TerminalTabProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const xtermRef = useRef<Terminal | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const terminalIdRef = useRef<string | null>(null)
  const [textMode, setTextMode] = useState(false)
  const [ctrlActive, setCtrlActive] = useState(false)
  const [textContent, setTextContent] = useState('')
  const ctrlActiveRef = useRef(false)

  // Keep ref in sync for use in xterm onData callback
  useEffect(() => {
    ctrlActiveRef.current = ctrlActive
  }, [ctrlActive])

  const sendBytes = useCallback((data: string) => {
    if (!terminalIdRef.current) return
    terminalVm.sendInput(terminalIdRef.current, new TextEncoder().encode(data))
  }, [terminalVm])

  useEffect(() => {
    if (!containerRef.current) return

    const xterm = new Terminal({
      theme: {
        background: '#000000',
        foreground: '#ffffff',
      },
      fontFamily: "'Menlo', 'Courier New', monospace",
      fontSize: 13,
      cursorBlink: true,
    })
    const fitAddon = new FitAddon()
    xterm.loadAddon(fitAddon)
    xterm.open(containerRef.current)
    fitAddon.fit()
    xtermRef.current = xterm
    fitAddonRef.current = fitAddon

    let terminalId: string | null = null
    let unsubOutput: (() => void) | null = null
    let destroyed = false

    // Create terminal and wire up
    terminalVm.createTerminal().then((id) => {
      if (destroyed) {
        terminalVm.deleteTerminal(id)
        return
      }
      terminalId = id
      terminalIdRef.current = id

      // Subscribe to output
      unsubOutput = terminalVm.onOutput(id, (data: Uint8Array) => {
        xterm.write(data)
        // Keep text content in sync when text mode is active
        if (textMode) {
          setTextContent(xterm.buffer.active.getLine(0)?.translateToString() ?? '')
        }
      })
    }).catch(() => {
      // Terminal creation failed — server unavailable
      xterm.write('\r\nFailed to connect to terminal.\r\n')
    })

    // On xterm input: send to server (with optional Ctrl modifier)
    const dataDispose = xterm.onData((data) => {
      if (!terminalId) return
      if (ctrlActiveRef.current) {
        // Ctrl modifier: convert A-Z to \x01-\x1a
        const ch = data.toUpperCase().charCodeAt(0)
        if (ch >= 65 && ch <= 90) {
          terminalVm.sendInput(terminalId, new TextEncoder().encode(String.fromCharCode(ch - 64)))
          setCtrlActive(false)
          return
        }
        setCtrlActive(false)
      }
      terminalVm.sendInput(terminalId, new TextEncoder().encode(data))
    })

    // Resize observer
    const ro = new ResizeObserver(() => {
      fitAddon.fit()
      if (terminalId) {
        terminalVm.resize(terminalId, xterm.rows, xterm.cols)
      }
    })
    if (containerRef.current) ro.observe(containerRef.current)

    return () => {
      destroyed = true
      dataDispose.dispose()
      ro.disconnect()
      unsubOutput?.()
      if (terminalId) {
        terminalVm.deleteTerminal(terminalId)
      }
      xterm.dispose()
      xtermRef.current = null
      fitAddonRef.current = null
      terminalIdRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [terminalVm])

  // When switching to text mode, snapshot terminal buffer
  const handleTextMode = () => {
    if (!xtermRef.current) return
    const xterm = xtermRef.current
    const lines: string[] = []
    for (let i = 0; i < xterm.buffer.active.length; i++) {
      lines.push(xterm.buffer.active.getLine(i)?.translateToString(true) ?? '')
    }
    setTextContent(lines.join('\n').trimEnd())
    setTextMode(true)
  }

  const handleLiveMode = () => {
    setTextMode(false)
    // Re-fit after switching back
    setTimeout(() => fitAddonRef.current?.fit(), 50)
  }

  const handlePaste = async () => {
    try {
      const text = await navigator.clipboard.readText()
      sendBytes(text)
    } catch {
      // Clipboard read failed (permission denied or not supported)
    }
  }

  // Keyboard toolbar button style
  const btnStyle: React.CSSProperties = {
    padding: '6px 10px',
    background: '#222',
    color: '#eee',
    border: '1px solid #444',
    borderRadius: 6,
    fontSize: 12,
    fontFamily: "'Menlo', 'Courier New', monospace",
    cursor: 'pointer',
    flexShrink: 0,
    minWidth: 36,
    textAlign: 'center',
  }

  const ctrlBtnStyle: React.CSSProperties = {
    ...btnStyle,
    background: ctrlActive ? '#0a84ff' : '#222',
    color: ctrlActive ? '#fff' : '#eee',
    border: `1px solid ${ctrlActive ? '#0a84ff' : '#444'}`,
  }

  return (
    <div style={{
      display: 'flex',
      flexDirection: 'column',
      height: '100%',
      background: '#000',
      borderRadius: 8,
      overflow: 'hidden',
    }}>
      {/* Terminal area */}
      <div style={{ flex: 1, minHeight: 0, position: 'relative' }}>
        {/* xterm canvas — always mounted so the terminal stays alive */}
        <div
          ref={containerRef}
          style={{
            position: 'absolute',
            inset: 0,
            display: textMode ? 'none' : 'block',
          }}
        />
        {/* Text mode overlay */}
        {textMode && (
          <pre style={{
            position: 'absolute',
            inset: 0,
            margin: 0,
            padding: 8,
            background: '#000',
            color: '#fff',
            fontFamily: "'Menlo', 'Courier New', monospace",
            fontSize: 13,
            overflowY: 'auto',
            overflowX: 'auto',
            whiteSpace: 'pre',
            userSelect: 'text',
          }}>
            {textContent}
          </pre>
        )}
      </div>

      {/* Keyboard toolbar */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: 6,
        padding: '6px 8px',
        background: '#111',
        borderTop: '1px solid #333',
        overflowX: 'auto',
        flexShrink: 0,
      }}>
        <button style={btnStyle} onClick={() => sendBytes('\x1b')}>Esc</button>
        <button style={ctrlBtnStyle} onClick={() => setCtrlActive(a => !a)}>Ctrl</button>
        <button style={btnStyle} onClick={() => sendBytes('\t')}>Tab</button>
        <button style={btnStyle} onClick={() => sendBytes('\x1b[A')}>▲</button>
        <button style={btnStyle} onClick={() => sendBytes('\x1b[B')}>▼</button>
        <button style={btnStyle} onClick={() => sendBytes('\x1b[D')}>◀</button>
        <button style={btnStyle} onClick={() => sendBytes('\x1b[C')}>▶</button>
        <button style={btnStyle} onClick={handlePaste}>⌅ Paste</button>
        {textMode ? (
          <button style={btnStyle} onClick={handleLiveMode}>⌨ Live</button>
        ) : (
          <button style={btnStyle} onClick={handleTextMode}>⎘ Text</button>
        )}
      </div>
    </div>
  )
}
