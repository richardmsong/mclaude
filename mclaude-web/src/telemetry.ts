import * as Sentry from '@sentry/react'

export interface TelemetryReporter {
  captureException(err: unknown, context?: Record<string, unknown>): void
  captureMessage(msg: string, level?: 'info' | 'warning' | 'error'): void
}

// Real Sentry reporter
const sentryReporter: TelemetryReporter = {
  captureException(err, context) {
    Sentry.withScope((scope) => {
      if (context) scope.setExtras(context)
      Sentry.captureException(err)
    })
  },
  captureMessage(msg, level = 'info') {
    Sentry.captureMessage(msg, level)
  },
}

// No-op reporter (used in tests or when Sentry DSN is not configured)
export const noopReporter: TelemetryReporter = {
  captureException: () => {},
  captureMessage: () => {},
}

let _reporter: TelemetryReporter = noopReporter

export function initTelemetry(dsn?: string): void {
  if (!dsn) return
  Sentry.init({
    dsn,
    environment: import.meta.env.MODE,
    tracesSampleRate: 1.0,
    // Integrate with React error boundaries
    integrations: [Sentry.browserTracingIntegration()],
  })
  _reporter = sentryReporter
}

export function getTelemetry(): TelemetryReporter {
  return _reporter
}

// For testing: inject a custom reporter
export function _setTelemetryReporter(r: TelemetryReporter): void {
  _reporter = r
}
