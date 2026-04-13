import type { INATSClient } from '@/types'
import type { SessionStore } from '@/stores/session-store'
import type { HeartbeatMonitor } from '@/stores/heartbeat-monitor'

export interface SessionVM {
  id: string
  name: string
  state: string
  model: string
  branch: string
  costUsd: number
  hasPendingPermission: boolean
}

export interface ProjectVM {
  id: string
  name: string
  status: string
  healthy: boolean
  sessions: SessionVM[]
}

export type SessionListListener = (projects: ProjectVM[]) => void

export class SessionListVM {
  private _listeners: SessionListListener[] = []
  private _unsubscribers: Array<() => void> = []

  constructor(
    private readonly sessionStore: SessionStore,
    private readonly heartbeatMonitor: HeartbeatMonitor,
    private readonly natsClient: INATSClient,
    private readonly userId: string,
  ) {
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
        costUsd: s.usage.costUsd,
        hasPendingPermission: Object.keys(s.pendingControls).length > 0,
      })),
    }))
  }

  async createProject(name: string, gitUrl?: string): Promise<string> {
    const subject = `mclaude.${this.userId}.api.projects.create`
    const payload: Record<string, string> = { name }
    if (gitUrl) payload['gitUrl'] = gitUrl
    const reply = await this.natsClient.request(subject, new TextEncoder().encode(JSON.stringify(payload)))
    const result = JSON.parse(new TextDecoder().decode(reply.data)) as { id?: string; error?: string }
    if (result.error) throw new Error(result.error)
    return result.id!
  }

  async createSession(projectId: string, branch: string, name: string): Promise<string> {
    const subject = `mclaude.${this.userId}.${projectId}.api.sessions.create`
    const payload = { projectId, branch, name }
    const reply = await this.natsClient.request(subject, new TextEncoder().encode(JSON.stringify(payload)))
    const result = JSON.parse(new TextDecoder().decode(reply.data)) as { id?: string; error?: string }
    if (result.error) throw new Error(result.error)
    return result.id!
  }

  async deleteSession(sessionId: string): Promise<void> {
    // Find which project this session belongs to
    const session = this.sessionStore.sessions.get(sessionId)
    if (!session) return
    const subject = `mclaude.${this.userId}.${session.projectId}.api.sessions.delete`
    await this.natsClient.request(subject, new TextEncoder().encode(JSON.stringify({ sessionId })))
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
