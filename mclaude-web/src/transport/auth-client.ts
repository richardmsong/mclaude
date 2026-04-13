import type { IAuthClient, AuthTokens } from '@/types'

export class AuthClient implements IAuthClient {
  private _tokens: AuthTokens | null = null
  private static readonly STORAGE_KEY = 'mclaude_tokens'

  constructor(private readonly baseUrl: string) {}

  async login(email: string, password: string): Promise<AuthTokens> {
    const res = await fetch(`${this.baseUrl}/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    })
    if (!res.ok) throw new Error(`Login failed: ${res.status}`)
    const tokens: AuthTokens = await res.json()
    this._tokens = tokens
    this._persist(tokens)
    return tokens
  }

  async loginSSO(provider: string): Promise<string> {
    const res = await fetch(`${this.baseUrl}/auth/sso/${provider}`)
    if (!res.ok) throw new Error(`SSO failed: ${res.status}`)
    const { redirectUrl } = await res.json()
    return redirectUrl as string
  }

  async refresh(): Promise<{ jwt: string }> {
    const res = await fetch(`${this.baseUrl}/auth/refresh`, {
      method: 'POST',
      headers: this._tokens ? { Authorization: `Bearer ${this._tokens.jwt}` } : {},
    })
    if (!res.ok) throw new Error(`Refresh failed: ${res.status}`)
    const { jwt } = await res.json()
    if (this._tokens) this._tokens = { ...this._tokens, jwt: jwt as string }
    return { jwt: jwt as string }
  }

  async logout(): Promise<void> {
    if (this._tokens) {
      await fetch(`${this.baseUrl}/auth/logout`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${this._tokens.jwt}` },
      }).catch(() => {})
    }
    this._tokens = null
    this._clearPersisted()
  }

  getStoredTokens(): AuthTokens | null {
    return this._tokens
  }

  storeTokens(tokens: AuthTokens): void {
    this._tokens = tokens
    this._persist(tokens)
  }

  clearTokens(): void {
    this._tokens = null
    this._clearPersisted()
  }

  loadFromStorage(): AuthTokens | null {
    try {
      const raw = localStorage.getItem(AuthClient.STORAGE_KEY)
      if (!raw) return null
      const tokens = JSON.parse(raw) as AuthTokens
      this._tokens = tokens
      return tokens
    } catch {
      return null
    }
  }

  private _persist(tokens: AuthTokens): void {
    try { localStorage.setItem(AuthClient.STORAGE_KEY, JSON.stringify(tokens)) } catch {}
  }

  private _clearPersisted(): void {
    try { localStorage.removeItem(AuthClient.STORAGE_KEY) } catch {}
  }
}
