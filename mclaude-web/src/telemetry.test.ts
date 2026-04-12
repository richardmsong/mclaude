import { describe, it, expect, vi } from 'vitest'
import { _setTelemetryReporter, getTelemetry, noopReporter, type TelemetryReporter } from './telemetry'

describe('TelemetryReporter', () => {
  it('defaults to noop reporter', () => {
    // Reset to noop
    _setTelemetryReporter(noopReporter)
    const reporter = getTelemetry()
    // noop doesn't throw
    expect(() => reporter.captureException(new Error('test'))).not.toThrow()
    expect(() => reporter.captureMessage('test')).not.toThrow()
  })

  it('can be replaced with a custom reporter', () => {
    const captured: unknown[] = []
    const customReporter: TelemetryReporter = {
      captureException: (err) => captured.push(err),
      captureMessage: vi.fn(),
    }
    _setTelemetryReporter(customReporter)
    getTelemetry().captureException(new Error('hello'))
    expect(captured).toHaveLength(1)
    expect(captured[0]).toBeInstanceOf(Error)
    // cleanup
    _setTelemetryReporter(noopReporter)
  })
})
