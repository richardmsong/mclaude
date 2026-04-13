import type { INATSClient, SessionKVState, ProjectKVState } from '@/types'
import { logger } from '@/logger'

export type SessionStoreListener = (sessions: Map<string, SessionKVState>) => void
export type ProjectStoreListener = (projects: Map<string, ProjectKVState>) => void

export class SessionStore {
  private _sessions = new Map<string, SessionKVState>()
  private _projects = new Map<string, ProjectKVState>()
  private _sessionListeners: SessionStoreListener[] = []
  private _projectListeners: ProjectStoreListener[] = []
  private _unwatchers: Array<() => void> = []

  constructor(
    private readonly natsClient: INATSClient,
    private readonly userId: string,
  ) {}

  get sessions(): Map<string, SessionKVState> {
    return this._sessions
  }

  get projects(): Map<string, ProjectKVState> {
    return this._projects
  }

  startWatching(): void {
    this._stopWatching()

    // Watch sessions — key format: "{userId}.{sessionId}" (NATS "." separator for wildcard support)
    const sessionKey = `${this.userId}.*`
    const unwatch1 = this.natsClient.kvWatch('mclaude-sessions', sessionKey, (entry) => {
      try {
        const state = JSON.parse(new TextDecoder().decode(entry.value)) as SessionKVState
        this._sessions.set(state.id, state)
        logger.debug({ component: 'session-store', sessionId: state.id, userId: this.userId }, 'session updated')
        this._notifySessions()
      } catch {
        // Deleted key or malformed
        const parts = entry.key.split('.')
        const sessionId = parts[parts.length - 1]
        this._sessions.delete(sessionId)
        this._notifySessions()
      }
    })
    this._unwatchers.push(unwatch1)

    // Watch projects — key format: "{userId}.{projectId}"
    const projectKey = `${this.userId}.*`
    const unwatch2 = this.natsClient.kvWatch('mclaude-projects', projectKey, (entry) => {
      try {
        const state = JSON.parse(new TextDecoder().decode(entry.value)) as ProjectKVState
        this._projects.set(state.id, state)
        this._notifyProjects()
      } catch {
        const parts = entry.key.split('.')
        const projectId = parts[parts.length - 1]
        this._projects.delete(projectId)
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
}
