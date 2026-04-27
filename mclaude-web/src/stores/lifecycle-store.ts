import type { INATSClient, LifecycleEvent } from '@/types'
import { subjLifecycleWildcard } from '@/lib/subj'
import type { UserSlug, HostSlug, ProjectSlug } from '@/lib/slug'

export type LifecycleListener = (event: LifecycleEvent) => void

export class LifecycleStore {
  private _listeners: LifecycleListener[] = []
  private _unsubscribe: (() => void) | null = null

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

  start(): void {
    this.stop()
    const subject = subjLifecycleWildcard(this.userSlug as UserSlug, this.hostSlug as HostSlug, this.projectSlug as ProjectSlug)
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
