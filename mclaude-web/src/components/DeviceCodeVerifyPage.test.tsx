// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { DeviceCodeVerifyPage } from './DeviceCodeVerifyPage'

// ---------- fetch mock helpers ----------

function mockFetchOk(body: unknown = {}): void {
  globalThis.fetch = vi.fn().mockResolvedValue({
    ok: true,
    status: 200,
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response)
}

function mockFetchError(status: number, body = ''): void {
  globalThis.fetch = vi.fn().mockResolvedValue({
    ok: false,
    status,
    json: () => Promise.resolve({}),
    text: () => Promise.resolve(body),
  } as unknown as Response)
}

// ---------- window.location mock ----------

const origSearch = window.location.search
function setQueryCode(code: string): void {
  Object.defineProperty(window, 'location', {
    value: { ...window.location, search: `?code=${code}` },
    writable: true,
  })
}
function clearQueryCode(): void {
  Object.defineProperty(window, 'location', {
    value: { ...window.location, search: origSearch },
    writable: true,
  })
}

describe('DeviceCodeVerifyPage', () => {
  const onNavigateToLogin = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
    clearQueryCode()
  })

  afterEach(() => {
    clearQueryCode()
  })

  it('redirects to login when not authenticated', () => {
    render(
      <DeviceCodeVerifyPage isAuthenticated={false} onNavigateToLogin={onNavigateToLogin} />,
    )
    expect(onNavigateToLogin).toHaveBeenCalledTimes(1)
  })

  it('renders the form when authenticated', () => {
    render(
      <DeviceCodeVerifyPage isAuthenticated={true} onNavigateToLogin={onNavigateToLogin} />,
    )
    expect(screen.getByTestId('device-code-verify-page')).toBeTruthy()
    expect(screen.getByTestId('device-code-input')).toBeTruthy()
    expect(screen.getByTestId('device-code-verify-button')).toBeTruthy()
  })

  it('pre-fills user code from ?code= query param', () => {
    setQueryCode('ABCD1234')
    render(
      <DeviceCodeVerifyPage isAuthenticated={true} onNavigateToLogin={onNavigateToLogin} />,
    )
    const input = screen.getByTestId('device-code-input') as HTMLInputElement
    expect(input.value).toBe('ABCD1234')
  })

  it('shows success screen on 200 response', async () => {
    mockFetchOk({})
    render(
      <DeviceCodeVerifyPage isAuthenticated={true} onNavigateToLogin={onNavigateToLogin} />,
    )
    const input = screen.getByTestId('device-code-input')
    fireEvent.change(input, { target: { value: 'TESTCODE' } })
    fireEvent.click(screen.getByTestId('device-code-verify-button'))
    await waitFor(() => {
      expect(screen.getByTestId('device-code-success')).toBeTruthy()
    })
  })

  it('shows error message on 400 response', async () => {
    mockFetchError(400)
    render(
      <DeviceCodeVerifyPage isAuthenticated={true} onNavigateToLogin={onNavigateToLogin} />,
    )
    const input = screen.getByTestId('device-code-input')
    fireEvent.change(input, { target: { value: 'BADCODE1' } })
    fireEvent.click(screen.getByTestId('device-code-verify-button'))
    await waitFor(() => {
      expect(screen.getByTestId('device-code-error')).toBeTruthy()
    })
    expect(screen.getByTestId('device-code-error').textContent).toContain('Invalid or expired code')
    // Form is still visible (not replaced by success screen)
    expect(screen.getByTestId('device-code-verify-page')).toBeTruthy()
  })

  it('shows error message on 404 response', async () => {
    mockFetchError(404)
    render(
      <DeviceCodeVerifyPage isAuthenticated={true} onNavigateToLogin={onNavigateToLogin} />,
    )
    const input = screen.getByTestId('device-code-input')
    fireEvent.change(input, { target: { value: 'NOTFOUND' } })
    fireEvent.click(screen.getByTestId('device-code-verify-button'))
    await waitFor(() => {
      expect(screen.getByTestId('device-code-error').textContent).toContain('Invalid or expired code')
    })
  })

  it('shows error message on 410 response', async () => {
    mockFetchError(410)
    render(
      <DeviceCodeVerifyPage isAuthenticated={true} onNavigateToLogin={onNavigateToLogin} />,
    )
    const input = screen.getByTestId('device-code-input')
    fireEvent.change(input, { target: { value: 'EXPIRED1' } })
    fireEvent.click(screen.getByTestId('device-code-verify-button'))
    await waitFor(() => {
      expect(screen.getByTestId('device-code-error').textContent).toContain('Invalid or expired code')
    })
  })

  it('sends POST /api/auth/device-code/verify with userCode', async () => {
    mockFetchOk({})
    render(
      <DeviceCodeVerifyPage isAuthenticated={true} onNavigateToLogin={onNavigateToLogin} />,
    )
    const input = screen.getByTestId('device-code-input')
    fireEvent.change(input, { target: { value: 'MYCODE12' } })
    fireEvent.click(screen.getByTestId('device-code-verify-button'))
    await waitFor(() => screen.getByTestId('device-code-success'))

    expect(globalThis.fetch).toHaveBeenCalledWith(
      '/api/auth/device-code/verify',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ userCode: 'MYCODE12' }),
      }),
    )
  })

  it('verify button is disabled when input is empty', () => {
    render(
      <DeviceCodeVerifyPage isAuthenticated={true} onNavigateToLogin={onNavigateToLogin} />,
    )
    const button = screen.getByTestId('device-code-verify-button') as HTMLButtonElement
    expect(button.disabled).toBe(true)
  })
})
