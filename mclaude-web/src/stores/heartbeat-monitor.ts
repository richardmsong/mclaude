import type { INATSClient } from '@/types'

export interface HeartbeatHealth {
  ts: string
  healthy: boolean
}

export type HealthListener = (projectId: string, healthy: boolean) => void

export class HeartbeatMonitor {
  private _health = new Map<string, HeartbeatHealth>()
  private _listeners: HealthListener[] = []
  private _unwatcher: (() => void) | null = null
  private _checkTimer: ReturnType<typeof setInterval> | null = null
  private _thresholdMs: number

  constructor(
    private readonly natsClient: INATSClient,
    private readonly userId: string,
    thresholdMs = 60_000,
  ) {
    this._thresholdMs = thresholdMs
  }

  start(checkIntervalMs = 5_000): void {
    this.stop()

    this._unwatcher = this.natsClient.kvWatch('mclaude-heartbeats', `${this.userId}.*`, (entry) => {
      const projectId = entry.key.split('.')[1]
      if (!projectId) return
      try {
        const { ts } = JSON.parse(new TextDecoder().decode(entry.value)) as { ts: string }
        const wasHealthy = this._health.get(projectId)?.healthy ?? false
        const healthy = Date.now() - new Date(ts).getTime() < this._thresholdMs
        this._health.set(projectId, { ts, healthy })
        if (healthy !== wasHealthy) {
          for (const l of this._listeners) l(projectId, healthy)
        }
      } catch {}
    })

    this._checkTimer = setInterval(() => {
      for (const [projectId, h] of this._health) {
        const healthy = Date.now() - new Date(h.ts).getTime() < this._thresholdMs
        if (healthy !== h.healthy) {
          h.healthy = healthy
          for (const l of this._listeners) l(projectId, healthy)
        }
      }
    }, checkIntervalMs)
  }

  stop(): void {
    if (this._unwatcher) { this._unwatcher(); this._unwatcher = null }
    if (this._checkTimer !== null) { clearInterval(this._checkTimer); this._checkTimer = null }
  }

  isHealthy(projectId: string): boolean {
    const h = this._health.get(projectId)
    if (!h) return false
    return Date.now() - new Date(h.ts).getTime() < this._thresholdMs
  }

  onHealthChanged(listener: HealthListener): () => void {
    this._listeners.push(listener)
    return () => { this._listeners = this._listeners.filter(l => l !== listener) }
  }
}
