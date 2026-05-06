import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { AuthClient } from './auth-client'

// ── ADR-0100: client-side NKey generation ────────────────────────────────────
//
// Before calling POST /auth/login the AuthClient must:
//   1. Generate a fresh NKey user key pair.
//   2. Include nkey_public in the request body.
//   3. Return tokens with nkeySeed and nkeyPublic set to locally-generated values.
//   4. NOT read nkeySeed from the server response.

describe('AuthClient — ADR-0100 client-side NKey generation', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    // Stub localStorage
    const store: Record<string, string> = {}
    vi.stubGlobal('localStorage', {
      getItem: (k: string) => store[k] ?? null,
      setItem: (k: string, v: string) => { store[k] = v },
      removeItem: (k: string) => { delete store[k] },
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('sends nkey_public in the login request body', async () => {
    // Server returns a response WITHOUT nkeySeed (ADR-0100)
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ jwt: 'test-jwt', userId: 'user-1', userSlug: 'user-1', natsUrl: 'wss://example.com/nats' }),
    })

    const client = new AuthClient('https://example.com')
    await client.login('user@example.com', 'password')

    expect(fetchMock).toHaveBeenCalledOnce()
    const [url, opts] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('https://example.com/auth/login')
    const body = JSON.parse(opts.body as string) as Record<string, string>
    expect(body).toHaveProperty('nkey_public')
    // NKey public keys for user type start with 'U'
    expect(body['nkey_public']).toMatch(/^U[A-Z2-7]+$/)
  })

  it('returns tokens with locally-generated nkeySeed (not from server response)', async () => {
    // Server response deliberately omits nkeySeed
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ jwt: 'test-jwt', userId: 'user-1', userSlug: 'user-1', natsUrl: 'wss://example.com/nats' }),
    })

    const client = new AuthClient('https://example.com')
    const tokens = await client.login('user@example.com', 'password')

    // Seed must be present and non-empty (locally generated)
    expect(tokens.nkeySeed).toBeTruthy()
    // NKey seeds start with 'SU' for user keys
    expect(tokens.nkeySeed).toMatch(/^SU/)
  })

  it('returns tokens with nkeyPublic matching what was sent to the server', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ jwt: 'test-jwt', userId: 'user-1', userSlug: 'user-1', natsUrl: 'wss://example.com/nats' }),
    })

    const client = new AuthClient('https://example.com')
    const tokens = await client.login('user@example.com', 'password')

    const [, opts] = fetchMock.mock.calls[0] as [string, RequestInit]
    const body = JSON.parse(opts.body as string) as Record<string, string>

    // Public key in tokens must match what was sent in the request
    expect(tokens.nkeyPublic).toBe(body['nkey_public'])
  })

  it('does not carry nkeySeed from server response into tokens', async () => {
    // Even if the server (incorrectly) returns nkeySeed, the client must ignore it
    // and use the locally-generated seed.
    const serverSeed = 'SUSERVER_SHOULD_NOT_BE_USED'
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        jwt: 'test-jwt',
        userId: 'user-1',
        userSlug: 'user-1',
        natsUrl: 'wss://example.com/nats',
        nkeySeed: serverSeed, // legacy server sending seed — must be ignored
      }),
    })

    const client = new AuthClient('https://example.com')
    const tokens = await client.login('user@example.com', 'password')

    // The client-generated seed must not equal the (wrong) server-returned seed
    expect(tokens.nkeySeed).not.toBe(serverSeed)
    // Must still be a valid locally-generated seed
    expect(tokens.nkeySeed).toMatch(/^SU/)
  })

  it('each login call generates a fresh NKey pair', async () => {
    fetchMock.mockResolvedValue({
      ok: true,
      json: async () => ({ jwt: 'test-jwt', userId: 'user-1', userSlug: 'user-1', natsUrl: 'wss://example.com/nats' }),
    })

    const client = new AuthClient('https://example.com')
    const tokens1 = await client.login('user@example.com', 'password')
    const tokens2 = await client.login('user@example.com', 'password')

    // Each login generates a distinct key pair
    expect(tokens1.nkeyPublic).not.toBe(tokens2.nkeyPublic)
    expect(tokens1.nkeySeed).not.toBe(tokens2.nkeySeed)
  })

  it('throws on login failure without corrupting key state', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 401,
      text: async () => 'invalid credentials',
    })

    const client = new AuthClient('https://example.com')
    await expect(client.login('user@example.com', 'bad')).rejects.toThrow('invalid credentials')
  })
})
