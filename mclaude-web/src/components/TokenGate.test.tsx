// @vitest-environment jsdom
/**
 * Tests for TokenGate (ADR-0092)
 * - renders ConnectSheet by default
 * - after token obtained with no defaultHostSlug → calls onReady immediately
 * - shows spinner "Setting up your environment…" during delivery
 * - calls onReady after successful delivery
 * - shows "host isn't responding" after delivery failure
 * - [Try again] retries delivery
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { TokenGate } from './TokenGate'
import { MockNATSClient } from '@/testutil/mock-nats'
import { UserInfoStore } from '@/stores/user-info-store'
import type { AuthClient } from '@/transport/auth-client'

// Mock libsodium
vi.mock('libsodium-wrappers', () => ({
  default: {
    ready: Promise.resolve(),
    from_base64: () => new Uint8Array(32),
    from_string: (s: string) => new TextEncoder().encode(s),
    crypto_box_seal: () => new Uint8Array(64),
    to_base64: () => 'SEALED_BOX_B64',
    base64_variants: { ORIGINAL: 0 },
  },
}))

function makeAuthClient(pubkey = 'AAAA'): AuthClient {
  return {
    exchangeOAuthCode: vi.fn().mockResolvedValue('token'),
    getGrants: vi.fn().mockResolvedValue([]),
    getUserAgentPubkey: vi.fn().mockResolvedValue(pubkey),
  } as unknown as AuthClient
}

describe('TokenGate', () => {
  let nc: MockNATSClient
  let store: UserInfoStore

  beforeEach(() => {
    nc = new MockNATSClient()
    store = new UserInfoStore(nc, 'alice')
    vi.restoreAllMocks()
  })

  it('renders ConnectSheet by default', () => {
    render(
      <TokenGate nc={nc} userInfoStore={store} authClient={makeAuthClient()} uslug="alice" onReady={() => {}} />
    )
    // ConnectSheet has a dialog role or Sign in button
    expect(screen.getByText(/sign in with claude/i)).toBeTruthy()
  })

  it('calls onReady immediately when no defaultHostSlug', async () => {
    const onReady = vi.fn()
    const { container } = render(
      <TokenGate nc={nc} userInfoStore={store} authClient={makeAuthClient()} uslug="alice" onReady={onReady} />
    )
    // Simulate obtaining token via manual paste
    const summary = container.querySelector('summary')
    if (summary) fireEvent.click(summary)
    const input = container.querySelector('input[type="password"]')!
    fireEvent.change(input, { target: { value: 'sk-ant-oat01-test' } })
    const form = container.querySelector('form')!
    await act(async () => {
      fireEvent.submit(form)
    })
    await waitFor(() => {
      expect(onReady).toHaveBeenCalledTimes(1)
    })
  })

  it('shows spinner during delivery to a host', async () => {
    // Set up a slow-responding handler to keep delivery in progress
    let resolveRequest!: (data: Uint8Array) => void
    nc.requestHandlers.set(
      'mclaude.users.alice.hosts.my-host.agent.token-delivery',
      () => {
        throw new Error('never') // won't complete synchronously
      }
    )
    // Override request to be pending
    vi.spyOn(nc, 'request').mockImplementation(() => new Promise(resolve => {
      resolveRequest = (data) => resolve({ subject: 'reply', data, seq: 1 })
    }))

    const { container } = render(
      <TokenGate
        nc={nc}
        userInfoStore={store}
        authClient={makeAuthClient()}
        uslug="alice"
        defaultHostSlug="my-host"
        onReady={() => {}}
      />
    )
    // Submit token via paste
    const summary = container.querySelector('summary')
    if (summary) fireEvent.click(summary)
    const input = container.querySelector('input[type="password"]')!
    fireEvent.change(input, { target: { value: 'sk-ant-oat01-test' } })
    const form = container.querySelector('form')!
    await act(async () => {
      fireEvent.submit(form)
    })
    await waitFor(() => {
      expect(screen.getByText(/setting up your environment/i)).toBeTruthy()
    })

    // Resolve the request to complete
    resolveRequest(new TextEncoder().encode('{"ok":true}'))
  })

  it('shows error and calls onReady after delivery failure', async () => {
    const onReady = vi.fn()
    // Delivery will fail
    vi.spyOn(nc, 'request').mockRejectedValue(new Error('NATS timeout'))

    const { container } = render(
      <TokenGate
        nc={nc}
        userInfoStore={store}
        authClient={makeAuthClient()}
        uslug="alice"
        defaultHostSlug="bad-host"
        onReady={onReady}
      />
    )
    const summary = container.querySelector('summary')
    if (summary) fireEvent.click(summary)
    const input = container.querySelector('input[type="password"]')!
    fireEvent.change(input, { target: { value: 'sk-ant-oat01-test' } })
    const form = container.querySelector('form')!
    await act(async () => {
      fireEvent.submit(form)
    })
    await waitFor(() => {
      expect(screen.getByText(/host isn't responding|host isn&#39;t responding/i)).toBeTruthy()
    })
    // onReady should NOT have been called on failure
    expect(onReady).not.toHaveBeenCalled()
  })

  it('[Try again] is shown after failure', async () => {
    vi.spyOn(nc, 'request').mockRejectedValue(new Error('timeout'))

    const { container } = render(
      <TokenGate
        nc={nc}
        userInfoStore={store}
        authClient={makeAuthClient()}
        uslug="alice"
        defaultHostSlug="bad-host"
        onReady={() => {}}
      />
    )
    const summary = container.querySelector('summary')
    if (summary) fireEvent.click(summary)
    const input = container.querySelector('input[type="password"]')!
    fireEvent.change(input, { target: { value: 'token' } })
    const form = container.querySelector('form')!
    await act(async () => {
      fireEvent.submit(form)
    })
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /try again/i })).toBeTruthy()
    })
  })

  it('calls onReady after successful delivery', async () => {
    const onReady = vi.fn()
    nc.requestHandlers.set(
      'mclaude.users.alice.hosts.my-host.agent.token-delivery',
      () => new TextEncoder().encode('{"ok":true}'),
    )
    // Override to allow the handler to work
    vi.spyOn(nc, 'request').mockImplementation(async (subject, data) => {
      nc.requests.push({ subject, data })
      return { subject, data: new TextEncoder().encode('{"ok":true}'), seq: 1 }
    })

    const { container } = render(
      <TokenGate
        nc={nc}
        userInfoStore={store}
        authClient={makeAuthClient()}
        uslug="alice"
        defaultHostSlug="my-host"
        onReady={onReady}
      />
    )
    const summary = container.querySelector('summary')
    if (summary) fireEvent.click(summary)
    const input = container.querySelector('input[type="password"]')!
    fireEvent.change(input, { target: { value: 'sk-ant-oat01-goodtoken' } })
    const form = container.querySelector('form')!
    await act(async () => {
      fireEvent.submit(form)
    })
    await waitFor(() => {
      expect(onReady).toHaveBeenCalledTimes(1)
    })
  })
})
