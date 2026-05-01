import type { INATSClient } from '@/types'

export interface HeartbeatHealth {
  online: boolean
}

export type HealthListener = (hostSlug: string, online: boolean) => void

/**
 * HeartbeatMonitor tracks host liveness via the mclaude-hosts KV bucket (ADR-0035, ADR-0046).
 * Reads `{online: boolean}` from KV entries written by the control-plane on $SYS events.
 * Online/offline state is driven by KV watch events — no time-based polling.
 */
export class HeartbeatMonitor {
  private _health = new Map<string, HeartbeatHealth>()
  private _listeners: HealthListener[] = []
  private _unwatcher: (() => void) | null = null

  constructor(
    private readonly natsClient: INATSClient,
    userId: string,
    /** User slug (ADR-0054: now encoded in bucket name, not KV key — kept for API compatibility). */
    _userSlug: string = userId,
  ) {}

  start(): void {
    this.stop()

    // Watch all accessible hosts in the shared mclaude-hosts bucket (ADR-0054).
    // Key format: {hslug} (flat, globally unique — no user prefix).
    // JWT scopes delivery to the user's permitted hosts server-side.
    this._unwatcher = this.natsClient.kvWatch('mclaude-hosts', '>', (entry) => {
      // Key is just {hslug} — no need to split
      const hostSlug = entry.key
      if (!hostSlug) return
      try {
        const { online } = JSON.parse(new TextDecoder().decode(entry.value)) as { online: boolean }
        const wasOnline = this._health.get(hostSlug)?.online ?? false
        this._health.set(hostSlug, { online })
        if (online !== wasOnline) {
          for (const l of this._listeners) l(hostSlug, online)
        }
      } catch {}
    })
  }

  stop(): void {
    if (this._unwatcher) { this._unwatcher(); this._unwatcher = null }
  }

  isHealthy(hostSlug: string): boolean {
    return this._health.get(hostSlug)?.online ?? false
  }

  onHealthChanged(listener: HealthListener): () => void {
    this._listeners.push(listener)
    return () => { this._listeners = this._listeners.filter(l => l !== listener) }
  }
}
