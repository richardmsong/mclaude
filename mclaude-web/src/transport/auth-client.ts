import type { IAuthClient, AuthTokens, AdminProvider, ConnectedProvider, RepoListResponse } from '@/types'

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
    if (!res.ok) {
      const body = await res.text().catch(() => '')
      throw new Error(body || `Login failed: ${res.status}`)
    }
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

  async getMe(): Promise<{ userId: string; email: string; name: string; connectedProviders: ConnectedProvider[] }> {
    const res = await fetch(`${this.baseUrl}/auth/me`, {
      headers: this._tokens ? { Authorization: `Bearer ${this._tokens.jwt}` } : {},
    })
    if (!res.ok) throw new Error(`/auth/me failed: ${res.status}`)
    const data = await res.json() as { userId: string; email: string; name: string; connectedProviders?: ConnectedProvider[] }
    return {
      userId: data.userId,
      email: data.email,
      name: data.name,
      connectedProviders: data.connectedProviders ?? [],
    }
  }

  async getAdminProviders(): Promise<AdminProvider[]> {
    const res = await fetch(`${this.baseUrl}/api/providers`, {
      headers: this._tokens ? { Authorization: `Bearer ${this._tokens.jwt}` } : {},
    })
    if (!res.ok) throw new Error(`/api/providers failed: ${res.status}`)
    const data = await res.json() as { providers: AdminProvider[] }
    return data.providers ?? []
  }

  async startOAuthConnect(providerId: string, returnUrl: string): Promise<string> {
    const res = await fetch(`${this.baseUrl}/api/providers/${providerId}/connect`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(this._tokens ? { Authorization: `Bearer ${this._tokens.jwt}` } : {}),
      },
      body: JSON.stringify({ returnUrl }),
    })
    if (!res.ok) throw new Error(`connect failed: ${res.status}`)
    const { redirectUrl } = await res.json() as { redirectUrl: string }
    return redirectUrl
  }

  async getConnectionRepos(connectionId: string, query: string, page = 1): Promise<RepoListResponse> {
    const params = new URLSearchParams({ page: String(page) })
    if (query) params.set('q', query)
    const res = await fetch(`${this.baseUrl}/api/connections/${connectionId}/repos?${params}`, {
      headers: this._tokens ? { Authorization: `Bearer ${this._tokens.jwt}` } : {},
    })
    if (res.status === 429) {
      const retryAfter = res.headers.get('retry-after')
      throw new Error(retryAfter ? `Rate limited — try again in ${retryAfter}s` : 'Rate limited — try again shortly')
    }
    if (!res.ok) throw new Error(`repo list failed: ${res.status}`)
    return res.json() as Promise<RepoListResponse>
  }

  async disconnectConnection(connectionId: string): Promise<void> {
    const res = await fetch(`${this.baseUrl}/api/connections/${connectionId}`, {
      method: 'DELETE',
      headers: this._tokens ? { Authorization: `Bearer ${this._tokens.jwt}` } : {},
    })
    if (!res.ok) throw new Error(`disconnect failed: ${res.status}`)
  }

  async addPAT(baseUrl: string, displayName: string, token: string): Promise<{ connectionId: string; providerType: string; displayName: string; username: string }> {
    const res = await fetch(`${this.baseUrl}/api/providers/pat`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(this._tokens ? { Authorization: `Bearer ${this._tokens.jwt}` } : {}),
      },
      body: JSON.stringify({ baseUrl, displayName, token }),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({})) as { error?: string }
      throw new Error(body.error ?? `addPAT failed: ${res.status}`)
    }
    return res.json() as Promise<{ connectionId: string; providerType: string; displayName: string; username: string }>
  }

  async updateProjectIdentity(projectId: string, connectionId: string | null): Promise<void> {
    const res = await fetch(`${this.baseUrl}/api/projects/${projectId}`, {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json',
        ...(this._tokens ? { Authorization: `Bearer ${this._tokens.jwt}` } : {}),
      },
      body: JSON.stringify({ gitIdentityId: connectionId }),
    })
    if (!res.ok) throw new Error(`updateProjectIdentity failed: ${res.status}`)
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
