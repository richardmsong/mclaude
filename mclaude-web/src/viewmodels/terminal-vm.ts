import type { INATSClient } from '@/types'

export interface TerminalInstance {
  id: string
  cwd: string
  active: boolean
}

export type TerminalOutputListener = (data: Uint8Array) => void

export class TerminalVM {
  private _terminals: TerminalInstance[] = []
  private _outputListeners = new Map<string, TerminalOutputListener[]>()
  private _unsubscribers = new Map<string, () => void>()

  constructor(
    private readonly natsClient: INATSClient,
    private readonly userId: string,
    private readonly projectId: string,
  ) {}

  get terminals(): TerminalInstance[] {
    return this._terminals
  }

  async createTerminal(cwd?: string): Promise<string> {
    const subject = `mclaude.${this.userId}.${this.projectId}.api.terminal.create`
    const reply = await this.natsClient.request(
      subject,
      new TextEncoder().encode(JSON.stringify({ cwd })),
    )
    const { terminalId } = JSON.parse(new TextDecoder().decode(reply.data)) as { terminalId: string }
    this._terminals.push({ id: terminalId, cwd: cwd ?? '', active: true })

    // Subscribe to output
    const outputSubject = `mclaude.${this.userId}.${this.projectId}.terminal.${terminalId}.output`
    const unsub = this.natsClient.subscribe(outputSubject, (msg) => {
      const listeners = this._outputListeners.get(terminalId) ?? []
      for (const l of listeners) l(msg.data)
    })
    this._unsubscribers.set(terminalId, unsub)
    return terminalId
  }

  async deleteTerminal(terminalId: string): Promise<void> {
    const subject = `mclaude.${this.userId}.${this.projectId}.api.terminal.delete`
    await this.natsClient.request(subject, new TextEncoder().encode(JSON.stringify({ terminalId })))
    const unsub = this._unsubscribers.get(terminalId)
    if (unsub) { unsub(); this._unsubscribers.delete(terminalId) }
    this._terminals = this._terminals.filter(t => t.id !== terminalId)
    this._outputListeners.delete(terminalId)
  }

  sendInput(terminalId: string, data: Uint8Array): void {
    const subject = `mclaude.${this.userId}.${this.projectId}.terminal.${terminalId}.input`
    this.natsClient.publish(subject, data)
  }

  resize(terminalId: string, rows: number, cols: number): void {
    const subject = `mclaude.${this.userId}.${this.projectId}.api.terminal.resize`
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify({ terminalId, rows, cols })))
  }

  onOutput(terminalId: string, callback: TerminalOutputListener): () => void {
    if (!this._outputListeners.has(terminalId)) {
      this._outputListeners.set(terminalId, [])
    }
    this._outputListeners.get(terminalId)!.push(callback)
    return () => {
      const listeners = this._outputListeners.get(terminalId) ?? []
      this._outputListeners.set(terminalId, listeners.filter(l => l !== callback))
    }
  }
}
