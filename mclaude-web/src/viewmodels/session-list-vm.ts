import type { INATSClient } from '@/types'
import type { SessionStore } from '@/stores/session-store'
import type { HeartbeatMonitor } from '@/stores/heartbeat-monitor'
import {
  subjProjectsCreate,
  subjSessionsCreate,
  subjSessionsDelete,
  subjSessionsRestart,
  subjEventsApi,
} from '@/lib/subj'
import type { UserSlug, HostSlug, ProjectSlug } from '@/lib/slug'

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
  /** Host slug from session KV (ADR-0035). */
  hostSlug: string
}

export interface ProjectVM {
  id: string
  name: string
  status: string
  healthy: boolean
  sessions: SessionVM[]
  /** Host slug from project KV (ADR-0035). Falls back to 'local'. */
  hostSlug: string
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
    userId: string,
    storage?: IStorage,
    /** User slug for subject construction (ADR-0024). Falls back to userId when absent. */
    private readonly userSlug: string = userId,
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
      healthy: this.heartbeatMonitor.isHealthy(p.hostSlug ?? 'local'),
      hostSlug: p.hostSlug ?? 'local',
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
        hostSlug: s.hostSlug ?? p.hostSlug ?? 'local',
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

  async createProject(name: string, gitUrl?: string, gitIdentityId?: string, hostSlug?: string): Promise<string> {
    const hslug = (hostSlug ?? 'local') as HostSlug
    const subject = subjProjectsCreate(this.userSlug as UserSlug, hslug)
    const payload: Record<string, string> = { name, hostSlug: hslug }
    if (gitUrl) payload['gitUrl'] = gitUrl
    if (gitIdentityId) payload['gitIdentityId'] = gitIdentityId
    const reply = await this.natsClient.request(subject, new TextEncoder().encode(JSON.stringify(payload)))
    const result = JSON.parse(new TextDecoder().decode(reply.data)) as { id?: string; error?: string }
    if (!result.id) throw new Error(result.error ?? 'createProject: no id in reply')
    return result.id
  }

  async createSession(projectId: string, branch: string, name: string, opts?: { extraFlags?: string; cwd?: string; joinWorktree?: boolean; permPolicy?: string; quotaMonitor?: boolean }): Promise<string> {
    const requestId = crypto.randomUUID()
    // ADR-0035: use host-scoped subject. Resolve hostSlug + pslug from project KV.
    const project = this.sessionStore.projects.get(projectId)
    const pslug = (project?.slug ?? projectId) as ProjectSlug
    const hslug = (project?.hostSlug ?? 'local') as HostSlug
    const subject = subjSessionsCreate(this.userSlug as UserSlug, hslug, pslug)
    const payload = {
      projectId,
      branch,
      name,
      requestId,
      ...(opts?.extraFlags !== undefined ? { extraFlags: opts.extraFlags } : {}),
      ...(opts?.cwd !== undefined ? { cwd: opts.cwd } : {}),
      ...(opts?.joinWorktree !== undefined ? { joinWorktree: opts.joinWorktree } : {}),
      ...(opts?.permPolicy !== undefined ? { permPolicy: opts.permPolicy } : {}),
      ...(opts?.quotaMonitor !== undefined ? { quotaMonitor: opts.quotaMonitor } : {}),
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
        subjEventsApi(this.userSlug as UserSlug, hslug, pslug),
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
    const delProject = this.sessionStore.projects.get(session.projectId)
    const delPslug = (delProject?.slug ?? session.projectId) as ProjectSlug
    const delHslug = (delProject?.hostSlug ?? session.hostSlug ?? 'local') as HostSlug
    const subject = subjSessionsDelete(this.userSlug as UserSlug, delHslug, delPslug)
    const deleteRequestId = crypto.randomUUID()
    this.natsClient.publish(subject, new TextEncoder().encode(JSON.stringify({ sessionId, requestId: deleteRequestId })))
  }

  async restartSession(sessionId: string, opts?: { extraFlags?: string }): Promise<void> {
    const session = this.sessionStore.sessions.get(sessionId)
    if (!session) return
    const rstProject = this.sessionStore.projects.get(session.projectId)
    const rstPslug = (rstProject?.slug ?? session.projectId) as ProjectSlug
    const rstHslug = (rstProject?.hostSlug ?? session.hostSlug ?? 'local') as HostSlug
    const subject = subjSessionsRestart(this.userSlug as UserSlug, rstHslug, rstPslug)
    const restartRequestId = crypto.randomUUID()
    const payload = {
      sessionId,
      requestId: restartRequestId,
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
