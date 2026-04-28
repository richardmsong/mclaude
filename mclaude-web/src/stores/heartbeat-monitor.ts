import type { INATSClient } from '@/types'
import { kvKeyHostsForUser } from '@/lib/subj'
import type { UserSlug } from '@/lib/slug'

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
    /** User slug for KV key construction (ADR-0024). Falls back to userId when absent. */
    private readonly userSlug: string = userId,
  ) {}

  start(): void {
    this.stop()

    this._unwatcher = this.natsClient.kvWatch('mclaude-hosts', kvKeyHostsForUser(this.userSlug as UserSlug), (entry) => {
      const hostSlug = entry.key.split('.')[1]
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
