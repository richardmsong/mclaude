import type { IAuthClient, AuthTokens } from '@/types'

export class MockAuthClient implements IAuthClient {
  private _tokens: AuthTokens | null = null

  // Configurable responses for tests
  loginResponse: AuthTokens = {
    jwt: 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEiLCJ1c2VySWQiOiJ1c2VyLTEiLCJleHAiOjk5OTk5OTk5OTl9.mock',
    nkeySeed: 'SUAMK4YJBQIXAQCVBKC7NHVLKIXJKFHHMF5E5KJQHIKFPBCPHHG7VMJGY',
    userId: 'user-1',
  }
  refreshResponse: { jwt: string } = {
    jwt: 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEiLCJ1c2VySWQiOiJ1c2VyLTEiLCJleHAiOjk5OTk5OTk5OTl9.refreshed',
  }
  shouldFailLogin = false
  shouldFailRefresh = false

  readonly loginCalls: Array<{ email: string; password: string }> = []
  readonly refreshCalls: number[] = []
  readonly logoutCalls: number[] = []

  async login(email: string, password: string): Promise<AuthTokens> {
    this.loginCalls.push({ email, password })
    if (this.shouldFailLogin) throw new Error('Login failed')
    this._tokens = this.loginResponse
    return this.loginResponse
  }

  async loginSSO(_provider: string): Promise<string> {
    return 'https://sso.example.com/callback'
  }

  async refresh(): Promise<{ jwt: string }> {
    this.refreshCalls.push(Date.now())
    if (this.shouldFailRefresh) throw new Error('Refresh failed')
    if (this._tokens) this._tokens = { ...this._tokens, ...this.refreshResponse }
    return this.refreshResponse
  }

  async logout(): Promise<void> {
    this.logoutCalls.push(Date.now())
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

  async getMe(): Promise<{ userId: string; email: string; name: string; connectedProviders: import('@/types').ConnectedProvider[] }> {
    return { userId: 'user-1', email: 'test@example.com', name: 'Test User', connectedProviders: [] }
  }

  async getAdminProviders(): Promise<import('@/types').AdminProvider[]> {
    return []
  }

  async startOAuthConnect(_providerId: string, _returnUrl: string): Promise<string> {
    return 'https://github.com/login/oauth/authorize?client_id=mock'
  }

  async getConnectionRepos(_connectionId: string, _query: string, _page?: number): Promise<import('@/types').RepoListResponse> {
    return { repos: [], nextPage: null, hasMore: false }
  }

  async disconnectConnection(_connectionId: string): Promise<void> {
    // no-op in tests
  }

  async addPAT(_baseUrl: string, _displayName: string, _token: string): Promise<{ connectionId: string; providerType: string; displayName: string; username: string }> {
    return { connectionId: 'conn-mock', providerType: 'github', displayName: 'Mock', username: 'mock-user' }
  }

  async updateProjectIdentity(_userSlug: string, _projectSlug: string, _connectionId: string | null): Promise<void> {
    // no-op in tests
  }
}
