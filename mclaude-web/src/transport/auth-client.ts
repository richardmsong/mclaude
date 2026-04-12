import type { IAuthClient, AuthTokens } from '@/types'

export class AuthClient implements IAuthClient {
  private _tokens: AuthTokens | null = null

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
  }

  getStoredTokens(): AuthTokens | null {
    return this._tokens
  }

  storeTokens(tokens: AuthTokens): void {
    this._tokens = tokens
  }

  clearTokens(): void {
    this._tokens = null
  }
}
