import type { INATSClient } from '@/types'
import type { SessionStore } from '@/stores/session-store'
import type { HeartbeatMonitor } from '@/stores/heartbeat-monitor'

const FILTER_PROJECT_KEY = 'mclaude.filterProjectId'

/**
 * Minimal Storage interface — same shape as window.localStorage.
 * Defaults to globalThis.localStorage in the browser; tests inject an in-memory fake.
 */
export interface IStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

export interface SessionVM {
  id: string
  name: string
  state: string
  model: string
  branch: string
  cwd: string
  costUsd: number
  hasPendingPermission: boolean
  /** ISO timestamp of last state change — used for sorting. */
  stateSince: string
  /** Raw CLI flags string passed at session create/restart. Empty string if none. */
  extraFlags: string
}

export interface ProjectVM {
  id: string
  name: string
  status: string
  healthy: boolean
  sessions: SessionVM[]
}

export interface ProjectGroup {
  project: ProjectVM
  sessions: SessionVM[]
}

export type SessionListListener = (projects: ProjectVM[]) => void

export class SessionListVM {
  private _listeners: SessionListListener[] = []
  private _unsubscribers: Array<() => void> = []
  private readonly _storage: IStorage

  constructor(
    private readonly sessionStore: SessionStore,
    private readonly heartbeatMonitor: HeartbeatMonitor,
    private readonly natsClient: INATSClient,
    private readonly userId: string,
    storage?: IStorage,
  ) {
    this._storage = storage ?? (globalThis.localStorage as IStorage)
    this._unsubscribers.push(
      this.sessionStore.onSessionChanged(() => this._notify()),
      this.sessionStore.onProjectChanged(() => this._notify()),
      this.heartbeatMonitor.onHealthChanged(() => this._notify()),
    )
  }

  get projects(): ProjectVM[] {
    return Array.from(this.sessionStore.projects.values()).map(p => ({
      id: p.id,
      name: p.name,
      status: p.status,
      healthy: this.heartbeatMonitor.isHealthy(p.id),
      sessions: this.sessionStore.getSessionsForProject(p.id).map(s => ({
        id: s.id,
        name: s.name,
        state: s.state,
        model: s.model,
        branch: s.branch,
        cwd: s.cwd,
        costUsd: s.usage.costUsd,
        hasPendingPermission: Object.keys(s.pendingControls).length > 0,
        stateSince: s.stateSince,
        extraFlags: s.extraFlags ?? '',
      })),
    }))
  }

  /** Current filter project ID from localStorage. Empty string = no filter. */
  get filterProjectId(): string {
    return this._storage.getItem(FILTER_PROJECT_KEY) ?? ''
  }

  /**
   * Set the project filter. Pass empty string to clear.
   * Persists to localStorage and notifies listeners.
   */
  setFilter(projectId: string): void {
    if (projectId) {
      this._storage.setItem(FILTER_PROJECT_KEY, projectId)
    } else {
      this._storage.removeItem(FILTER_PROJECT_KEY)
    }
    this._notify()
  }

  /**
   * Returns the effective filter project ID after validating that the
   * stored project still exists in the KV store. If the project no longer
   * exists, clears the filter automatically and returns empty string.
   */
  resolveFilter(): string {
    const stored = this.filterProjectId
    if (!stored) return ''
    const exists = Array.from(this.sessionStore.projects.values()).some(p => p.id === stored)
    if (!exists) {
      this._storage.removeItem(FILTER_PROJECT_KEY)
      return ''
    }
    return stored
  }

  /**
   * Projects sorted alphabetically by name. When a valid project filter is active,
   * returns only that one project's group.
   */
  get sortedGroups(): ProjectGroup[] {
    const allProjects = this.projects
    const effectiveFilter = this.resolveFilter()

    const sorted = [...allProjects].sort((a, b) => a.name.localeCompare(b.name))

    const filtered = effectiveFilter
      ? sorted.filter(p => p.id === effectiveFilter)
      : sorted

    return filtered.map(p => ({
      project: p,
      sessions: [...p.sessions].sort((a, b) =>
        b.stateSince.localeCompare(a.stateSince)
      ),
    }))
  }

  async createProject(name: string, gitUrl?: string, gitIdentityId?: string): Promise<string> {
    const subject = `mclaude.${this.userId}.api.projects.create`
    const payload: Record<string, string> = { name }
    if (gitUrl) payload['gitUrl'] = gitUrl
    if (gitIdentityId) payload['gitIdentityId'] = gitIdentityId
    const reply = await this.natsClient.request(subject, new TextEncoder().encode(JSON.stringify(payload)))
    const result = JSON.parse(new TextDecoder().decode(reply.data)) as { id?: string; error?: string }
    if (!result.id) throw new Error(result.error ?? 'createProject: no id in reply')
    return result.id
  }

  async createSession(projectId: string, branch: string, name: string, opts?: { extraFlags?: string }): Promise<string> {
    const requestId = crypto.randomUUID()
    const subject = `mclaude.${this.userId}.${projectId}.api.sessions.create`
    const payload = {
      projectId,
      branch,
      name,
      requestId,
      ...(opts?.extraFlags !== undefined ? { extraFlags: opts.extraFlags } : {}),
    }
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify(payload)))

    return new Promise((resolve, reject) => {
      let timer: ReturnType<typeof setTimeout>
      let unsubKV: (() => void) | undefined
      let unsubErr: (() => void) | undefined

      const cleanup = () => {
        clearTimeout(timer)
        unsubKV?.()
        unsubErr?.()
      }

      timer = setTimeout(() => {
        cleanup()
        reject(new Error('Create session timed out'))
      }, 30_000)

      // Success: session appears in KV (watched by session-store)
      unsubKV = this.sessionStore.onSessionAdded(projectId, (session) => {
        cleanup()
        resolve(session.id)
      })

      // Error: temporary core NATS sub on project-level _api subject
      unsubErr = this.natsClient.subscribe(
        `mclaude.${this.userId}.${projectId}.events._api`,
        (msg) => {
          try {
            const event = JSON.parse(new TextDecoder().decode(msg.data)) as { type?: string; request_id?: string; error?: string }
            if (event.type === 'api_error' && event.request_id === requestId) {
              cleanup()
              reject(new Error(event.error ?? 'api_error'))
            }
          } catch {
            // ignore parse errors
          }
        }
      )
    })
  }

  async deleteSession(sessionId: string): Promise<void> {
    // Find which project this session belongs to
    const session = this.sessionStore.sessions.get(sessionId)
    if (!session) return
    const subject = `mclaude.${this.userId}.${session.projectId}.api.sessions.delete`
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify({ sessionId })))
  }

  async restartSession(sessionId: string, opts?: { extraFlags?: string }): Promise<void> {
    const subject = `mclaude.${this.userId}.api.sessions.restart`
    const payload = {
      sessionId,
      ...(opts?.extraFlags !== undefined ? { extraFlags: opts.extraFlags } : {}),
    }
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify(payload)))
  }

  onProjectsChanged(listener: SessionListListener): () => void {
    this._listeners.push(listener)
    return () => { this._listeners = this._listeners.filter(l => l !== listener) }
  }

  destroy(): void {
    for (const u of this._unsubscribers) u()
    this._unsubscribers = []
  }

  private _notify(): void {
    for (const l of this._listeners) l(this.projects)
  }
}
