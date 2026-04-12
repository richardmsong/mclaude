import type { IAuthClient, INATSClient, AuthTokens } from '@/types'
import { logger } from '@/logger'

export type AuthStatus = 'unauthenticated' | 'authenticated' | 'refreshing' | 'expired'

export interface AuthState {
  userId: string | null
  jwt: string | null
  status: AuthStatus
}

export type AuthListener = (state: AuthState) => void

export class AuthStore {
  private _state: AuthState = { userId: null, jwt: null, status: 'unauthenticated' }
  private _listeners: AuthListener[] = []
  private _refreshTimer: ReturnType<typeof setInterval> | null = null
  private _tokens: AuthTokens | null = null

  constructor(
    private readonly authClient: IAuthClient,
    private readonly natsClient: INATSClient,
  ) {}

  get state(): AuthState {
    return this._state
  }

  async login(email: string, password: string): Promise<void> {
    const tokens = await this.authClient.login(email, password)
    this._tokens = tokens
    this._setState({ userId: tokens.userId, jwt: tokens.jwt, status: 'authenticated' })
  }

  async loginSSO(provider: string): Promise<string> {
    return this.authClient.loginSSO(provider)
  }

  async logout(): Promise<void> {
    this._stopRefreshLoop()
    await this.authClient.logout()
    this._tokens = null
    this._setState({ userId: null, jwt: null, status: 'unauthenticated' })
    await this.natsClient.close()
  }

  startRefreshLoop(checkIntervalMs = 60_000): void {
    this._stopRefreshLoop()
    this._refreshTimer = setInterval(() => {
      void this._checkAndRefresh()
    }, checkIntervalMs)
  }

  stopRefreshLoop(): void {
    this._stopRefreshLoop()
  }

  private _stopRefreshLoop(): void {
    if (this._refreshTimer !== null) {
      clearInterval(this._refreshTimer)
      this._refreshTimer = null
    }
  }

  private async _checkAndRefresh(): Promise<void> {
    if (!this._tokens) return
    const expiry = this._decodeExpiry(this._tokens.jwt)
    if (!expiry) return

    const ttlMs = expiry * 1000 - Date.now()
    if (ttlMs > 5 * 60 * 1000) return // More than 5 min left — no action

    this._setState({ ...this._state, status: 'refreshing' })
    try {
      const { jwt } = await this.authClient.refresh()
      this._tokens = { ...this._tokens, jwt }
      this._setState({ ...this._state, jwt, status: 'authenticated' })
      await this.natsClient.reconnect(jwt)
      logger.info({ component: 'auth-store', userId: this._state.userId }, 'JWT refreshed')
    } catch (err) {
      logger.error({ component: 'auth-store', err }, 'JWT refresh failed')
      this._setState({ ...this._state, status: 'expired' })
    }
  }

  private _decodeExpiry(jwt: string): number | null {
    try {
      const parts = jwt.split('.')
      if (parts.length !== 3) return null
      const payload = JSON.parse(atob(parts[1])) as { exp?: number }
      return payload.exp ?? null
    } catch {
      return null
    }
  }

  onStateChanged(listener: AuthListener): () => void {
    this._listeners.push(listener)
    return () => {
      this._listeners = this._listeners.filter(l => l !== listener)
    }
  }

  private _setState(state: AuthState): void {
    this._state = state
    for (const l of this._listeners) l(state)
  }
}
