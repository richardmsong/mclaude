import React from 'react'
import { getTelemetry } from '@/telemetry'
import { logger } from '@/logger'

interface Props {
  children: React.ReactNode
  fallback?: React.ReactNode
  componentName?: string
}

interface State {
  hasError: boolean
  error?: Error
}

export class ErrorBoundary extends React.Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = { hasError: false }
  }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, info: React.ErrorInfo): void {
    const component = this.props.componentName ?? 'unknown'
    logger.error(
      { component: 'error-boundary', wrappedComponent: component, err: error.message },
      'React component error caught',
    )
    getTelemetry().captureException(error, {
      componentStack: info.componentStack ?? '',
      componentName: component,
    })
  }

  render(): React.ReactNode {
    if (this.state.hasError) {
      return this.props.fallback ?? (
        <div role="alert">
          <p>Something went wrong.</p>
        </div>
      )
    }
    return this.props.children
  }
}
