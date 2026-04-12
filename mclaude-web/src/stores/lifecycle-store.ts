import type { INATSClient, LifecycleEvent } from '@/types'

export type LifecycleListener = (event: LifecycleEvent) => void

export class LifecycleStore {
  private _listeners: LifecycleListener[] = []
  private _unsubscribe: (() => void) | null = null

  constructor(
    private readonly natsClient: INATSClient,
    private readonly userId: string,
    private readonly projectId: string,
  ) {}

  start(): void {
    this.stop()
    const subject = `mclaude.${this.userId}.${this.projectId}.lifecycle.>`
    this._unsubscribe = this.natsClient.subscribe(subject, (msg) => {
      try {
        const event = JSON.parse(new TextDecoder().decode(msg.data)) as LifecycleEvent
        for (const l of this._listeners) l(event)
      } catch {}
    })
  }

  stop(): void {
    if (this._unsubscribe) { this._unsubscribe(); this._unsubscribe = null }
  }

  onLifecycleEvent(listener: LifecycleListener): () => void {
    this._listeners.push(listener)
    return () => { this._listeners = this._listeners.filter(l => l !== listener) }
  }
}
