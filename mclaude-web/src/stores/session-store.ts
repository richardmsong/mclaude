import type { INATSClient, SessionKVState, ProjectKVState } from '@/types'
import { logger } from '@/logger'

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

    // Watch sessions — per-user bucket (ADR-0054). Bucket name encodes user slug.
    // Key format: hosts.{hslug}.projects.{pslug}.sessions.{sslug}
    // Use > wildcard to match all keys within the per-user bucket.
    const unwatch1 = this.natsClient.kvWatch(`mclaude-sessions-${this.userSlug}`, '>', (entry) => {
      if (entry.operation === 'DEL' || entry.operation === 'PURGE') {
        // Key format: hosts.{hslug}.projects.{pslug}.sessions.{sslug}
        // The _sessions map is keyed by UUID. Find the session by slug from the last key segment.
        const parts = entry.key.split('.')
        const sessionSlug = parts[parts.length - 1]
        if (sessionSlug) {
          // Iterate sessions to find the UUID matching this slug
          let uuidToDelete: string | undefined
          for (const [uuid, session] of this._sessions) {
            if (session.slug === sessionSlug) {
              uuidToDelete = uuid
              break
            }
          }
          if (uuidToDelete) {
            this._sessions.delete(uuidToDelete)
          }
        }
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
        // Malformed value — ignore silently
        this._notifySessions()
      }
    })
    this._unwatchers.push(unwatch1)

    // Watch projects — per-user bucket (ADR-0054). Bucket name encodes user slug.
    // Key format: hosts.{hslug}.projects.{pslug}
    const unwatch2 = this.natsClient.kvWatch(`mclaude-projects-${this.userSlug}`, '>', (entry) => {
      // Spec: explicitly check DEL/PURGE instead of relying on parse failure
      if (entry.operation === 'DEL' || entry.operation === 'PURGE') {
        // Key format: hosts.{hslug}.projects.{pslug}
        // The _projects map is keyed by UUID. Find the project by slug from the last key segment.
        const parts = entry.key.split('.')
        const projectSlug = parts[parts.length - 1]
        if (projectSlug) {
          let uuidToDelete: string | undefined
          for (const [uuid, project] of this._projects) {
            if (project.slug === projectSlug) {
              uuidToDelete = uuid
              break
            }
          }
          if (uuidToDelete) {
            this._projects.delete(uuidToDelete)
          }
        }
        this._notifyProjects()
        return
      }
      try {
        const state = JSON.parse(new TextDecoder().decode(entry.value)) as ProjectKVState
        this._projects.set(state.id, state)
        this._notifyProjects()
      } catch {
        logger.warn({ component: 'session-store', key: entry.key }, 'malformed project KV entry')
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
