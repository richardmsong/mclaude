import type { ConversationVM } from './conversation-vm'

export interface PendingPermission {
  requestId: string
  toolName: string
  inputSummary: string
  fullInput?: unknown
}

export type PermissionPromptListener = (pending: PendingPermission | null) => void

export class PermissionPromptVM {
  private _listeners: PermissionPromptListener[] = []
  private _unsubscribe: (() => void) | null = null

  constructor(private readonly conversationVM: ConversationVM) {
    this._unsubscribe = this.conversationVM.onStateChanged(() => this._notify())
  }

  get pending(): PendingPermission | null {
    const { turns } = this.conversationVM.state
    for (let i = turns.length - 1; i >= 0; i--) {
      const turn = turns[i]
      for (const block of turn.blocks) {
        if (block.type === 'control_request' && block.status === 'pending') {
          return {
            requestId: block.requestId,
            toolName: block.toolName,
            inputSummary: JSON.stringify(block.input).slice(0, 200),
            fullInput: block.input,
          }
        }
      }
    }
    return null
  }

  get allPending(): PendingPermission[] {
    const result: PendingPermission[] = []
    for (const turn of this.conversationVM.state.turns) {
      for (const block of turn.blocks) {
        if (block.type === 'control_request' && block.status === 'pending') {
          result.push({
            requestId: block.requestId,
            toolName: block.toolName,
            inputSummary: JSON.stringify(block.input).slice(0, 200),
            fullInput: block.input,
          })
        }
      }
    }
    return result
  }

  approve(): void {
    const p = this.pending
    if (p) this.conversationVM.approvePermission(p.requestId)
  }

  deny(): void {
    const p = this.pending
    if (p) this.conversationVM.denyPermission(p.requestId)
  }

  onPendingChanged(listener: PermissionPromptListener): () => void {
    this._listeners.push(listener)
    return () => { this._listeners = this._listeners.filter(l => l !== listener) }
  }

  destroy(): void {
    if (this._unsubscribe) { this._unsubscribe(); this._unsubscribe = null }
  }

  private _notify(): void {
    for (const l of this._listeners) l(this.pending)
  }
}
