// @vitest-environment jsdom
/**
 * Tests for ConnectSheet (ADR-0092)
 * - renders OAuth button and manual paste details
 * - [✕] disabled on firstRun
 * - manual paste validates empty input
 * - manual paste calls onTokenObtained with trimmed value
 * - OAuth popup blocked shows error
 * - postMessage with wrong origin is ignored
 * - postMessage with wrong state shows error
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { ConnectSheet } from './ConnectSheet'
import type { AuthClient } from '@/transport/auth-client'

function makeAuthClient(overrides?: Partial<AuthClient>): AuthClient {
  return {
    exchangeOAuthCode: vi.fn().mockResolvedValue('sk-ant-oat01-exchanged'),
    getGrants: vi.fn().mockResolvedValue([]),
    getUserAgentPubkey: vi.fn().mockResolvedValue('AAAA'),
    ...overrides,
  } as unknown as AuthClient
}

describe('ConnectSheet', () => {
  beforeEach(() => {
    // Reset crypto.getRandomValues mock — use real implementation
    vi.restoreAllMocks()
  })

  it('renders Sign in with Claude button', () => {
    render(
      <ConnectSheet onTokenObtained={() => {}} authClient={makeAuthClient()} />
    )
    expect(screen.getByText(/sign in with claude/i)).toBeTruthy()
  })

  it('renders the manual paste section', () => {
    render(
      <ConnectSheet onTokenObtained={() => {}} authClient={makeAuthClient()} />
    )
    expect(screen.getByText(/or enter a token manually/i)).toBeTruthy()
  })

  it('[✕] is disabled on firstRun', () => {
    render(
      <ConnectSheet firstRun onTokenObtained={() => {}} authClient={makeAuthClient()} />
    )
    const closeBtn = screen.getByRole('button', { name: /close/i })
    expect(closeBtn).toHaveProperty('disabled', true)
  })

  it('[✕] is enabled when firstRun is false', () => {
    const onDismiss = vi.fn()
    render(
      <ConnectSheet firstRun={false} onTokenObtained={() => {}} onDismiss={onDismiss} authClient={makeAuthClient()} />
    )
    const closeBtn = screen.getByRole('button', { name: /close/i })
    expect(closeBtn).toHaveProperty('disabled', false)
    fireEvent.click(closeBtn)
    expect(onDismiss).toHaveBeenCalled()
  })

  it('manual paste empty shows validation error', async () => {
    const { container } = render(
      <ConnectSheet onTokenObtained={() => {}} authClient={makeAuthClient()} />
    )
    // Open the details
    const summary = screen.getByText(/or enter a token manually/i)
    fireEvent.click(summary)

    // Submit empty form
    const form = container.querySelector('form')!
    await act(async () => {
      fireEvent.submit(form)
    })
    expect(screen.getByText(/please enter a valid token/i)).toBeTruthy()
  })

  it('manual paste calls onTokenObtained with trimmed token', async () => {
    const onTokenObtained = vi.fn()
    const { container } = render(
      <ConnectSheet onTokenObtained={onTokenObtained} authClient={makeAuthClient()} />
    )
    const summary = screen.getByText(/or enter a token manually/i)
    fireEvent.click(summary)

    const input = container.querySelector('input[type="password"]')!
    fireEvent.change(input, { target: { value: '  sk-ant-oat01-abc123  ' } })

    const form = container.querySelector('form')!
    await act(async () => {
      fireEvent.submit(form)
    })
    expect(onTokenObtained).toHaveBeenCalledWith('sk-ant-oat01-abc123')
  })

  it('OAuth popup blocked shows error', async () => {
    // window.open returns null (popup blocked)
    vi.spyOn(window, 'open').mockReturnValue(null)

    render(
      <ConnectSheet onTokenObtained={() => {}} authClient={makeAuthClient()} />
    )
    const oauthBtn = screen.getByText(/sign in with claude/i)
    await act(async () => {
      fireEvent.click(oauthBtn)
    })
    await waitFor(() => {
      expect(screen.getByText(/allow popups for this site/i)).toBeTruthy()
    })
  })

  it('postMessage with wrong origin is ignored', async () => {
    const onTokenObtained = vi.fn()
    render(
      <ConnectSheet onTokenObtained={onTokenObtained} authClient={makeAuthClient()} />
    )
    // Fire message from different origin
    await act(async () => {
      window.dispatchEvent(new MessageEvent('message', {
        origin: 'https://evil.example.com',
        data: { type: 'mclaude:oauth-callback', code: 'evil-code', state: 'evil-state' },
      }))
    })
    expect(onTokenObtained).not.toHaveBeenCalled()
  })

  it('postMessage with wrong state shows error', async () => {
    // Mock window.open to return a fake popup
    const fakePopup = { closed: false } as Window
    vi.spyOn(window, 'open').mockReturnValue(fakePopup)

    render(
      <ConnectSheet onTokenObtained={() => {}} authClient={makeAuthClient()} />
    )
    const oauthBtn = screen.getByText(/sign in with claude/i)
    await act(async () => {
      fireEvent.click(oauthBtn)
    })

    // Send message with mismatched state
    await act(async () => {
      window.dispatchEvent(new MessageEvent('message', {
        origin: window.location.origin,
        data: { type: 'mclaude:oauth-callback', code: 'some-code', state: 'WRONG_STATE' },
      }))
    })
    await waitFor(() => {
      expect(screen.getByText(/state mismatch/i)).toBeTruthy()
    })
  })
})
