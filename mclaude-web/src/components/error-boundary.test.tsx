// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ErrorBoundary } from './ErrorBoundary'
import { _setTelemetryReporter, noopReporter, type TelemetryReporter } from '@/telemetry'

// Mock logger to suppress output in tests
vi.mock('@/logger', () => ({
  logger: {
    info: vi.fn(),
    error: vi.fn(),
    debug: vi.fn(),
    warn: vi.fn(),
  },
}))

// Component that throws
function BrokenComponent({ shouldThrow }: { shouldThrow: boolean }): React.ReactElement {
  if (shouldThrow) throw new Error('test error')
  return <div>ok</div>
}

describe('ErrorBoundary', () => {
  let capturedErrors: Array<{ err: unknown; context?: Record<string, unknown> }> = []
  let mockReporter: TelemetryReporter

  beforeEach(() => {
    capturedErrors = []
    mockReporter = {
      captureException: vi.fn((err, context) => { capturedErrors.push({ err, context }) }),
      captureMessage: vi.fn(),
    }
    _setTelemetryReporter(mockReporter)
    // Suppress React error boundary console.error in tests
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    _setTelemetryReporter(noopReporter)
    vi.restoreAllMocks()
  })

  it('renders children when no error', () => {
    render(
      <ErrorBoundary>
        <BrokenComponent shouldThrow={false} />
      </ErrorBoundary>
    )
    expect(screen.getByText('ok')).toBeDefined()
  })

  it('shows fallback when child throws', () => {
    render(
      <ErrorBoundary fallback={<div>Error occurred</div>}>
        <BrokenComponent shouldThrow={true} />
      </ErrorBoundary>
    )
    expect(screen.getByText('Error occurred')).toBeDefined()
  })

  it('reports error to telemetry', () => {
    render(
      <ErrorBoundary componentName="TestComponent">
        <BrokenComponent shouldThrow={true} />
      </ErrorBoundary>
    )
    expect(mockReporter.captureException).toHaveBeenCalledWith(
      expect.any(Error),
      expect.objectContaining({ componentName: 'TestComponent' })
    )
  })
})
