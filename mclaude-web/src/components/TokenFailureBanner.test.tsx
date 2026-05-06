// @vitest-environment jsdom
/**
 * Tests for TokenFailureBanner (ADR-0092)
 * - renders warning text and [Reconnect] button
 * - [Reconnect] calls onReconnect prop
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { TokenFailureBanner } from './TokenFailureBanner'

describe('TokenFailureBanner', () => {
  it('renders the banner text', () => {
    render(<TokenFailureBanner onReconnect={() => {}} />)
    expect(screen.getByRole('alert')).toBeTruthy()
    expect(screen.getByText(/your claude token is invalid/i)).toBeTruthy()
  })

  it('renders a Reconnect button', () => {
    render(<TokenFailureBanner onReconnect={() => {}} />)
    expect(screen.getByRole('button', { name: /reconnect/i })).toBeTruthy()
  })

  it('calls onReconnect when button is clicked', () => {
    const onReconnect = vi.fn()
    render(<TokenFailureBanner onReconnect={onReconnect} />)
    fireEvent.click(screen.getByRole('button', { name: /reconnect/i }))
    expect(onReconnect).toHaveBeenCalledTimes(1)
  })
})
