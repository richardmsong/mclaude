import type { INATSClient, SessionKVState, ProjectKVState } from '@/types'
import { logger } from '@/logger'
import { kvKeySessionsForUser, kvKeyProjectsForUser } from '@/lib/subj'
import type { UserSlug } from '@/lib/slug'

export type SessionStoreListener = (sessions: Map<string, SessionKVState>) => void
export type ProjectStoreListener = (projects: Map<string, ProjectKVState>) => void
export type SessionAddedListener = (id: string, session: SessionKVState) => void

export class SessionStore {
  private _sessions = new Map<string, SessionKVState>()
  private _projects = new Map<string, ProjectKVState>()
  private _sessionListeners: SessionStoreListener[] = []
  private _projectListeners: ProjectStoreListener[] = []
  private _addListeners: SessionAddedListener[] = []
  private _unwatchers: Array<() => void> = []

  constructor(
    private readonly natsClient: INATSClient,
    private readonly userId: string,
    /** User slug for KV key construction (ADR-0024). Falls back to userId when absent. */
    private readonly userSlug: string = userId,
  ) {}

  get sessions(): Map<string, SessionKVState> {
    return this._sessions
  }

  get projects(): Map<string, ProjectKVState> {
    return this._projects
  }

  startWatching(): void {
    this._stopWatching()

    // Watch sessions — keys are {uslug}.{pslug}.{sslug} (ADR-0024 typed-slug scheme)
    // Use > wildcard for multi-level match across all projects and sessions for this user
    const sessionKey = kvKeySessionsForUser(this.userSlug as UserSlug)
    const unwatch1 = this.natsClient.kvWatch('mclaude-sessions', sessionKey, (entry) => {
      if (entry.operation === 'DEL' || entry.operation === 'PURGE') {
        const parts = entry.key.split('.')
        const sessionId = parts[parts.length - 1]
        if (sessionId) this._sessions.delete(sessionId)
        this._notifySessions()
        return
      }
      try {
        const state = JSON.parse(new TextDecoder().decode(entry.value)) as SessionKVState
        this._sessions.set(state.id, state)
        logger.debug({ component: 'session-store', sessionId: state.id, userId: this.userId }, 'session updated')
        this._notifySessions()
        this._notifyAddListeners(state.id, state)
      } catch {
        // Malformed value — extract sessionId from key {userId}.{projectId}.{sessionId}
        const parts = entry.key.split('.')
        const sessionId = parts[parts.length - 1]
        if (sessionId) {
          this._sessions.delete(sessionId)
        }
        this._notifySessions()
      }
    })
    this._unwatchers.push(unwatch1)

    // Watch projects — key format: "{uslug}.{pslug}" (ADR-0024 typed-slug scheme)
    const projectKey = kvKeyProjectsForUser(this.userSlug as UserSlug)
    const unwatch2 = this.natsClient.kvWatch('mclaude-projects', projectKey, (entry) => {
      try {
        const state = JSON.parse(new TextDecoder().decode(entry.value)) as ProjectKVState
        this._projects.set(state.id, state)
        this._notifyProjects()
      } catch {
        const parts = entry.key.split('.')
        const projectId = parts[parts.length - 1]
        if (projectId) {
          this._projects.delete(projectId)
        }
        this._notifyProjects()
      }
    })
    this._unwatchers.push(unwatch2)
  }

  stopWatching(): void {
    this._stopWatching()
  }

  private _stopWatching(): void {
    for (const u of this._unwatchers) u()
    this._unwatchers = []
  }

  getSessionsForProject(projectId: string): SessionKVState[] {
    return Array.from(this._sessions.values()).filter(s => s.projectId === projectId)
  }

  /**
   * Look up a session by its slug (ADR-0024).
   * Falls back gracefully: if no session has a `slug` field matching, returns undefined.
   * Use when the route contains a session slug instead of a UUID.
   */
  getSessionBySlug(sslug: string): SessionKVState | undefined {
    for (const session of this._sessions.values()) {
      if (session.slug === sslug) return session
    }
    return undefined
  }

  /**
   * Look up a session by UUID or slug — handles both old and new route formats.
   * ADR-0024: routes use slug format; UUID format is kept for backward compat.
   */
  resolveSession(idOrSlug: string): SessionKVState | undefined {
    // Try UUID lookup first (fast path, existing routes)
    const byId = this._sessions.get(idOrSlug)
    if (byId) return byId
    // Fall back to slug scan
    return this.getSessionBySlug(idOrSlug)
  }

  onSessionChanged(listener: SessionStoreListener): () => void {
    this._sessionListeners.push(listener)
    return () => {
      this._sessionListeners = this._sessionListeners.filter(l => l !== listener)
    }
  }

  onProjectChanged(listener: ProjectStoreListener): () => void {
    this._projectListeners.push(listener)
    return () => {
      this._projectListeners = this._projectListeners.filter(l => l !== listener)
    }
  }

  private _notifySessions(): void {
    for (const l of this._sessionListeners) l(this._sessions)
  }

  private _notifyProjects(): void {
    for (const l of this._projectListeners) l(this._projects)
  }

  private _notifyAddListeners(id: string, session: SessionKVState): void {
    for (const l of this._addListeners) l(id, session)
  }

  onSessionAdded(projectId: string, cb: (session: SessionKVState) => void): () => void {
    const knownAtRegistration = new Set(this._sessions.keys())
    const handler: SessionAddedListener = (id: string, session: SessionKVState) => {
      if (session.projectId === projectId && !knownAtRegistration.has(id)) {
        cb(session)
      }
    }
    this._addListeners.push(handler)
    return () => { this._addListeners = this._addListeners.filter(l => l !== handler) }
  }
}
