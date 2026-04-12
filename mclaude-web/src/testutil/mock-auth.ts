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
}
