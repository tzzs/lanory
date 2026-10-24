import { Component, type ErrorInfo, type ReactNode } from 'react'
import { t } from '@/i18n'
import { Button, Card } from '@/components/ui'

/**
 * Catches a render error in a page and shows a recoverable message instead of
 * letting React unmount the whole tree.
 *
 * Without a boundary, one bad value in one page blanks the entire app: React
 * has nowhere to render the error, so it tears the tree down and the user is
 * left with an empty document and no way back. That is exactly what a null
 * array did to the monitoring page.
 *
 * The boundary sits inside the routed content rather than around the app, so a
 * failure keeps the navigation and the rest of the shell usable: the user can
 * switch to another page, and the "Try again" action remounts this one.
 */
interface ErrorBoundaryProps {
  children: ReactNode
  /** Remounts the subtree when it changes, so a navigation clears the error. */
  resetKey?: string
}

interface ErrorBoundaryState {
  error: Error | null
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error }
  }

  componentDidUpdate(prev: ErrorBoundaryProps): void {
    if (prev.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null })
    }
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // Keep the component stack in the console: the message alone rarely says
    // which page produced the bad value.
    console.error('render error', error, info.componentStack)
  }

  private reset = (): void => {
    this.setState({ error: null })
  }

  render(): ReactNode {
    const { error } = this.state
    if (!error) return this.props.children

    return (
      <div role="alert" className="p-6">
        <Card className="mx-auto max-w-lg p-6">
          <h2 className="text-sm font-semibold text-danger">{t('app.error')}</h2>
          <p className="mt-1 text-xs text-muted">
            {t('app.render_error_body')}
          </p>
          <pre className="mt-3 overflow-x-auto rounded bg-surface-2 px-3 py-2 font-mono text-[11px] text-muted">
            {error.message}
          </pre>
          <div className="mt-4">
            <Button variant="primary" onClick={this.reset}>
              {t('app.retry')}
            </Button>
          </div>
        </Card>
      </div>
    )
  }
}
