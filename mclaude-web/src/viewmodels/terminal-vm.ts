import type { INATSClient } from '@/types'
import { subjTerminal } from '@/lib/subj'
import type { UserSlug, HostSlug, ProjectSlug } from '@/lib/slug'

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
    userId: string,
    projectId: string,
    /** User slug for subject construction (ADR-0024). Falls back to userId when absent. */
    private readonly userSlug: string = userId,
    /** Host slug for subject construction (ADR-0035). Falls back to 'local' when absent. */
    private readonly hostSlug: string = 'local',
    /** Project slug for subject construction (ADR-0024). Falls back to projectId when absent. */
    private readonly projectSlug: string = projectId,
  ) {}

  get terminals(): TerminalInstance[] {
    return this._terminals
  }

  async createTerminal(cwd?: string): Promise<string> {
    const subject = subjTerminal(this.userSlug as UserSlug, this.hostSlug as HostSlug, this.projectSlug as ProjectSlug, 'create')
    const reply = await this.natsClient.request(
      subject,
      new TextEncoder().encode(JSON.stringify({ cwd })),
    )
    const { terminalId } = JSON.parse(new TextDecoder().decode(reply.data)) as { terminalId: string }
    this._terminals.push({ id: terminalId, cwd: cwd ?? '', active: true })

    // Subscribe to output
    const outputSubject = subjTerminal(this.userSlug as UserSlug, this.hostSlug as HostSlug, this.projectSlug as ProjectSlug, `${terminalId}.output`)
    const unsub = this.natsClient.subscribe(outputSubject, (msg) => {
      const listeners = this._outputListeners.get(terminalId) ?? []
      for (const l of listeners) l(msg.data)
    })
    this._unsubscribers.set(terminalId, unsub)
    return terminalId
  }

  async deleteTerminal(terminalId: string): Promise<void> {
    const subject = subjTerminal(this.userSlug as UserSlug, this.hostSlug as HostSlug, this.projectSlug as ProjectSlug, 'delete')
    await this.natsClient.request(subject, new TextEncoder().encode(JSON.stringify({ terminalId })))
    const unsub = this._unsubscribers.get(terminalId)
    if (unsub) { unsub(); this._unsubscribers.delete(terminalId) }
    this._terminals = this._terminals.filter(t => t.id !== terminalId)
    this._outputListeners.delete(terminalId)
  }

  sendInput(terminalId: string, data: Uint8Array): void {
    const subject = subjTerminal(this.userSlug as UserSlug, this.hostSlug as HostSlug, this.projectSlug as ProjectSlug, `${terminalId}.input`)
    this.natsClient.publish(subject, data)
  }

  resize(terminalId: string, rows: number, cols: number): void {
    const subject = subjTerminal(this.userSlug as UserSlug, this.hostSlug as HostSlug, this.projectSlug as ProjectSlug, 'resize')
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
