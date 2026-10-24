import { describe, expect, it, vi, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ErrorBoundary } from './ErrorBoundary'

/** A component that throws, standing in for a page with a bad value. */
function Boom({ shouldThrow }: { shouldThrow: boolean }) {
  if (shouldThrow) throw new TypeError("Cannot read properties of null (reading 'length')")
  return <p>recovered</p>
}

describe('ErrorBoundary', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows a recoverable message instead of unmounting the tree', () => {
    // React logs the caught error; keep the test output clean but still assert
    // the boundary reported it.
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})

    const { container } = render(
      <ErrorBoundary>
        <Boom shouldThrow />
      </ErrorBoundary>,
    )

    // The app is still on screen: an alert with the message and a retry action.
    expect(screen.getByRole('alert')).toBeTruthy()
    expect(screen.getByText(/reading 'length'/)).toBeTruthy()
    expect(screen.getByRole('button')).toBeTruthy()
    expect(container.innerHTML.length).toBeGreaterThan(0)
    expect(spy).toHaveBeenCalled()
  })

  it('renders children normally when nothing throws', () => {
    render(
      <ErrorBoundary>
        <Boom shouldThrow={false} />
      </ErrorBoundary>,
    )
    expect(screen.getByText('recovered')).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('clears the error when the reset key changes', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})

    const { rerender } = render(
      <ErrorBoundary resetKey="/monitoring">
        <Boom shouldThrow />
      </ErrorBoundary>,
    )
    expect(screen.getByRole('alert')).toBeTruthy()

    // Navigating to another route must clear the failure, otherwise the error
    // would follow the user around the app.
    rerender(
      <ErrorBoundary resetKey="/devices">
        <Boom shouldThrow={false} />
      </ErrorBoundary>,
    )
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByText('recovered')).toBeTruthy()
  })

  it('retries the same subtree from the retry button', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})

    const { rerender } = render(
      <ErrorBoundary>
        <Boom shouldThrow />
      </ErrorBoundary>,
    )
    expect(screen.getByRole('alert')).toBeTruthy()

    // The child is fixed (e.g. new data arrived), then the user retries.
    rerender(
      <ErrorBoundary>
        <Boom shouldThrow={false} />
      </ErrorBoundary>,
    )
    fireEvent.click(screen.getByRole('button'))
    expect(screen.getByText('recovered')).toBeTruthy()
  })
})
