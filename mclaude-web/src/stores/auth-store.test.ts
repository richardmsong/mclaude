import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { AuthStore } from './auth-store'
import { MockAuthClient } from '../testutil/mock-auth'
import { MockNATSClient } from '../testutil/mock-nats'

function makeJWT(expSeconds: number): string {
  const header = btoa(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).replace(/=/g, '')
  const payload = btoa(JSON.stringify({ sub: 'user-1', userId: 'user-1', exp: expSeconds })).replace(/=/g, '')
  return `${header}.${payload}.mock-sig`
}

describe('AuthStore', () => {
  let mockAuth: MockAuthClient
  let mockNats: MockNATSClient
  let store: AuthStore

  beforeEach(() => {
    mockAuth = new MockAuthClient()
    mockNats = new MockNATSClient()
    store = new AuthStore(mockAuth, mockNats)
  })

  afterEach(() => {
    store.stopRefreshLoop()
    vi.useRealTimers()
  })

  describe('login', () => {
    it('calls authClient.login and sets state to authenticated with correct userId/jwt', async () => {
      await store.login('user@example.com', 'password')
      expect(mockAuth.loginCalls).toHaveLength(1)
      expect(mockAuth.loginCalls[0]).toEqual({ email: 'user@example.com', password: 'password' })
      expect(store.state.status).toBe('authenticated')
      expect(store.state.userId).toBe('user-1')
      expect(store.state.jwt).toBe(mockAuth.loginResponse.jwt)
    })

    it('throws on login failure', async () => {
      mockAuth.shouldFailLogin = true
      await expect(store.login('user@example.com', 'bad')).rejects.toThrow('Login failed')
      expect(store.state.status).toBe('unauthenticated')
    })
  })

  describe('logout', () => {
    it('calls authClient.logout and sets state to unauthenticated', async () => {
      await store.login('user@example.com', 'password')
      expect(store.state.status).toBe('authenticated')
      await store.logout()
      expect(mockAuth.logoutCalls).toHaveLength(1)
      expect(store.state.status).toBe('unauthenticated')
      expect(store.state.userId).toBeNull()
      expect(store.state.jwt).toBeNull()
    })
  })

  describe('JWT refresh loop', () => {
    it('calls authClient.refresh and updates state.jwt when JWT is near expiry', async () => {
      vi.useFakeTimers()

      // Login first to set tokens
      await store.login('user@example.com', 'password')

      // Override the stored JWT to expire in 2 minutes (< 5 min threshold)
      const nearExpiryJwt = makeJWT(Math.floor(Date.now() / 1000) + 120)
      mockAuth.loginResponse = {
        ...mockAuth.loginResponse,
        jwt: nearExpiryJwt,
      }
      // Re-login to store near-expiry token
      await store.login('user@example.com', 'password')

      const checkIntervalMs = 1000
      store.startRefreshLoop(checkIntervalMs)

      expect(mockAuth.refreshCalls).toHaveLength(0)
      // Advance exactly one interval — one tick of the setInterval
      await vi.advanceTimersByTimeAsync(checkIntervalMs + 10)
      store.stopRefreshLoop() // stop before runAllTimers to avoid infinite loop

      expect(mockAuth.refreshCalls.length).toBeGreaterThanOrEqual(1)
      expect(store.state.jwt).toBe(mockAuth.refreshResponse.jwt)
      expect(store.state.status).toBe('authenticated')
    })

    it('does NOT call refresh when JWT has plenty of time left', async () => {
      vi.useFakeTimers()

      // JWT with expiry in 1 hour
      const farFutureJwt = makeJWT(Math.floor(Date.now() / 1000) + 3600)
      mockAuth.loginResponse = { ...mockAuth.loginResponse, jwt: farFutureJwt }
      await store.login('user@example.com', 'password')

      const checkIntervalMs = 1000
      store.startRefreshLoop(checkIntervalMs)

      await vi.advanceTimersByTimeAsync(checkIntervalMs + 10)
      store.stopRefreshLoop()

      expect(mockAuth.refreshCalls).toHaveLength(0)
    })
  })

  describe('refresh failure', () => {
    it('sets state.status to expired when refresh fails', async () => {
      vi.useFakeTimers()

      mockAuth.shouldFailRefresh = true
      const nearExpiryJwt = makeJWT(Math.floor(Date.now() / 1000) + 120)
      mockAuth.loginResponse = { ...mockAuth.loginResponse, jwt: nearExpiryJwt }
      await store.login('user@example.com', 'password')

      const checkIntervalMs = 1000
      store.startRefreshLoop(checkIntervalMs)
      await vi.advanceTimersByTimeAsync(checkIntervalMs + 10)
      store.stopRefreshLoop()

      expect(store.state.status).toBe('expired')
    })
  })

  describe('listener notifications', () => {
    it('onStateChanged fires when state changes', async () => {
      const states: string[] = []
      store.onStateChanged((s) => states.push(s.status))
      await store.login('user@example.com', 'password')
      expect(states).toContain('authenticated')
    })

    it('onStateChanged fires on logout', async () => {
      await store.login('user@example.com', 'password')
      const states: string[] = []
      store.onStateChanged((s) => states.push(s.status))
      await store.logout()
      expect(states).toContain('unauthenticated')
    })

    it('unsubscribe stops notifications', async () => {
      let callCount = 0
      const unsub = store.onStateChanged(() => { callCount++ })
      await store.login('user@example.com', 'password')
      unsub()
      await store.logout()
      expect(callCount).toBe(1)
    })
  })
})
