import React from 'react'
import ReactDOM from 'react-dom/client'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { App } from '@/components/App'
import { initTelemetry } from '@/telemetry'
import '@/styles/tokens.css'

// Initialize telemetry before rendering (no-op if VITE_SENTRY_DSN not set)
initTelemetry(import.meta.env['VITE_SENTRY_DSN'])

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ErrorBoundary componentName="App">
      <App />
    </ErrorBoundary>
  </React.StrictMode>,
)
