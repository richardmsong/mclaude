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

  // ── ADR-0092: Anthropic OAuth token exchange proxy ────────────────────────

  /** Exchange an OAuth authorization code for an access token via CP proxy.
   *  CP forwards to claude.ai/oauth/token server-side to avoid CORS issues. */
  async exchangeOAuthCode(params: {
    code: string
    code_verifier: string
    redirect_uri: string
    client_id: string
  }): Promise<string> {
    const res = await fetch(`${this.baseUrl}/api/auth/oauth/exchange`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(this._tokens ? { Authorization: `Bearer ${this._tokens.jwt}` } : {}),
      },
      body: JSON.stringify(params),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({})) as { error?: string; error_description?: string }
      throw new Error(body.error_description ?? body.error ?? `OAuth exchange failed: ${res.status}`)
    }
    const data = await res.json() as { access_token: string }
    return data.access_token
  }

  // ── ADR-0091 / ADR-0092: Grant and user-agent pubkey endpoints ─────────────

  /** Get all host grants for the user. SPA filters to accepted grants client-side. */
  async getGrants(userSlug: string): Promise<Array<{ hostSlug: string; status: string }>> {
    const res = await fetch(`${this.baseUrl}/api/users/${userSlug}/grants`, {
      headers: this._tokens ? { Authorization: `Bearer ${this._tokens.jwt}` } : {},
    })
    if (!res.ok) throw new Error(`getGrants failed: ${res.status}`)
    const data = await res.json() as { grants: Array<{ hostSlug: string; status: string }> }
    return data.grants ?? []
  }

  /** Get the X25519 public key of the user-agent for a specific host. */
  async getUserAgentPubkey(userSlug: string, hostSlug: string): Promise<string> {
    const res = await fetch(`${this.baseUrl}/api/users/${userSlug}/hosts/${hostSlug}/user-agent/pubkey`, {
      headers: this._tokens ? { Authorization: `Bearer ${this._tokens.jwt}` } : {},
    })
    if (!res.ok) throw new Error(`getUserAgentPubkey failed: ${res.status}`)
    const data = await res.json() as { pubkey: string }
    return data.pubkey
  }

  async updateProjectIdentity(userSlug: string, projectSlug: string, connectionId: string | null): Promise<void> {
    // ADR-0024: URL uses slug-based path /api/users/{uslug}/projects/{pslug}
    const res = await fetch(`${this.baseUrl}/api/users/${userSlug}/projects/${projectSlug}`, {
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
